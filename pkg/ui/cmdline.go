package ui

import "strings"

// ParseCmdLine turns a user-supplied command line into an argv-style slice.
//
// If line contains any shell metacharacter that requires a shell to
// interpret (pipes, redirections, command/process substitution, glob,
// tilde expansion, env-var expansion, &&/||/; chaining, etc.), the
// returned argv is ["bash", "-c", line] so bash can interpret it as
// intended.
//
// Otherwise the result is strings.Fields(line) (simple whitespace split),
// which is enough for plain commands like "bash" or "ssh user@host".
//
// This fixes cases like `bash --rcfile <(echo "cd /mnt/repos")` that
// silently broke when started directly via exec.Command, because
// process substitution `<(...)` is a bash-only feature and the literal
// tokens land in the child as filenames.
func ParseCmdLine(line string) []string {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}
	if needsShell(line) {
		return []string{"bash", "-c", line}
	}
	return strings.Fields(line)
}

// parseCmdLine is the package-private alias used by internal callers.
func parseCmdLine(line string) []string { return ParseCmdLine(line) }

// needsShell reports whether line contains characters that only a shell
// can interpret correctly.
func needsShell(line string) bool {
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '|', '&', ';', '<', '>', '(', ')', '$', '`', '*', '?', '[', ']', '{', '}', '~', '"', '\'', '\\':
			return true
		}
	}
	return false
}
