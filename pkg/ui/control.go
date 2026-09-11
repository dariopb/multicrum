package ui

import (
	"context"
	"encoding/json"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"charm.land/bubbles/v2/viewport"
	"multicrum/pkg/control"
	"multicrum/pkg/session"
	"multicrum/pkg/ssh_client"
)

type controlSessionMeta struct {
	createdBy string
	parentID  string
	labels    map[string]string
	cleanup   string
}

type controlRequestMsg struct {
	method       string
	params       json.RawMessage
	controllerID string
	response     chan controlRequestResult
}

type controlRequestResult struct {
	result any
	err    *control.Error
}

// HandleControl serializes protocol operations through Bubble Tea's update
// loop, avoiding races with interactive TUI and WebSocket mutations.
func (m *Model) HandleControl(ctx context.Context, method string, params json.RawMessage, controllerID string) (any, *control.Error) {
	if m.s.program == nil {
		return nil, control.NewError("unavailable", "owner event loop is not running")
	}
	response := make(chan controlRequestResult, 1)
	m.s.program.Send(controlRequestMsg{
		method: method, params: params, controllerID: controllerID, response: response,
	})
	select {
	case result := <-response:
		return result.result, result.err
	case <-ctx.Done():
		return nil, control.NewError("timeout", ctx.Err().Error())
	}
}

