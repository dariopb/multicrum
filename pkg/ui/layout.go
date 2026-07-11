package ui

type connectionLayout string

const (
	connectionLayoutBottom connectionLayout = "bottom"
	connectionLayoutLeft   connectionLayout = "left"

	connectionRailWidth        = 15
	minimumConnectionRailWidth = 8
	connectionRailDividerWidth = 1
	minimumMainPaneWidth       = 10
)

type rect struct {
	X, Y          int
	Width, Height int
}

type layoutGeometry struct {
	Screen            rect
	ConnectionRail    rect
	ConnectionDivider rect
	TabBar            rect
	Pane              rect
	StatusBar         rect
}

func normalizedConnectionLayout(value string) connectionLayout {
	if connectionLayout(value) == connectionLayoutLeft {
		return connectionLayoutLeft
	}
	return connectionLayoutBottom
}

func (s *state) geometry() layoutGeometry {
	width, height := s.width, s.height
	if width < 1 {
		width = 1
	}
	// A tab row, pane row, and status row are each always addressable.
	if height < 3 {
		height = 3
	}
	g := layoutGeometry{
		Screen: rect{Width: width, Height: height},
	}
	if s.connectionLayout == connectionLayoutLeft &&
		width >= minimumConnectionRailWidth+connectionRailDividerWidth+minimumMainPaneWidth {
		railWidth := s.connectionRailWidth
		if railWidth <= 0 {
			railWidth = connectionRailWidth
		}
		maxRailWidth := width - connectionRailDividerWidth - minimumMainPaneWidth
		railWidth = min(max(railWidth, minimumConnectionRailWidth), maxRailWidth)
		mainX := railWidth + connectionRailDividerWidth
		mainWidth := width - mainX
		g.ConnectionRail = rect{X: 0, Y: 0, Width: railWidth, Height: height}
		g.ConnectionDivider = rect{X: railWidth, Y: 0, Width: connectionRailDividerWidth, Height: height}
		g.TabBar = rect{X: mainX, Y: 0, Width: mainWidth, Height: 1}
		g.Pane = rect{X: mainX, Y: 1, Width: mainWidth, Height: height - 1}
		return g
	}
	g.TabBar = rect{X: 0, Y: 0, Width: width, Height: 1}
	g.Pane = rect{X: 0, Y: 1, Width: width, Height: height - 2}
	g.StatusBar = rect{X: 0, Y: height - 1, Width: width, Height: 1}
	return g
}

func (s *state) resizeConnectionRail(width int) {
	maxWidth := s.width - connectionRailDividerWidth - minimumMainPaneWidth
	if maxWidth < minimumConnectionRailWidth {
		return
	}
	width = min(max(width, minimumConnectionRailWidth), maxWidth)
	if width == s.connectionRailWidth {
		return
	}
	s.connectionRailWidth = width
	s.applyGeometry()
}

func (g layoutGeometry) Contains(r rect, x, y int) bool {
	return x >= r.X && x < r.X+r.Width && y >= r.Y && y < r.Y+r.Height
}

func (g layoutGeometry) ScreenToPane(x, y int) (paneX, paneY int, ok bool) {
	if !g.Contains(g.Pane, x, y) {
		return 0, 0, false
	}
	return x - g.Pane.X, y - g.Pane.Y, true
}

func (g layoutGeometry) PaneToScreen(x, y int) (screenX, screenY int) {
	return g.Pane.X + x, g.Pane.Y + y
}

func (s *state) usingLeftRail() bool {
	return s.geometry().ConnectionRail.Width > 0
}
