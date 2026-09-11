package session

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/charmbracelet/x/vt"
)

const marginTestContent = "\x1b[Habcdefghij\x1b[2;1Hklmnopqrst\x1b[3;1HuvwxyzABCD" +
	"\x1b[4;1HEFGHIJKLMN\x1b[5;1HOPQRSTUVWX\x1b[6;1HYZ01234567"

func TestResizeReflowRejectsOutOfBoundsScrollMargins(t *testing.T) {
	for _, tt := range []struct {
		name    string
		mode    string
		margins string
	}{
		{name: "vertical", margins: "\x1b[1;60r"},
		{name: "horizontal", mode: "\x1b[?69h", margins: "\x1b[1;160s"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			const output = "\x1b[Hfirst\x1b[2;1Hsecond\x1b[H\x1b[13Mafter"
			raw := []byte(tt.mode + tt.margins + output)
			s := NewVTScreen(160, 60)
			s.Write(raw)
			s.Resize(136, 44)

			// At the smaller size the old margin command is invalid and
			// must leave the existing (full-screen) scroll region intact.
			want := vt.NewEmulator(136, 44)
			defer want.Close()
			_, _ = want.Write([]byte(tt.mode + output))
			if got := s.Render(); got != want.Render() {
				t.Fatalf("reflow render = %q, want %q", got, want.Render())
			}
			if !bytes.Equal(s.RawSnapshot(), raw) {
				t.Fatal("margin handling changed raw browser replay bytes")
			}

			s.Resize(160, 60)
			want.Resize(160, 60)
			_, _ = want.Write(append([]byte("\x1bc"), raw...))
			if got := s.Render(); got != want.Render() {
				t.Fatalf("expanded render = %q, want %q", got, want.Render())
			}
		})
	}
}

func TestLiveOutputRejectsOutOfBoundsScrollMargins(t *testing.T) {
	for _, tt := range []struct {
		name    string
		valid   string
		invalid string
	}{
		{name: "bottom", valid: "\x1b[2;5r", invalid: "\x1b[1;7r"},
		{name: "top", valid: "\x1b[2;5r", invalid: "\x1b[7;8r"},
		{name: "right", valid: "\x1b[?69h\x1b[2;10s", invalid: "\x1b[1;13s"},
		{name: "left", valid: "\x1b[?69h\x1b[2;10s", invalid: "\x1b[13;14s"},
	} {
		for _, op := range []struct {
			name string
			data string
		}{
			{name: "delete-line", data: "\x1b[2;2H\x1b[M"},
			{name: "insert-line", data: "\x1b[2;2H\x1b[L"},
			{name: "scroll-up", data: "\x1b[S"},
			{name: "scroll-down", data: "\x1b[T"},
			{name: "delete-character", data: "\x1b[2;2H\x1b[P"},
			{name: "insert-character", data: "\x1b[2;2H\x1b[@"},
			{name: "line-feed", data: "\x1b[5;2H\n"},
			{name: "reverse-index", data: "\x1b[2;2H\x1bM"},
		} {
			t.Run(tt.name+"/"+op.name, func(t *testing.T) {
				raw := []byte(marginTestContent + tt.valid + tt.invalid + op.data)
				s := NewVTScreen(12, 6)
				for _, b := range raw {
					s.Write([]byte{b})
				}
				want := vt.NewEmulator(12, 6)
				defer want.Close()
				_, _ = want.Write([]byte(marginTestContent + tt.valid + op.data))
				if got := s.Render(); got != want.Render() {
					t.Fatalf("render = %q, want %q", got, want.Render())
				}
				if !bytes.Equal(s.RawSnapshot(), raw) {
					t.Fatal("margin handling changed raw browser replay bytes")
				}
			})
		}
	}
}

func TestValidScrollMarginsMatchEmulator(t *testing.T) {
	for i, margins := range []string{
		"\x1b[2;5r", "\x1b[r", "\x1b[0;0r", "\x1b[2r", "\x1b[;5r",
		"\x1b[?69h\x1b[2;10s", "\x1b[?69h\x1b[s",
		"\x1b[?69h\x1b[0;0s", "\x1b[?69h\x1b[2s", "\x1b[?69h\x1b[;10s",
	} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			raw := []byte(marginTestContent + margins + "\x1b[3;3H\x1b[M")
			s := NewVTScreen(12, 6)
			s.Write(raw)
			want := vt.NewEmulator(12, 6)
			defer want.Close()
			_, _ = want.Write(raw)
			if got := s.Render(); got != want.Render() {
				t.Fatalf("margins %q: render = %q, want %q", margins, got, want.Render())
			}
		})
	}
}

func TestMarginGuardsPreserveCursorSave(t *testing.T) {
	for _, tt := range []struct {
		name string
		mode string
	}{
		{name: "default"},
		{name: "disabled", mode: "\x1b[?69h\x1b[?69l"},
		{name: "reset", mode: "\x1b[?69h\x1bc"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := NewVTScreen(12, 6)
			s.Write([]byte(tt.mode + "\x1b[3;4H\x1b[999;999s\x1b[H\x1b8X"))
			cell := s.term.CellAt(3, 2)
			if cell == nil || cell.Content != "X" {
				t.Fatalf("CSI s did not save cursor: %q", s.Render())
			}
		})
	}
}