func (s *state) handleControlRequest(m Model, method string, raw json.RawMessage, controllerID string) (any, *control.Error) {
	switch method {
	case "session.create", "session.focus", "session.move", "session.respawn", "session.close",
		"connection.create", "connection.remove":
		s.trace.Record("control.operation method=%s", method)
	}
	switch method {
	case "server.get":
		return map[string]any{
			"serverName": s.serverName, "capabilities": control.Capabilities,
			"limits": control.Limits{MaxFrameBytes: 1 << 20, MaxSessions: 32, ReplayBytesPerSession: 256 * 1024},
		}, nil
	case "connection.list":
		return map[string]any{"connections": s.controlConnectionList()}, nil
	case "connection.create":
		var p struct {
			Name   string            `json:"name"`
			Labels map[string]string `json:"labels"`
		}
		if err := decodeControl(raw, &p); err != nil {
			return nil, err
		}
		p.Name = strings.TrimSpace(p.Name)
		if p.Name == "" {
			return nil, control.NewError("invalid_argument", "name is required")
		}
		if s.connectionByName(p.Name) != nil {
			return nil, control.NewError("already_exists", "connection name already exists")
		}
		conn := s.addConnection(p.Name)
		conn.labels = cloneLabels(p.Labels)
		conn.createdBy = controllerID
		geom := s.geometry()
		conn.manager = session.NewManagerWithSSH(geom.Pane.Width, geom.Pane.Height, nil, nil, nil)
		conn.manager.SetAgentStateEndpoint(s.agentNativeEndpoint)
		s.bindConnectionCallbacks(conn)
		s.notifyMeta()
		s.publishControlEvent("session.lifecycle", "connection.created", "", 0, map[string]any{
			"connectionId": conn.id, "name": conn.name,
		})
		return map[string]any{"connection": s.controlConnection(conn)}, nil
	case "connection.rename":
		var p struct {
			ConnectionID string `json:"connectionId"`
			Name         string `json:"name"`
		}
		if err := decodeControl(raw, &p); err != nil {
			return nil, err
		}
		conn, _ := s.connectionByID(p.ConnectionID)
		if conn == nil {
			return nil, control.NewError("not_found", "connection not found")
		}
		p.Name = strings.TrimSpace(p.Name)
		if p.Name == "" {
			return nil, control.NewError("invalid_argument", "name is required")
		}
		if other := s.connectionByName(p.Name); other != nil && other != conn {
			return nil, control.NewError("already_exists", "connection name already exists")
		}
		conn.name = p.Name
		s.notifyMeta()
		s.publishControlEvent("session.metadata", "connection.renamed", "", 0, map[string]any{
			"connectionId": conn.id, "name": conn.name,
		})
		return map[string]any{"connection": s.controlConnection(conn)}, nil
	case "connection.remove":
		return s.controlRemoveConnection(raw)
	case "session.list":
		return s.controlListSessions(raw), nil
	case "session.get":
		sess, conn, _, err := s.controlSessionFromParams(raw, false)
		if err != nil {
			return nil, err
		}
		return map[string]any{"session": s.controlSession(conn, sess)}, nil
	case "session.create":
		return s.controlCreateSession(raw, controllerID)
	case "session.focus":
		sess, conn, _, err := s.controlSessionFromParams(raw, false)
		if err != nil {
			return nil, err
		}
		connIndex := connectionIndex(s.connections, conn)
		if connIndex < 0 {
			return nil, control.NewError("not_found", "session connection not found")
		}
		conn.manager.Focus(sess.Index())
		s.focusConnection(connIndex)
		return map[string]any{"session": s.controlSession(conn, sess)}, nil
	case "session.rename":
		sess, conn, p, err := s.controlSessionFromParams(raw, true)
		if err != nil {
			return nil, err
		}
		var name struct {
			Title string `json:"title"`
		}
		_ = json.Unmarshal(raw, &name)
		sess.SetTitle(strings.TrimSpace(name.Title))
		s.notifyMeta()
		s.publishSessionEvent("session.metadata", "session.renamed", sess, map[string]any{"title": sess.Title()})
		return map[string]any{"session": s.controlSession(conn, sess), "generation": p.Generation}, nil
	case "session.move":
		return s.controlMoveSession(raw)
	case "session.resize":
		sess, conn, _, err := s.controlSessionFromParams(raw, true)
		if err != nil {
			return nil, err
		}
		var p struct {
			Cols int `json:"cols"`
			Rows int `json:"rows"`
		}
		_ = json.Unmarshal(raw, &p)
		if p.Cols < 1 || p.Rows < 1 {
			return nil, control.NewError("invalid_argument", "cols and rows must be positive")
		}
		sessionID, generation, _, _ := sess.RuntimeSnapshot()
		s.trace.Record("resize.request source=control connection=%s session=%s generation=%d new=%dx%d",
			conn.id, sessionID, generation, p.Cols, p.Rows)
		conn.manager.ResizeOne(sess.Index(), p.Cols, p.Rows)
		s.publishSessionEvent("session.metadata", "session.resized", sess, map[string]any{"cols": p.Cols, "rows": p.Rows})
		return map[string]any{"session": s.controlSession(conn, sess)}, nil
	case "session.respawn":
		sess, conn, _, err := s.controlSessionFromParams(raw, true)
		if err != nil {
			return nil, err
		}
		cols, rows := sess.Screen().Dimensions()
		if err := conn.manager.Respawn(sess.Index()); err != nil {
			return nil, control.NewError("unavailable", err.Error())
		}
		conn.manager.ResizeOne(sess.Index(), cols, rows)
		s.resetConnectionViewport(conn, sess.Index())
		s.notifyMeta()
		s.publishSessionEvent("session.lifecycle", "session.respawned", sess, nil)
		return map[string]any{"session": s.controlSession(conn, sess)}, nil
	case "session.close":
		return s.controlCloseSession(raw)
	case "session.sendText":
		return s.controlSendText(raw)
	case "session.paste":
		return s.controlPaste(raw)
	case "session.sendBytes":
		return s.controlSendBytes(raw)
	case "session.snapshot":
		return s.controlSnapshot(raw)
	case "session.replay":
		return s.controlReplay(raw)
	case "session.awaitCheck":
		return s.controlAwaitCheck(raw)
	case "capability.inject":
		return s.controlInject(raw)
	default:
		return nil, control.NewError("not_found", "unknown method")
	}
}

func decodeControl(raw json.RawMessage, target any) *control.Error {
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return control.NewError("invalid_argument", "invalid params: "+err.Error())
	}
	return nil
}

func (s *state) connectionByName(name string) *connectionState {
	for _, conn := range s.connections {
		if conn.name == name {
			return conn
		}
	}
	return nil
}

func (s *state) connectionByID(id string) (*connectionState, int) {
	for i, conn := range s.connections {
		if conn.id == id {
			return conn, i
		}
	}
	return nil, -1
}

func (s *state) controlConnection(conn *connectionState) map[string]any {
	count := 0
	if conn.manager != nil {
		count = conn.manager.Len()
	}
	return map[string]any{
		"connectionId": conn.id, "name": conn.name, "sessionCount": count,
		"createdBy": conn.createdBy, "labels": cloneLabels(conn.labels),
	}
}

