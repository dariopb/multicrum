package session

import (
	"strings"
	"testing"
)

func BenchmarkVTScreenSustainedWrite(b *testing.B) {
	s := NewVTScreen(120, 40)
	chunk := []byte(strings.Repeat("continuous terminal output\r\n", 128))
	for written := 0; written < maxScrollback+len(chunk); written += len(chunk) {
		s.Write(chunk)
	}

	b.ReportAllocs()
	b.SetBytes(int64(len(chunk)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.Write(chunk)
	}
}
