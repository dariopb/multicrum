package ssh_client

import (
	"fmt"
	"net"
	"os"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

func buildAuthMethods(identityFiles []string, password string, useAgent bool) ([]ssh.AuthMethod, error) {
	var methods []ssh.AuthMethod
	if len(identityFiles) > 0 {
		for _, path := range identityFiles {
			method, err := keyAuthMethod(path)
			if err != nil {
				return nil, err
			}
			methods = append(methods, method)
		}
	} else if password == "" && useAgent {
		if method := agentAuthMethod(); method != nil {
			methods = append(methods, method)
		}
	}
	if password != "" {
		methods = append(methods, ssh.Password(password))
		methods = append(methods, ssh.KeyboardInteractive(func(_ string, _ string, questions []string, echos []bool) ([]string, error) {
			answers := make([]string, len(questions))
			for i := range answers {
				if i < len(echos) && !echos[i] {
					answers[i] = password
				}
			}
			return answers, nil
		}))
	}
	return methods, nil
}

func agentAuthMethod() ssh.AuthMethod {
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return nil
	}
	return ssh.PublicKeysCallback(func() ([]ssh.Signer, error) {
		conn, err := net.Dial("unix", sock)
		if err != nil {
			return nil, err
		}
		client := agent.NewClient(conn)
		return client.Signers()
	})
}

func keyAuthMethod(path string) (ssh.AuthMethod, error) {
	pemBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read identity file %s: %w", path, err)
	}
	signer, err := ssh.ParsePrivateKey(pemBytes)
	if err != nil {
		if _, ok := err.(*ssh.PassphraseMissingError); ok {
			return nil, fmt.Errorf("identity file %s requires a passphrase; passphrase-protected keys are not supported yet", path)
		}
		return nil, fmt.Errorf("parse identity file %s: %w", path, err)
	}
	return ssh.PublicKeys(signer), nil
}