func (s *state) controlConnectionList() []map[string]any {
	out := make([]map[string]any, 0, len(s.connections))
	for _, conn := range s.connections {
		out = append(out, s.controlConnection(conn))
	}
	return out
}

func cloneLabels(labels map[string]string) map[string]string {
	out := make(map[string]string, len(labels))
	for key, value := range labels {
		out[key] = value
	}
	return out
}

type sessionRefParams struct {
	SessionID  string `json:"sessionId"`
	Generation uint64 `json:"generation"`
}

func (s *state) controlSessionFromParams(raw json.RawMessage, checkGeneration bool) (*session.Session, *connectionState, sessionRefParams, *control.Error) {
	var p sessionRefParams
	if err := decodeControl(raw, &p); err != nil {
		return nil, nil, p, err
	}
	for _, conn := range s.connections {
		if conn.manager == nil {
			continue
		}
		for _, sess := range conn.manager.Sessions() {
			id, generation, _, _ := sess.RuntimeSnapshot()
			if id != p.SessionID {
				continue
			}
			if checkGeneration && generation != p.Generation {
				return nil, nil, p, control.NewError("not_found", "session generation not found")
			}
			return sess, conn, p, nil
		}
	}
	return nil, nil, p, control.NewError("not_found", "session not found")
}

func (s *state) controlSession(conn *connectionState, sess *session.Session) map[string]any {
	id, generation, pid, local := sess.RuntimeSnapshot()
	cols, rows := sess.Screen().Dimensions()
	meta := s.controlSessions[id]
	backend := "local"
	if !local {
		backend = "ssh"
	}
	state := "running"
	if sess.Exited() {
		state = "exited"
	}
	result := map[string]any{
		"sessionId": id, "generation": generation, "connectionId": conn.id,
		"index": sess.Index(), "title": sess.Title(), "state": state, "pid": pid,
		"backend": backend, "createdBy": meta.createdBy, "parentSessionId": meta.parentID,
		"labels": cloneLabels(meta.labels), "cwd": sess.ConfiguredWorkingDirectory(),
		"cmd": sess.Cmd(), "cols": cols, "rows": rows, "outputSequence": sess.OutputSequence(),
	}
	if status, ok := s.agentStatus(sess); ok {
		result["agent"] = map[string]any{
			"provider": status.Provider, "state": status.State,
			"source": status.Source, "confidence": status.Confidence,
		}
	}
	return result
}

func (s *state) controlListSessions(raw json.RawMessage) map[string]any {
	var filter struct {
		ConnectionID string            `json:"connectionId"`
		CreatedBy    string            `json:"createdBy"`
		Labels       map[string]string `json:"labels"`
	}
	_ = json.Unmarshal(raw, &filter)
	var out []map[string]any
	for _, conn := range s.connections {
		if conn.manager == nil || (filter.ConnectionID != "" && filter.ConnectionID != conn.id) {
			continue
		}
		for _, sess := range conn.manager.Sessions() {
			id, _, _, _ := sess.RuntimeSnapshot()
			meta := s.controlSessions[id]
			if filter.CreatedBy != "" && meta.createdBy != filter.CreatedBy {
				continue
			}
			if !labelsMatch(meta.labels, filter.Labels) {
				continue
			}
			out = append(out, s.controlSession(conn, sess))
		}
	}
	return map[string]any{"sessions": out}
}

func labelsMatch(actual, wanted map[string]string) bool {
	for key, value := range wanted {
		if actual[key] != value {
			return false
		}
	}
	return true
}

type createSessionParams struct {
	ConnectionID string            `json:"connectionId"`
	Title        string            `json:"title"`
	Focus        bool              `json:"focus"`
	Cmd          []string          `json:"cmd"`
	Cwd          string            `json:"cwd"`
	Env          map[string]string `json:"env"`
	Labels       map[string]string `json:"labels"`
	Backend      struct {
		Kind          string `json:"kind"`
		Target        string `json:"target"`
		Port          string `json:"port"`
		KeyRef        string `json:"keyRef"`
		VerifyHostKey bool   `json:"verifyHostKey"`
	} `json:"backend"`
	Terminal struct {
		Cols int `json:"cols"`
		Rows int `json:"rows"`
	} `json:"terminal"`
	Ownership struct {
		Cleanup         string `json:"cleanup"`
		ParentSessionID string `json:"parentSessionId"`
	} `json:"ownership"`
}

