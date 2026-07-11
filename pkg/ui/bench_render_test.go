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
		_ = m.s.renderPaneContent(0, vp, 120, 38, false)
	}
}

// The cache-hit path is what most View() calls exercise between render frames.
func BenchmarkRenderPaneContentCached(b *testing.B) {
	m := benchModel(b, 120, 40)
	vp := m.s.viewports[0]
	_ = m.s.renderPaneContent(0, vp, 120, 38, false) // prime cache
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.s.renderPaneContent(0, vp, 120, 38, false)
	}
}

func benchmarkScrollbackViewport(b *testing.B, width int) (*Model, string) {
	m := NewModel([]string{"bash"}, width, 40)
	m.s.ensureViewport(0, width, 40)
	vp := m.s.viewports[0]
	vp.SoftWrap = true

	var content strings.Builder
	for i := 0; i < 10000; i++ {
		content.WriteString(strings.Repeat("scrollback ", 14))
		content.WriteByte('\n')
	}
	text := content.String()
	m.s.setScrollbackContent(0, vp, text)
	return m, text
}

func BenchmarkRenderScrollbackOffset(b *testing.B) {
	for _, tc := range []struct {
		name  string
		width int
	}{
		{name: "horizontal", width: 120},
		{name: "vertical", width: 64},
	} {
		b.Run(tc.name, func(b *testing.B) {
			m, _ := benchmarkScrollbackViewport(b, tc.width)
			vp := m.s.viewports[0]
			_ = m.s.renderPaneContent(0, vp, tc.width, 38, true)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				vp.SetYOffset(i % 500)
				_ = m.s.renderPaneContent(0, vp, tc.width, 38, true)
			}
		})
	}
}

func BenchmarkRenderScrollbackOffsetUncached(b *testing.B) {
	for _, tc := range []struct {
		name  string
		width int
	}{
		{name: "horizontal", width: 120},
		{name: "vertical", width: 64},
	} {
		b.Run(tc.name, func(b *testing.B) {
			_, content := benchmarkScrollbackViewport(b, tc.width)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = renderPaneContentUncached(content, tc.width, 38, true, i%500)
			}
		})
	}
}
