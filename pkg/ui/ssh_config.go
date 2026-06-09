package ui

import (
	"strings"

	"multicrum/pkg/config"
	"multicrum/pkg/ssh_client"
)

func sshClientFromConfig(entry *config.SSHEntry, cmd []string) (*ssh_client.Client, error) {
	if entry == nil {
		return nil, nil
	}
	return ssh_client.New(ssh_client.Options{
		Target:                strings.TrimSpace(entry.Target),
		Port:                  strings.TrimSpace(entry.Port),
		IdentityFile:          strings.TrimSpace(entry.Key),
		UseDefaultKeys:        entry.UseDefaultKeys,
		UseAgent:              entry.UseAgent,
		KnownHosts:            strings.TrimSpace(entry.KnownHosts),
		InsecureIgnoreHostKey: entry.InsecureIgnoreHostKey,
		Command:               cmd,
	})
}

func sshEntryFromResolved(cfg ssh_client.ResolvedConfig) *config.SSHEntry {
	target := strings.TrimSpace(cfg.Target)
	if target == "" {
		target = cfg.Host
		if cfg.User != "" {
			target = cfg.User + "@" + target
		}
	}
	entry := &config.SSHEntry{
		Target:                target,
		Port:                  cfg.Port,
		Key:                   cfg.ExplicitIdentityFile,
		UseDefaultKeys:        cfg.UseDefaultKeys,
		UseAgent:              cfg.UseAgent,
		KnownHosts:            cfg.KnownHosts,
		InsecureIgnoreHostKey: cfg.InsecureIgnoreHostKey,
	}
	return entry
}