func (s *state) controlCreateSession(raw json.RawMessage, controllerID string) (any, *control.Error) {
	var p createSessionParams
	if err := decodeControl(raw, &p); err != nil {
		return nil, err
	}
	conn, _ := s.connectionByID(p.ConnectionID)
	if conn == nil || conn.manager == nil {
		return nil, control.NewError("not_found", "connection not found")
	}
	if len(p.Cmd) == 0 || strings.TrimSpace(p.Cmd[0]) == "" {
		return nil, control.NewError("invalid_argument", "cmd must contain at least one argument")
	}
	if p.Cwd != "" && !filepath.IsAbs(p.Cwd) {
		return nil, control.NewError("invalid_argument", "cwd must be absolute")
	}
	env := make([]string, 0, len(p.Env))
	keys := make([]string, 0, len(p.Env))
	for key := range p.Env {
		if strings.HasPrefix(key, "MULTICRUM_CONTROL_") || strings.HasPrefix(key, "HERDR_") {
			return nil, control.NewError("permission_denied", "reserved environment variable")
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		env = append(env, key+"="+p.Env[key])
	}
	var sshClient *ssh_client.Client
	if p.Backend.Kind == "ssh" {
		client, err := ssh_client.New(ssh_client.Options{
			Target: p.Backend.Target, Port: p.Backend.Port,
			IdentityFile:          keyRefPath(p.Backend.KeyRef),
			UseDefaultKeys:        p.Backend.KeyRef == "" || p.Backend.KeyRef == "default",
			UseAgent:              p.Backend.KeyRef == "" || p.Backend.KeyRef == "default",
			InsecureIgnoreHostKey: !p.Backend.VerifyHostKey, Command: p.Cmd,
		})
		if err != nil {
			return nil, control.NewError("invalid_argument", err.Error())
		}
		sshClient = client
	} else if p.Backend.Kind != "" && p.Backend.Kind != "local" {
		return nil, control.NewError("invalid_argument", "backend kind must be local or ssh")
	}
	sess, err := conn.manager.NewConfigured(p.Cmd, sshClient, p.Cwd, env)
	if err != nil {
		return nil, control.NewError("unavailable", err.Error())
	}
	if p.Title != "" {
		sess.SetTitle(strings.TrimSpace(p.Title))
	}
	if p.Terminal.Cols > 0 && p.Terminal.Rows > 0 {
		conn.manager.ResizeOne(sess.Index(), p.Terminal.Cols, p.Terminal.Rows)
	}
	id, _, _, _ := sess.RuntimeSnapshot()
	cleanup := p.Ownership.Cleanup
	if cleanup == "" {
		cleanup = "retain"
	}
	s.controlSessions[id] = controlSessionMeta{
		createdBy: controllerID, parentID: p.Ownership.ParentSessionID,
		labels: cloneLabels(p.Labels), cleanup: cleanup,
	}
	if p.Focus {
		for i, candidate := range s.connections {
			if candidate == conn {
				s.activeConn = i
				break
			}
		}
		s.syncActiveConnectionFields()
		conn.manager.Focus(sess.Index())
		s.clearSelection()
		s.refreshFocused()
	}
	s.syncAgentSessions()
	s.notifyMeta()
	s.publishSessionEvent("session.lifecycle", "session.created", sess, map[string]any{"connectionId": conn.id})
	s.publishSessionEvent("session.lifecycle", "session.started", sess, nil)
	return map[string]any{"session": s.controlSession(conn, sess)}, nil
}

func keyRefPath(ref string) string {
	if ref == "" || ref == "default" {
		return ""
	}
	return ref
}

func (s *state) controlSendText(raw json.RawMessage) (any, *control.Error) {
	sess, _, _, err := s.controlSessionFromParams(raw, true)
	if err != nil {
		return nil, err
	}
	var p struct {
		Text   string `json:"text"`
		Submit bool   `json:"submit"`
	}
	_ = json.Unmarshal(raw, &p)
	data := []byte(p.Text)
	if p.Submit {
		data = append(data, '\r')
	}
	n, writeErr := sess.Write(data)
	if writeErr != nil {
		return nil, control.NewError("failed_precondition", writeErr.Error())
	}
	return map[string]any{"accepted": n}, nil
}

func (s *state) controlPaste(raw json.RawMessage) (any, *control.Error) {
	sess, _, _, err := s.controlSessionFromParams(raw, true)
	if err != nil {
		return nil, err
	}
	var p struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal(raw, &p)
	data := []byte(p.Text)
	if sess.Screen().BracketedPasteMode() {
		data = append(append([]byte("\x1b[200~"), data...), []byte("\x1b[201~")...)
	}
	n, writeErr := sess.Write(data)
	if writeErr != nil {
		return nil, control.NewError("failed_precondition", writeErr.Error())
	}
	return map[string]any{"accepted": n}, nil
}

func (s *state) controlSendBytes(raw json.RawMessage) (any, *control.Error) {
	sess, _, _, err := s.controlSessionFromParams(raw, true)
	if err != nil {
		return nil, err
	}
	var p struct {
		Data []byte `json:"data"`
	}
	_ = json.Unmarshal(raw, &p)
	n, writeErr := sess.Write(p.Data)
	if writeErr != nil {
		return nil, control.NewError("failed_precondition", writeErr.Error())
	}
	return map[string]any{"accepted": n}, nil
}

func (s *state) controlSnapshot(raw json.RawMessage) (any, *control.Error) {
	sess, _, _, err := s.controlSessionFromParams(raw, true)
	if err != nil {
		return nil, err
	}
	var p struct {
		Format string `json:"format"`
	}
	_ = json.Unmarshal(raw, &p)
	cols, rows := sess.Screen().Dimensions()
	result := map[string]any{
		"cols": cols, "rows": rows, "outputSequence": sess.OutputSequence(),
		"cursor": sess.Screen().Cursor(), "bounded": true,
	}
	switch p.Format {
	case "", "raw-replay":
		result["format"] = "raw-replay"
		result["data"] = sess.Screen().RawSnapshot()
	case "ansi-screen":
		result["format"] = p.Format
		result["text"] = sess.Screen().Render()
		result["bounded"] = false
	case "plain-screen":
		result["format"] = p.Format
		result["lines"] = plainLines(sess.Screen().VisibleLines())
		result["bounded"] = false
	case "plain-buffer":
		result["format"] = p.Format
		result["lines"] = plainLines(sess.Screen().BufferLines())
	default:
		return nil, control.NewError("invalid_argument", "unknown snapshot format")
	}
	return result, nil
}

func plainLines(lines []session.BufferLine) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = line.Text
	}
	return out
}

