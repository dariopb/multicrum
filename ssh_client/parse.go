package ssh_client

import (
	"fmt"
	"net"
	"strings"
)

// Target is an OpenSSH-like destination: [user@]host[:port].
type Target struct {
	User string
	Host string
	Port string
}

// ParseTarget accepts host, user@host, host:port, user@host:port, and
// bracketed IPv6 forms such as user@[2001:db8::1]:2222.
func ParseTarget(value string) (Target, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return Target{}, nil
	}
	var target Target
	if at := strings.LastIndex(value, "@"); at >= 0 {
		target.User = value[:at]
		value = value[at+1:]
		if target.User == "" {
			return Target{}, fmt.Errorf("invalid SSH target %q: empty user", value)
		}
	}
	if strings.HasPrefix(value, "[") {
		host, port, err := net.SplitHostPort(value)
		if err == nil {
			target.Host = host
			target.Port = port
			return target, nil
		}
		if strings.HasSuffix(value, "]") {
			target.Host = strings.TrimPrefix(strings.TrimSuffix(value, "]"), "[")
			return target, nil
		}
		return Target{}, fmt.Errorf("invalid SSH target %q: %w", value, err)
	}
	// A single colon means host:port. Multiple colons means unbracketed IPv6,
	// which OpenSSH accepts as a host without an inline port.
	if strings.Count(value, ":") == 1 {
		host, port, err := net.SplitHostPort(value)
		if err != nil {
			parts := strings.SplitN(value, ":", 2)
			target.Host = parts[0]
			target.Port = parts[1]
		} else {
			target.Host = host
			target.Port = port
		}
	} else {
		target.Host = value
	}
	if target.Host == "" {
		return Target{}, fmt.Errorf("invalid SSH target: empty host")
	}
	return target, nil
}
