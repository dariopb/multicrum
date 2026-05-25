// Package ssh_client provides a reusable SSH client and interactive remote PTY
// backend for multicrum/session and other applications.
//
// The package intentionally does not reimplement SSH. It builds on
// golang.org/x/crypto/ssh, golang.org/x/crypto/ssh/agent,
// github.com/kevinburke/ssh_config, and github.com/skeema/knownhosts.
//
// Typical use:
//
//	client, err := ssh_client.New(ssh_client.Options{
//	    Target:       "user@example.com:22",
//	    IdentityFile: "/home/me/.ssh/id_ed25519",
//	})
//	if err != nil {
//	    // handle config/auth/known_hosts error
//	}
//	remote, err := client.Start(120, 40)
//	if err != nil {
//	    // handle connection/session error
//	}
//	defer remote.Close()
//
// RemoteSession implements io.ReadWriteCloser and adds Resize and Done. Reads
// return remote stdout/stderr bytes, writes send terminal input, Resize forwards
// SSH window-change requests, and Done closes when the remote process exits.
//
// Target parsing accepts OpenSSH-like forms: host, user@host, host:port,
// user@host:port, and bracketed IPv6. Resolve applies explicit options,
// ~/.ssh/config, /etc/ssh/ssh_config, known_hosts verification, and auth method
// construction.
//
// Authentication precedence is explicit: an explicit key uses only that key; an
// explicit password uses only password and keyboard-interactive password auth;
// SSH config identities, default keys, and SSH agent are considered only when no
// explicit key/password is supplied.
package ssh_client
