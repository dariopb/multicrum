package transport

import (
	"strings"
	"testing"
)

func TestWebTerminalDefaultsToSteadyCursor(t *testing.T) {
	html := indexHTML("")
	if !strings.Contains(html, "cursorBlink:false") {
		t.Fatal("web terminal does not default to a steady cursor")
	}
}