func (s *state) controlReplay(raw json.RawMessage) (any, *control.Error) {
	sess, _, p, err := s.controlSessionFromParams(raw, true)
	if err != nil {
		return nil, err
	}
	var from struct {
		Sequence uint64 `json:"sequence"`
	}
	_ = json.Unmarshal(raw, &from)
	data := sess.Screen().RawSnapshot()
	next := sess.OutputSequence()
	available := next - uint64(len(data))
	if from.Sequence < available {
		return nil, &control.Error{
			Code: "output_gap", Message: "requested output is no longer retained",
			Details: map[string]any{"requestedSequence": from.Sequence, "availableSequence": available},
		}
	}
	if from.Sequence > next {
		return nil, control.NewError("invalid_argument", "sequence exceeds current output")
	}
	offset := from.Sequence - available
	return map[string]any{
		"data": append([]byte(nil), data[offset:]...), "sequence": from.Sequence,
		"generation": p.Generation,
	}, nil
}

func (s *state) controlCloseSession(raw json.RawMessage) (any, *control.Error) {
	sess, conn, _, err := s.controlSessionFromParams(raw, true)
	if err != nil {
		return nil, err
	}
	var p struct {
		Mode string `json:"mode"`
	}
	_ = json.Unmarshal(raw, &p)
	id, generation, _, _ := sess.RuntimeSnapshot()
	if p.Mode == "" || p.Mode == "terminate" || p.Mode == "remove" {
		s.suppressExit(id, generation)
	}
	switch p.Mode {
	case "interrupt":
		if _, writeErr := sess.Write([]byte{3}); writeErr != nil {
			return nil, control.NewError("failed_precondition", writeErr.Error())
		}
	case "", "terminate":
		if closeErr := sess.Close(); closeErr != nil {
			return nil, control.NewError("internal", closeErr.Error())
		}
		s.notifyMeta()
		s.publishControlEvent("session.lifecycle", "session.exited", id, generation, map[string]any{"controlled": true})
	case "remove":
		index := sess.Index()
		if !conn.manager.Remove(index) {
			return nil, control.NewError("not_found", "session not found")
		}
		delete(s.controlSessions, id)
		s.clearConnectionViewports(conn)
		if conn == s.activeConnection() && conn.manager.Len() > 0 {
			s.refreshFocused()
		}
		s.notifyMeta()
		s.publishControlEvent("session.lifecycle", "session.removed", id, generation, nil)
	default:
		return nil, control.NewError("invalid_argument", "mode must be interrupt, terminate, or remove")
	}
	return map[string]any{"closed": true}, nil
}

