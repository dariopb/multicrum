package ui

import (
	"strings"
	"testing"

	"multicrum/pkg/session"
)

func benchModel(b *testing.B, cols, rows int) *Model {
	m := NewModel([]string{"bash"}, cols, rows)
	m.s.manager = session.NewManager(cols, rows-2, nil, nil)
	if _, err := m.s.manager.New([]string{"sh"}); err != nil {
		b.Fatalf("new session: %v", err)
	}
	// Fill the screen with colorful-ish content.
	sess := m.s.manager.Focused()
	var sb strings.Builder
	for i := 0; i < rows; i++ {
		sb.WriteString("\x1b[32m")
		sb.WriteString(strings.Repeat("x", cols-1))
		sb.WriteString("\x1b[0m\r\n")
	}
	sess.Screen().Write([]byte(sb.String()))
	m.s.ensureViewport(0, cols, rows+2)
	vp := m.s.viewports[0]
	vp.SetContent(sess.Screen().Render())
	m.s.viewports[0] = vp
	return m
}

func BenchmarkView(b *testing.B) {
	m := benchModel(b, 120, 40)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.View()
	}
}

func BenchmarkScreenRender(b *testing.B) {
	m := benchModel(b, 120, 40)
	sess := m.s.manager.Focused()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = sess.Screen().Render()
	}
}

func BenchmarkRenderPaneContent(b *testing.B) {
	m := benchModel(b, 120, 40)
	vp := m.s.viewports[0]
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.s.renderPaneContent(vp, 120, 38, false)
	}
}

// The cache-hit path is what most View() calls exercise between render frames.
func BenchmarkRenderPaneContentCached(b *testing.B) {
	m := benchModel(b, 120, 40)
	vp := m.s.viewports[0]
	_ = m.s.renderPaneContent(vp, 120, 38, false) // prime cache
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.s.renderPaneContent(vp, 120, 38, false)
	}
}
