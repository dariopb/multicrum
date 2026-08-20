package control

import (
	"crypto/rand"
	"encoding/hex"
	"regexp"
	"strings"
)

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func NewID(prefix string) string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return prefix + "_" + hex.EncodeToString(b[:])
}

func NewToken() string { return NewID("mct") }

func sanitize(value string) string {
	return unsafeName.ReplaceAllString(value, "_")
}

func TokenPath(endpoint string) string {
	return strings.TrimSuffix(endpoint, ".sock") + ".token"
}