func (s *state) controlMoveSession(raw json.RawMessage) (any, *control.Error) {
	sess, source, _, err := s.controlSessionFromParams(raw, true)
	if err != nil {
		return nil, err
	}
	var p struct {
		ConnectionID string `json:"connectionId"`
		Position     int    `json:"position"`
	}
	_ = json.Unmarshal(raw, &p)
	target, _ := s.connectionByID(p.ConnectionID)
	if target == nil || target.manager == nil {
		return nil, control.NewError("not_found", "target connection not found")
	}
	if source == target {
		source.manager.Move(sess.Index(), p.Position)
	} else {
		detached := source.manager.Detach(sess.Index())
		target.manager.Adopt(detached, p.Position)
	}
	s.clearConnectionViewports(source)
	if target != source {
		s.clearConnectionViewports(target)
	}
	s.notifyMeta()
	s.publishSessionEvent("session.metadata", "session.moved", sess, map[string]any{"connectionId": target.id, "position": sess.Index()})
	return map[string]any{"session": s.controlSession(target, sess)}, nil
}

func (s *state) controlRemoveConnection(raw json.RawMessage) (any, *control.Error) {
	var p struct {
		ConnectionID       string `json:"connectionId"`
		SessionPolicy      string `json:"sessionPolicy"`
		TargetConnectionID string `json:"targetConnectionId"`
	}
	if err := decodeControl(raw, &p); err != nil {
		return nil, err
	}
	conn, index := s.connectionByID(p.ConnectionID)
	if conn == nil {
		return nil, control.NewError("not_found", "connection not found")
	}
	if len(s.connections) <= 1 {
		return nil, control.NewError("failed_precondition", "cannot remove the final connection")
	}
	count := conn.manager.Len()
	switch p.SessionPolicy {
	case "", "reject":
		if count > 0 {
			return nil, control.NewError("failed_precondition", "connection contains sessions")
		}
	case "terminate":
		for _, sess := range conn.manager.Sessions() {
			id, generation, _, _ := sess.RuntimeSnapshot()
			s.suppressExit(id, generation)
			delete(s.controlSessions, id)
		}
	case "move":
		target, _ := s.connectionByID(p.TargetConnectionID)
		if target == nil || target == conn {
			return nil, control.NewError("invalid_argument", "valid targetConnectionId is required")
		}
		for conn.manager.Len() > 0 {
			target.manager.Adopt(conn.manager.Detach(0), -1)
		}
	default:
		return nil, control.NewError("invalid_argument", "sessionPolicy must be reject, terminate, or move")
	}
	id := conn.id
	s.removeConnection(index)
	s.publishControlEvent("session.lifecycle", "connection.removed", "", 0, map[string]any{"connectionId": id})
	return map[string]any{"removed": true}, nil
}

