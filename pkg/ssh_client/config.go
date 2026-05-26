package ssh_client

import (
	"fmt"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	sshconfig "github.com/kevinburke/ssh_config"
	"github.com/skeema/knownhosts"
	"golang.org/x/crypto/ssh"
)

const defaultPort = "22"

var defaultIdentityNames = []string{
	"id_ed25519",
	"id_ecdsa",
	"id_rsa",
	"id_dsa",
}

// Options controls how an SSH-backed session is resolved and started.
type Options struct {
	Target                string
	User                  string
	Host                  string
	Port                  string
	IdentityFile          string
	Password              string
	UseDefaultKeys        bool
	UseAgent              bool
	KnownHosts            string
	InsecureIgnoreHostKey bool
	Command               []string
}

// ResolvedConfig is a concrete ssh.ClientConfig plus metadata needed to start
// interactive sessions.
type ResolvedConfig struct {
	Target        string
	Alias         string
	User          string
	Host          string
	Port          string
	Addr          string
	IdentityFiles []string
	Command       []string
	ClientConfig  *ssh.ClientConfig
}

// Resolve parses target/options, applies ~/.ssh/config, discovers auth methods,
// and builds an ssh.ClientConfig suitable for ssh.Dial.
func Resolve(opts Options) (ResolvedConfig, error) {
	parsed, err := ParseTarget(opts.Target)
	if err != nil {
		return ResolvedConfig{}, err
	}
	alias := parsed.Host
	if alias == "" {
		alias = opts.Host
	}
	if alias == "" {
		return ResolvedConfig{}, fmt.Errorf("ssh target host is required")
	}

	userName := firstNonEmpty(opts.User, parsed.User, sshConfigValue(alias, "User"), currentUsername())
	host := firstNonEmpty(opts.Host, sshConfigValue(alias, "HostName"), parsed.Host)
	port := firstNonEmpty(opts.Port, parsed.Port, sshConfigValue(alias, "Port"), defaultPort)
	if userName == "" {
		return ResolvedConfig{}, fmt.Errorf("ssh user is required")
	}
	if host == "" {
		return ResolvedConfig{}, fmt.Errorf("ssh host is required")
	}
	if _, err := strconv.Atoi(port); err != nil {
		return ResolvedConfig{}, fmt.Errorf("invalid ssh port %q: %w", port, err)
	}

	identityFiles := resolveIdentityFiles(alias, opts)
	authMethods, err := buildAuthMethods(identityFiles, opts.Password, opts.UseAgent)
	if err != nil {
		return ResolvedConfig{}, err
	}
	if len(authMethods) == 0 {
		return ResolvedConfig{}, fmt.Errorf("no SSH auth methods available; provide --ssh-key, --ssh-passwd, --ssh-use-default-keys, or SSH_AUTH_SOCK")
	}

	addr := net.JoinHostPort(host, port)
	hostKeyCallback, hostKeyAlgorithms, err := hostKeyConfig(addr, opts)
	if err != nil {
		return ResolvedConfig{}, err
	}

	clientConfig := &ssh.ClientConfig{
		User:              userName,
		Auth:              authMethods,
		HostKeyCallback:   hostKeyCallback,
		HostKeyAlgorithms: hostKeyAlgorithms,
		Timeout:           15 * time.Second,
	}

	return ResolvedConfig{
		Target:        opts.Target,
		Alias:         alias,
		User:          userName,
		Host:          host,
		Port:          port,
		Addr:          addr,
		IdentityFiles: identityFiles,
		Command:       append([]string(nil), opts.Command...),
		ClientConfig:  clientConfig,
	}, nil
}

func sshConfigValue(alias, key string) string {
	value, err := sshconfig.GetStrict(alias, key)
	if err != nil {
		return ""
	}
	return expandPath(value)
}

func sshConfigValues(alias, key string) []string {
	values, err := sshconfig.GetAllStrict(alias, key)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = expandPath(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func resolveIdentityFiles(alias string, opts Options) []string {
	seen := make(map[string]struct{})
	var files []string
	add := func(path string) {
		path = expandPath(path)
		if path == "" {
			return
		}
		if _, ok := seen[path]; ok {
			return
		}
		seen[path] = struct{}{}
		files = append(files, path)
	}
	if opts.IdentityFile != "" {
		add(opts.IdentityFile)
		return files
	}
	if opts.Password != "" {
		return files
	}
	for _, path := range sshConfigValues(alias, "IdentityFile") {
		add(path)
	}
	if opts.UseDefaultKeys {
		for _, path := range defaultIdentityFiles() {
			add(path)
		}
	}
	return files
}

func defaultIdentityFiles() []string {
	home := homeDir()
	if home == "" {
		return nil
	}
	out := make([]string, 0, len(defaultIdentityNames))
	for _, name := range defaultIdentityNames {
		out = append(out, filepath.Join(home, ".ssh", name))
	}
	return out
}

func hostKeyConfig(addr string, opts Options) (ssh.HostKeyCallback, []string, error) {
	if opts.InsecureIgnoreHostKey {
		return ssh.InsecureIgnoreHostKey(), nil, nil
	}
	files := knownHostFiles(opts.KnownHosts)
	if len(files) == 0 {
		return nil, nil, fmt.Errorf("no known_hosts file found; refusing insecure SSH host key verification")
	}
	db, err := knownhosts.NewDB(files...)
	if err != nil {
		return nil, nil, fmt.Errorf("load known_hosts: %w", err)
	}
	return db.HostKeyCallback(), db.HostKeyAlgorithms(addr), nil
}

func knownHostFiles(override string) []string {
	if override != "" {
		return []string{expandPath(override)}
	}
	candidates := []string{}
	if home := homeDir(); home != "" {
		candidates = append(candidates, filepath.Join(home, ".ssh", "known_hosts"))
	}
	candidates = append(candidates, "/etc/ssh/ssh_known_hosts")
	var files []string
	for _, path := range candidates {
		if st, err := os.Stat(path); err == nil && !st.IsDir() {
			files = append(files, path)
		}
	}
	return files
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func currentUsername() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		if i := strings.LastIndexAny(u.Username, `\\/`); i >= 0 {
			return u.Username[i+1:]
		}
		return u.Username
	}
	return os.Getenv("USER")
}

func homeDir() string {
	if home := os.Getenv("HOME"); home != "" {
		return home
	}
	if u, err := user.Current(); err == nil {
		return u.HomeDir
	}
	return ""
}

func expandPath(path string) string {
	if path == "" {
		return ""
	}
	if strings.HasPrefix(path, "~/") || path == "~" {
		home := homeDir()
		if home == "" {
			return path
		}
		if path == "~" {
			return home
		}
		return filepath.Join(home, path[2:])
	}
	return os.ExpandEnv(path)
}
