package localserver

import "regexp"

var safeNameRe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func sanitize(s string) string { return safeNameRe.ReplaceAllString(s, "_") }
