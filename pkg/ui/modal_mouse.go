package ui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"multicrum/pkg/session"
)

type modalGeometry struct {
	Box     rect
	Content rect
}

type modalAction struct {
	label string
	key   tea.KeyPressMsg
}

func (s *state) centeredModalOpen() bool {
	switch s.mode {
	case modeHelp, modeRenaming, modeExitPrompt, modeNewSession, modeSelecting,
		modeConnections, modeQuitConfirm, modeDeleteConfirm, modeFilePicker, modeSettings:
		return true
	}
	return false
}

func (m Model) centeredModalGeometry(box string) modalGeometry {
	pane := m.s.geometry().Pane
	width, height := lipgloss.Width(box), lipgloss.Height(box)
	left := pane.X + max(0, (pane.Width-width)/2)
	top := pane.Y + max(0, (pane.Height-height)/2)
	paddingTop, paddingRight, paddingBottom, paddingLeft := helpModalStyle.GetPadding()
	content := rect{
		X:      left + helpModalStyle.GetBorderLeftSize() + paddingLeft,
		Y:      top + helpModalStyle.GetBorderTopSize() + paddingTop,
		Width:  width - helpModalStyle.GetBorderLeftSize() - helpModalStyle.GetBorderRightSize() - paddingLeft - paddingRight,
		Height: height - helpModalStyle.GetBorderTopSize() - helpModalStyle.GetBorderBottomSize() - paddingTop - paddingBottom,
	}
	return modalGeometry{
		Box:     rect{X: left, Y: top, Width: width, Height: height},
		Content: content,
	}
}

func (m Model) currentModalBox() string {
	switch m.s.mode {
	case modeHelp:
		return m.renderHelpModal()
	case modeRenaming:
		return m.renderRenameModal()
	case modeExitPrompt:
		return m.renderExitModal()
	case modeNewSession:
		return m.renderNewSessionModal()
	case modeFilePicker:
		return m.renderFilePickerModal()
	case modeSelecting:
		return m.renderSessionSelectorModal()
	case modeConnections:
		return m.renderConnectionsModal()
	case modeSettings:
		return m.renderSettingsModal()
	case modeQuitConfirm:
		return m.renderQuitConfirmModal()
	case modeDeleteConfirm:
		return m.renderDeleteConfirmModal()
	}
	return ""
}

func (s *state) handleModalMouse(m Model, ev mouseEvent) tea.Cmd {
	box := m.currentModalBox()
	geometry := m.centeredModalGeometry(box)
	contentX := ev.X - geometry.Content.X
	contentY := ev.Y - geometry.Content.Y

	if s.mouseDrag.kind == mouseDragSessionRow || s.mouseDrag.kind == mouseDragConnectionRow {
		switch ev.Action {
		case mouseMotion:
			s.updateModalDrag(contentY)
		case mouseRelease:
			return s.finishModalDrag(m, contentY)
		}
		return nil
	}
	if ev.Action != mousePress || ev.Button != tea.MouseLeft ||
		!s.geometry().Contains(geometry.Content, ev.X, ev.Y) ||
		!s.geometry().Contains(s.geometry().Pane, ev.X, ev.Y) {
		return nil
	}

	switch s.mode {
	case modeHelp:
		if contentY == geometry.Content.Height-1 {
			s.handleHelpKey(enterKey())
		}
	case modeRenaming:
		if contentY == geometry.Content.Height-1 {
			if key, ok := modalActionAt(contentX, "Enter save   Esc cancel", []modalAction{
				{label: "Enter save", key: enterKey()},
				{label: "Esc cancel", key: escapeKey()},
			}); ok {
				s.handleRenameKey(key)
			}
		}
	case modeExitPrompt:
		return s.handleExitModalMouse(m, contentX, contentY)
	case modeNewSession:
		return s.handleNewSessionModalMouse(m, contentX, contentY, geometry.Content.Height)
	case modeFilePicker:
		s.handleFilePickerModalMouse(contentX, contentY, geometry.Content.Height)
	case modeSelecting:
		return s.handleSessionSelectorMouse(m, contentX, contentY, geometry.Content.Height)
	case modeConnections:
		return s.handleConnectionsModalMouse(m, contentX, contentY, geometry.Content.Height)
	case modeSettings:
		return s.handleSettingsModalMouse(contentX, contentY)
	case modeQuitConfirm:
		return s.handleQuitModalMouse(contentX, contentY)
	case modeDeleteConfirm:
		return s.handleDeleteModalMouse(contentX, contentY)
	}
	return nil
}