func (s *state) controlInject(raw json.RawMessage) (any, *control.Error) {
	sess, conn, _, err := s.controlSessionFromParams(raw, false)
	if err != nil {
		return nil, err
	}
	var p struct {
		Env map[string]string `json:"env"`
	}
	_ = json.Unmarshal(raw, &p)
	keys := make([]string, 0, len(p.Env))
	for key := range p.Env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	env := make([]string, 0, len(keys))
	for _, key := range keys {
		env = append(env, key+"="+p.Env[key])
	}
	sess.SetEnvironment(env)
	cols, rows := sess.Screen().Dimensions()
	if err := conn.manager.Respawn(sess.Index()); err != nil {
		return nil, control.NewError("unavailable", err.Error())
	}
	conn.manager.ResizeOne(sess.Index(), cols, rows)
	s.notifyMeta()
	s.publishSessionEvent("session.lifecycle", "session.respawned", sess, nil)
	return map[string]any{"injected": true, "session": s.controlSession(conn, sess)}, nil
}

func (s *state) controlAwaitCheck(raw json.RawMessage) (any, *control.Error) {
	sess, _, _, err := s.controlSessionFromParams(raw, true)
	if err != nil {
		return nil, err
	}
	var p struct {
		Condition json.RawMessage `json:"condition"`
	}
	_ = json.Unmarshal(raw, &p)
	matched, reason, state := s.matchCondition(sess, p.Condition)
	result := map[string]any{
		"matched": matched, "reason": reason, "outputSequence": sess.OutputSequence(),
	}
	if state != "" {
		result["agentState"] = state
	}
	return result, nil
}

func (s *state) matchCondition(sess *session.Session, raw json.RawMessage) (bool, string, string) {
	var condition struct {
		AgentState      []string          `json:"agentState"`
		Exit            bool              `json:"exit"`
		OutputRegex     string            `json:"outputRegex"`
		ScreenRegex     string            `json:"screenRegex"`
		SequenceAtLeast *uint64           `json:"sequenceAtLeast"`
		Any             []json.RawMessage `json:"any"`
		All             []json.RawMessage `json:"all"`
	}
	if json.Unmarshal(raw, &condition) != nil {
		return false, "", ""
	}
	for _, child := range condition.Any {
		if matched, reason, state := s.matchCondition(sess, child); matched {
			return true, reason, state
		}
	}
	if len(condition.All) > 0 {
		reason := "all"
		for _, child := range condition.All {
			matched, _, _ := s.matchCondition(sess, child)
			if !matched {
				return false, "", ""
			}
		}
		return true, reason, ""
	}
	if condition.Exit && sess.Exited() {
		return true, "exit", ""
	}
	if len(condition.AgentState) > 0 {
		if status, ok := s.agentStatus(sess); ok {
			for _, wanted := range condition.AgentState {
				if string(status.State) == wanted {
					return true, "agentState", wanted
				}
			}
		}
	}
	if condition.SequenceAtLeast != nil && sess.OutputSequence() >= *condition.SequenceAtLeast {
		return true, "sequenceAtLeast", ""
	}
	if condition.OutputRegex != "" {
		if re, err := regexp.Compile(condition.OutputRegex); err == nil && re.Match(sess.Screen().RawSnapshot()) {
			return true, "outputRegex", ""
		}
	}
	if condition.ScreenRegex != "" {
		if re, err := regexp.Compile(condition.ScreenRegex); err == nil &&
			re.MatchString(strings.Join(plainLines(sess.Screen().VisibleLines()), "\n")) {
			return true, "screenRegex", ""
		}
	}
	return false, "", ""
}

func (s *state) publishSessionEvent(stream, name string, sess *session.Session, data map[string]any) {
	id, generation, _, _ := sess.RuntimeSnapshot()
	s.publishControlEvent(stream, name, id, generation, data)
}

func (s *state) publishControlEvent(stream, name, sessionID string, generation uint64, data map[string]any) {
	if s.controlService == nil {
		return
	}
	s.controlService.PublishEvent(stream, control.Event{
		Event: name, SessionID: sessionID, Generation: generation, Data: data,
	})
}

func (s *state) resetConnectionViewport(conn *connectionState, index int) {
	delete(conn.viewports, index)
	delete(conn.scrollbackCache, index)
	delete(conn.liveLines, index)
}

func (s *state) clearConnectionViewports(conn *connectionState) {
	conn.viewports = make(map[int]*viewport.Model)
	conn.scrollbackCache = make(map[int]scrollWrapCache)
	conn.liveLines = make(map[int][]session.BufferLine)
	conn.altScreens = make(map[int]bool)
	conn.scrollbackMode = make(map[int]bool)
	if conn == s.activeConnection() {
		s.syncActiveConnectionFields()
	}
}