func (s *state) handleExitModalMouse(m Model, x, y int) tea.Cmd {
	if y == 5 {
		first := lipgloss.Width(exitChoiceActiveStyle.Render("[ Respawn ]"))
		second := lipgloss.Width(exitChoiceActiveStyle.Render("[ Remove ]"))
		switch {
		case x >= 0 && x < first:
			if s.exitChoice != 0 {
				s.handleExitPromptKey(m, rightKey())
			}
			return s.handleExitPromptKey(m, enterKey())
		case x >= first+3 && x < first+3+second:
			if s.exitChoice != 1 {
				s.handleExitPromptKey(m, rightKey())
			}
			return s.handleExitPromptKey(m, enterKey())
		}
	}
	if y == 8 {
		if key, ok := modalActionAt(x, "R respawn   X remove   Esc dismiss", []modalAction{
			{label: "R respawn", key: textKey('r')},
			{label: "X remove", key: textKey('x')},
			{label: "Esc dismiss", key: escapeKey()},
		}); ok {
			return s.handleExitPromptKey(m, key)
		}
	}
	return nil
}

func (s *state) handleNewSessionModalMouse(m Model, x, y, contentHeight int) tea.Cmd {
	if y == 2 {
		labels := []string{"[ Same as current/default ]", "[ Local command ]", "[ Remote SSH ]"}
		offset := 0
		for choice, label := range labels {
			width := lipgloss.Width(exitChoiceActiveStyle.Render(label))
			if x >= offset && x < offset+width {
				return s.handleNewSessionKey(m, runeKey(rune('1'+choice)))
			}
			offset += width + 3
		}
	}
	if y == contentHeight-1 {
		if key, ok := modalActionAt(x, "Enter start/browse   Esc cancel   ↑/↓ choose   Tab fields   1/2/3 choose", []modalAction{
			{label: "Enter start/browse", key: enterKey()},
			{label: "Esc cancel", key: escapeKey()},
		}); ok {
			return s.handleNewSessionKey(m, key)
		}
	}
	return nil
}

func (s *state) handleFilePickerModalMouse(x, y, contentHeight int) {
	start := 4
	rows := s.filePickerListRows()
	if y >= start && y < start+rows {
		index := s.filePicker.scroll + y - start
		if index >= 0 && index < len(s.filePicker.entries) {
			s.filePicker.cursor = index
			s.chooseFilePickerEntry()
		}
		return
	}
	if y != contentHeight-1 {
		return
	}
	footer := "Up/Down select   Enter open/select   Backspace parent   Esc cancel"
	if key, ok := modalActionAt(x, footer, []modalAction{
		{label: "Enter open/select", key: enterKey()},
		{label: "Backspace parent", key: tea.KeyPressMsg(tea.Key{Code: tea.KeyBackspace})},
		{label: "Esc cancel", key: escapeKey()},
	}); ok {
		s.handleFilePickerKey(key)
	}
}

func (s *state) handleSessionSelectorMouse(m Model, x, y, contentHeight int) tea.Cmd {
	listStart := s.sessionSelectorListStart()
	listRows := s.sessionSelectorListRows()
	matches := s.filteredSessions()
	if !s.selectRenaming && !s.selectFiltering && y >= listStart && y < listStart+listRows {
		cursor := s.selectScroll + y - listStart
		if cursor >= 0 && cursor < len(matches) {
			s.selectCursor = cursor
			s.mouseDrag = mouseDrag{kind: mouseDragSessionRow, session: matches[cursor]}
		}
		return nil
	}
	if y != contentHeight-1 {
		return nil
	}
	if s.selectRenaming {
		if key, ok := modalActionAt(x, "Rename: Enter save   Esc cancel   Backspace edits", []modalAction{
			{label: "Enter save", key: enterKey()},
			{label: "Esc cancel", key: escapeKey()},
		}); ok {
			return s.handleSelectKey(m, key)
		}
		return nil
	}
	if s.selectFiltering {
		if key, ok := modalActionAt(x, "Filter: type pattern   Enter apply   Esc actions   Ctrl+U clear", []modalAction{
			{label: "Enter apply", key: enterKey()},
			{label: "Esc actions", key: escapeKey()},
		}); ok {
			return s.handleSelectKey(m, key)
		}
		return nil
	}
	footer := "↑/↓ select   Enter focus   N new   R rename   M move   F filter   Del/X remove   Esc cancel"
	if s.selectMoving {
		footer = "Move: ↑/↓ reorder   M/Esc stop moving"
	}
	if matches := s.filteredSessions(); len(matches) > s.sessionSelectorListRows() {
		footer = fmt.Sprintf("%d/%d   %s", s.selectCursor+1, len(matches), footer)
	}
	if key, ok := modalActionAt(x, footer, []modalAction{
		{label: "Enter focus", key: enterKey()},
		{label: "N new", key: textKey('n')},
		{label: "R rename", key: textKey('r')},
		{label: "M move", key: textKey('m')},
		{label: "F filter", key: textKey('f')},
		{label: "Del/X remove", key: deleteKey()},
		{label: "Esc cancel", key: escapeKey()},
		{label: "M/Esc stop moving", key: textKey('m')},
	}); ok {
		return s.handleSelectKey(m, key)
	}
	return nil
}

func (s *state) handleConnectionsModalMouse(m Model, x, y, contentHeight int) tea.Cmd {
	listStart := s.connectionsListStart()
	matches := s.filteredConnections()
	listRows := max(1, len(matches))
	if !s.connRenaming && !s.connFiltering && y >= listStart && y < listStart+listRows {
		cursor := y - listStart
		if cursor >= 0 && cursor < len(matches) {
			s.connCursor = cursor
			s.mouseDrag = mouseDrag{
				kind:       mouseDragConnectionRow,
				connection: s.connections[matches[cursor]],
			}
		}
		return nil
	}
	if y != contentHeight-1 {
		return nil
	}
	if s.connRenaming {
		if key, ok := modalActionAt(x, "Rename: Enter save   Esc cancel   Backspace edits", []modalAction{
			{label: "Enter save", key: enterKey()},
			{label: "Esc cancel", key: escapeKey()},
		}); ok {
			return s.handleConnectionsKey(m, key)
		}
		return nil
	}
	if s.connFiltering {
		if key, ok := modalActionAt(x, "Filter: type pattern   Enter apply   Esc actions   Ctrl+U clear", []modalAction{
			{label: "Enter apply", key: enterKey()},
			{label: "Esc actions", key: escapeKey()},
		}); ok {
			return s.handleConnectionsKey(m, key)
		}
		return nil
	}
	footer := "↑/↓ select   Enter focus   N new   R rename   M move   L layout   F filter   Del/X remove   Esc cancel"
	if s.connMoving {
		footer = "Move: ↑/↓ reorder   M/Esc stop moving"
	}
	if key, ok := modalActionAt(x, footer, []modalAction{
		{label: "Enter focus", key: enterKey()},
		{label: "N new", key: textKey('n')},
		{label: "R rename", key: textKey('r')},
		{label: "M move", key: textKey('m')},
		{label: "L layout", key: textKey('l')},
		{label: "F filter", key: textKey('f')},
		{label: "Del/X remove", key: deleteKey()},
		{label: "Esc cancel", key: escapeKey()},
		{label: "M/Esc stop moving", key: textKey('m')},
	}); ok {
		return s.handleConnectionsKey(m, key)
	}
	return nil
}

func (s *state) handleQuitModalMouse(x, y int) tea.Cmd {
	if y != 5 {
		return nil
	}
	return s.handleBinaryConfirmMouse(x, s.exitChoice, func(key tea.KeyPressMsg) tea.Cmd {
		return s.handleQuitConfirmKey(key)
	})
}

func (s *state) handleDeleteModalMouse(x, y int) tea.Cmd {
	if y != 4 {
		return nil
	}
	return s.handleBinaryConfirmMouse(x, s.deleteChoice, func(key tea.KeyPressMsg) tea.Cmd {
		return s.handleDeleteConfirmKey(key)
	})
}

func (s *state) handleBinaryConfirmMouse(x, choice int, handler func(tea.KeyPressMsg) tea.Cmd) tea.Cmd {
	yesWidth := lipgloss.Width(exitChoiceActiveStyle.Render("[ Yes ]"))
	noWidth := lipgloss.Width(exitChoiceActiveStyle.Render("[ No ]"))
	desired := -1
	switch {
	case x >= 0 && x < yesWidth:
		desired = 0
	case x >= yesWidth+3 && x < yesWidth+3+noWidth:
		desired = 1
	}
	if desired < 0 {
		return nil
	}
	if desired != choice {
		handler(rightKey())
	}
	return handler(enterKey())
}

func (s *state) updateModalDrag(contentY int) {
	switch s.mouseDrag.kind {
	case mouseDragSessionRow:
		listStart := s.sessionSelectorListStart()
		if contentY < listStart || contentY >= listStart+s.sessionSelectorListRows() {
			return
		}
		matches := s.filteredSessions()
		cursor := s.selectScroll + contentY - listStart
		if cursor < 0 || cursor >= len(matches) {
			return
		}
		from := sessionIndex(s.manager, s.mouseDrag.session)
		to := sessionIndex(s.manager, matches[cursor])
		if from >= 0 && to >= 0 && from != to {
			s.moveSession(from, to)
			s.selectCursor = sessionCursor(s.filteredSessions(), s.mouseDrag.session)
			s.mouseDrag.moved = true
		}
	case mouseDragConnectionRow:
		listStart := s.connectionsListStart()
		matches := s.filteredConnections()
		cursor := contentY - listStart
		if cursor < 0 || cursor >= len(matches) {
			return
		}
		target := s.connections[matches[cursor]]
		from := connectionIndex(s.connections, s.mouseDrag.connection)
		to := connectionIndex(s.connections, target)
		if from >= 0 && to >= 0 && from != to {
			s.moveConnection(from, to)
			s.connCursor = connectionCursor(s, s.mouseDrag.connection)
			s.mouseDrag.moved = true
		}
	}
}

func (s *state) finishModalDrag(m Model, contentY int) tea.Cmd {
	drag := s.mouseDrag
	s.mouseDrag = mouseDrag{}
	if drag.moved {
		return nil
	}
	switch drag.kind {
	case mouseDragSessionRow:
		listStart := s.sessionSelectorListStart()
		cursor := s.selectScroll + contentY - listStart
		matches := s.filteredSessions()
		if contentY < listStart || contentY >= listStart+s.sessionSelectorListRows() ||
			cursor < 0 || cursor >= len(matches) || matches[cursor] != drag.session {
			return nil
		}
		s.selectCursor = sessionCursor(s.filteredSessions(), drag.session)
		return s.handleSelectKey(m, enterKey())
	case mouseDragConnectionRow:
		listStart := s.connectionsListStart()
		cursor := contentY - listStart
		matches := s.filteredConnections()
		if contentY < listStart || cursor < 0 || cursor >= len(matches) ||
			s.connections[matches[cursor]] != drag.connection {
			return nil
		}
		s.connCursor = connectionCursor(s, drag.connection)
		return s.handleConnectionsKey(m, enterKey())
	}
	return nil
}

func (s *state) sessionSelectorListStart() int {
	start := 2
	if strings.TrimSpace(s.selectFilter) != "" || s.selectFiltering {
		start++
	}
	if s.selectRenaming {
		start += 2
	}
	return start
}

func (s *state) sessionSelectorListRows() int {
	matches := s.filteredSessions()
	chrome := 4
	if strings.TrimSpace(s.selectFilter) != "" || s.selectFiltering {
		chrome++
	}
	if s.selectRenaming {
		chrome += 2
	}
	maxRows := s.geometry().Pane.Height*80/100 - chrome
	if maxRows < 3 {
		maxRows = 3
	}
	rows := max(1, len(matches))
	return min(rows, maxRows)
}

func (s *state) connectionsListStart() int {
	if strings.TrimSpace(s.connFilter) != "" || s.connFiltering {
		return 4
	}
	return 2
}

func sessionCursor(matches []*session.Session, target *session.Session) int {
	for cursor, sess := range matches {
		if sess == target {
			return cursor
		}
	}
	return 0
}

func connectionCursor(s *state, target *connectionState) int {
	for cursor, index := range s.filteredConnections() {
		if s.connections[index] == target {
			return cursor
		}
	}
	return 0
}

func modalActionAt(x int, row string, actions []modalAction) (tea.KeyPressMsg, bool) {
	for _, action := range actions {
		offset := strings.Index(row, action.label)
		if offset >= 0 && x >= lipgloss.Width(row[:offset]) &&
			x < lipgloss.Width(row[:offset])+lipgloss.Width(action.label) {
			return action.key, true
		}
	}
	return tea.KeyPressMsg{}, false
}

func enterKey() tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter})
}

func escapeKey() tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape})
}

func deleteKey() tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Code: tea.KeyDelete})
}

func rightKey() tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Code: tea.KeyRight})
}

func textKey(code rune) tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Code: code, Text: string(code)})
}

func runeKey(code rune) tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Code: code})
}
