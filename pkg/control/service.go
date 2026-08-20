package control

import (
	"context"
	"crypto/subtle"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"multicrum/pkg/localserver"
)

type Service struct {
	serverName string
	serverID   string
	endpoint   string
	token      string
	backend    Backend
	ln         net.Listener

	mu       sync.RWMutex
	clients  map[*controlClient]struct{}
	grants   map[string]grant
	outputAt map[string]time.Time
	idem     map[string]idempotentResult
	closed   bool
}

type idempotentResult struct {
	method  string
	result  json.RawMessage
	err     *Error
	expires time.Time
}

type grant struct {
	controllerID        string
	expires             time.Time
	permissions         map[string]bool
	scope               Scope
	subjectSessionID    string
	subjectConnectionID string
}

type Scope struct {
	CreatedBySubject bool     `json:"createdBySubject"`
	MaxConnections   int      `json:"maxConnections"`
	MaxSessions      int      `json:"maxSessions"`
	AllowedCommands  []string `json:"allowedCommands"`
	AllowedCwdRoots  []string `json:"allowedCwdRoots"`
	AllowSSH         bool     `json:"allowSSH"`
}

type controlClient struct {
	conn          net.Conn
	controllerID  string
	grant         grant
	send          chan outbound
	done          chan struct{}
	subMu         sync.RWMutex
	subscriptions map[string]subscription
}

type subscription struct {
	id       string
	streams  map[string]bool
	sessions map[string]bool
}

type outbound struct {
	typ  byte
	body []byte
}

func NewService(serverName, endpoint, token string, backend Backend) *Service {
	return &Service{
		serverName: serverName, serverID: NewID("srv"), endpoint: endpoint,
		token: token, backend: backend, clients: make(map[*controlClient]struct{}),
		grants: make(map[string]grant), outputAt: make(map[string]time.Time),
		idem: make(map[string]idempotentResult),
	}
}

func (s *Service) Endpoint() string { return s.endpoint }

func (s *Service) Start() error {
	if err := os.Remove(s.endpoint); err != nil && !os.IsNotExist(err) {
		return err
	}
	ln, err := listenEndpoint(s.endpoint)
	if err != nil {
		return err
	}
	if strings.HasSuffix(s.endpoint, ".sock") {
		if err := os.Chmod(s.endpoint, 0o600); err != nil {
			ln.Close()
			return err
		}
	}
	s.ln = ln
	go s.acceptLoop()
	return nil
}

func (s *Service) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	clients := make([]*controlClient, 0, len(s.clients))
	for client := range s.clients {
		clients = append(clients, client)
	}
	s.mu.Unlock()
	for _, client := range clients {
		client.conn.Close()
	}
	var err error
	if s.ln != nil {
		err = s.ln.Close()
	}
	_ = os.Remove(s.endpoint)
	return err
}

func (s *Service) acceptLoop() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.serve(conn)
	}
}

func (s *Service) serve(conn net.Conn) {
	typ, body, err := localserver.ReadFrame(conn)
	if err != nil || typ != FrameHello {
		conn.Close()
		return
	}
	var hello Hello
	if json.Unmarshal(body, &hello) != nil || hello.Protocol != Protocol ||
		hello.Version != Version || hello.Server != s.serverName {
		s.writeHandshakeError(conn, "incompatible control handshake")
		conn.Close()
		return
	}
	controllerID := hello.ResumeControllerID
	if controllerID == "" {
		controllerID = NewID("ctl")
	}
	g, ok := s.authenticate(hello.Token, controllerID)
	if !ok {
		s.writeHandshakeError(conn, "authentication failed")
		conn.Close()
		return
	}
	client := &controlClient{
		conn: conn, controllerID: g.controllerID, grant: g,
		send: make(chan outbound, 256), done: make(chan struct{}),
		subscriptions: make(map[string]subscription),
	}
	s.mu.Lock()
	s.clients[client] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.clients, client)
		s.mu.Unlock()
		conn.Close()
		close(client.done)
	}()

	welcome, _ := json.Marshal(Welcome{
		Protocol: Protocol, Version: Version, ServerID: s.serverID,
		ServerName: s.serverName, ControllerID: client.controllerID, OwnerPID: os.Getpid(),
		Capabilities: Capabilities,
		Limits:       Limits{MaxFrameBytes: localserver.MaxFrameSize, MaxSessions: 32, ReplayBytesPerSession: 256 * 1024},
	})
	if err := localserver.WriteFrame(conn, FrameWelcome, welcome); err != nil {
		return
	}
	go client.writeLoop()
	for {
		typ, body, err := localserver.ReadFrame(conn)
		if err != nil {
			return
		}
		switch typ {
		case FrameRequest:
			var request Request
			if err := json.Unmarshal(body, &request); err != nil {
				client.respond(Response{OK: false, Error: NewError("invalid_argument", "invalid request JSON")})
				continue
			}
			go s.handleRequest(client, request)
		case FrameInput:
			go s.handleBinaryInput(client, body)
		case FrameAck:
			// Acknowledgements are advisory in v1; replay retention is global.
		default:
			return
		}
	}
}

func (s *Service) authenticate(token, controllerID string) (grant, bool) {
	if secureEqual(token, s.token) {
		return grant{controllerID: controllerID}, true
	}
	s.mu.RLock()
	g, ok := s.grants[token]
	s.mu.RUnlock()
	if !ok || time.Now().After(g.expires) {
		return grant{}, false
	}
	return g, true
}

func secureEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func (s *Service) writeHandshakeError(conn net.Conn, message string) {
	body, _ := json.Marshal(Response{OK: false, Error: NewError("permission_denied", message)})
	_ = localserver.WriteFrame(conn, FrameResponse, body)
}

func (c *controlClient) writeLoop() {
	for {
		select {
		case message := <-c.send:
			if err := localserver.WriteFrame(c.conn, message.typ, message.body); err != nil {
				c.conn.Close()
				return
			}
		case <-c.done:
			return
		}
	}
}

func (c *controlClient) enqueue(typ byte, body []byte) bool {
	select {
	case c.send <- outbound{typ: typ, body: body}:
		return true
	default:
		return false
	}
}

func (c *controlClient) respond(response Response) {
	body, _ := json.Marshal(response)
	if !c.enqueue(FrameResponse, body) {
		c.conn.Close()
	}
}

func (s *Service) handleRequest(client *controlClient, request Request) {
	if request.ID == "" || request.Method == "" {
		client.respond(Response{ID: request.ID, OK: false, Error: NewError("invalid_argument", "id and method are required")})
		return
	}
	request = normalizeSessionCloseAlias(request)
	if !s.allowed(client, request.Method, request.Params) {
		client.respond(Response{ID: request.ID, OK: false, Error: NewError("permission_denied", "capability does not allow this operation")})
		return
	}

	idempotencyKey := requestIdempotencyKey(request.Params)
	if idempotencyKey != "" && isMutating(request.Method) {
		cacheKey := client.controllerID + "\x00" + idempotencyKey
		s.mu.RLock()
		cached, ok := s.idem[cacheKey]
		s.mu.RUnlock()
		if ok && time.Now().Before(cached.expires) {
			if cached.method != request.Method {
				client.respond(Response{ID: request.ID, OK: false, Error: NewError("already_exists", "idempotency key was used for another method")})
				return
			}
			client.respond(Response{ID: request.ID, OK: cached.err == nil, Result: cached.result, Error: cached.err})
			return
		}
	}
	var result any
	var controlErr *Error
	switch request.Method {
	case "subscribe":
		result, controlErr = s.subscribe(client, request.Params)
	case "session.await":
		result, controlErr = s.await(client, request.Params)
	case "capability.delegate":
		result, controlErr = s.delegate(client, request.Params)
	default:
		result, controlErr = s.backend.HandleControl(context.Background(), request.Method, request.Params, client.controllerID)
	}
	if controlErr != nil {
		s.cacheIdempotent(client.controllerID, request.Method, idempotencyKey, nil, controlErr)
		client.respond(Response{ID: request.ID, OK: false, Error: controlErr})
		return
	}
	if client.grant.permissions != nil && client.grant.scope.CreatedBySubject {
		result = filterCreatedResult(request.Method, result, client.grant)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		client.respond(Response{ID: request.ID, OK: false, Error: NewError("internal", "cannot encode response")})
		return
	}
	s.cacheIdempotent(client.controllerID, request.Method, idempotencyKey, raw, nil)
	client.respond(Response{ID: request.ID, OK: true, Result: raw})
}

func normalizeSessionCloseAlias(request Request) Request {
	mode := ""
	switch request.Method {
	case "session.remove":
		mode = "remove"
	case "session.terminate":
		mode = "terminate"
	default:
		return request
	}
	var params map[string]any
	if json.Unmarshal(request.Params, &params) != nil {
		return request
	}
	params["mode"] = mode
	request.Method = "session.close"
	request.Params = mustJSON(params)
	return request
}

func requestIdempotencyKey(raw json.RawMessage) string {
	var params struct {
		IdempotencyKey string `json:"idempotencyKey"`
	}
	_ = json.Unmarshal(raw, &params)
	return params.IdempotencyKey
}

func isMutating(method string) bool {
	switch method {
	case "server.get", "connection.list", "session.list", "session.get",
		"session.snapshot", "session.await", "subscribe":
		return false
	default:
		return true
	}
}

func (s *Service) cacheIdempotent(controllerID, method, key string, result json.RawMessage, controlErr *Error) {
	if key == "" || !isMutating(method) {
		return
	}
	s.mu.Lock()
	s.idem[controllerID+"\x00"+key] = idempotentResult{
		method: method, result: append(json.RawMessage(nil), result...),
		err: controlErr, expires: time.Now().Add(10 * time.Minute),
	}
	s.mu.Unlock()
}

func filterCreatedResult(method string, result any, capability grant) any {
	payload, ok := result.(map[string]any)
	if !ok {
		return result
	}
	switch method {
	case "connection.list":
		connections, ok := payload["connections"].([]map[string]any)
		if !ok {
			return result
		}
		filtered := connections[:0]
		for _, connection := range connections {
			if connection["createdBy"] == capability.controllerID ||
				connection["connectionId"] == capability.subjectConnectionID {
				filtered = append(filtered, connection)
			}
		}
		out := make(map[string]any, len(payload))
		for key, value := range payload {
			out[key] = value
		}
		out["connections"] = filtered
		return out
	case "session.list":
		sessions, ok := payload["sessions"].([]map[string]any)
		if !ok {
			return result
		}
		filtered := sessions[:0]
		for _, session := range sessions {
			if session["createdBy"] == capability.controllerID ||
				session["sessionId"] == capability.subjectSessionID {
				filtered = append(filtered, session)
			}
		}
		out := make(map[string]any, len(payload))
		for key, value := range payload {
			out[key] = value
		}
		out["sessions"] = filtered
		return out
	default:
		return result
	}
}

func (s *Service) allowed(client *controlClient, method string, params json.RawMessage) bool {
	if client.grant.permissions == nil {
		return true
	}
	permission := method
	if method == "subscribe" {
		permission = "session.subscribe"
	}
	if !client.grant.permissions[permission] {
		return false
	}
	if !s.delegatedObjectAllowed(client, method, params) {
		return false
	}
	if method != "session.create" {
		return true
	}
	var p struct {
		Cmd     []string `json:"cmd"`
		Cwd     string   `json:"cwd"`
		Backend struct {
			Kind string `json:"kind"`
		} `json:"backend"`
	}
	if json.Unmarshal(params, &p) != nil || len(p.Cmd) == 0 {
		return false
	}
	scope := client.grant.scope
	if scope.MaxSessions > 0 && s.createdSessionCount(client.controllerID) >= scope.MaxSessions {
		return false
	}
	if p.Backend.Kind == "ssh" && !scope.AllowSSH {
		return false
	}
	if len(scope.AllowedCommands) > 0 {
		ok := false
		for _, command := range scope.AllowedCommands {
			if p.Cmd[0] == command {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	if p.Cwd != "" && len(scope.AllowedCwdRoots) > 0 {
		ok := false
		for _, root := range scope.AllowedCwdRoots {
			if withinRoot(p.Cwd, root) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

func (s *Service) delegatedObjectAllowed(client *controlClient, method string, params json.RawMessage) bool {
	if !client.grant.scope.CreatedBySubject {
		return true
	}
	switch method {
	case "server.get", "connection.list", "session.list", "connection.create":
		if method == "connection.create" && client.grant.scope.MaxConnections > 0 {
			return s.createdConnectionCount(client.controllerID) < client.grant.scope.MaxConnections
		}
		return true
	case "subscribe":
		var request SubscriptionRequest
		if json.Unmarshal(params, &request) != nil {
			return false
		}
		for _, sessionID := range request.SessionIDs {
			if sessionID != client.grant.subjectSessionID &&
				!s.sessionCreatedBy(sessionID, client.controllerID) {
				return false
			}
		}
		return len(request.SessionIDs) > 0
	}
	var reference struct {
		SessionID    string `json:"sessionId"`
		ConnectionID string `json:"connectionId"`
	}
	if json.Unmarshal(params, &reference) != nil {
		return false
	}
	if strings.HasPrefix(method, "session.") {
		if method == "session.create" {
			return reference.ConnectionID == client.grant.subjectConnectionID ||
				s.connectionCreatedBy(reference.ConnectionID, client.controllerID)
		}
		return reference.SessionID != "" &&
			(reference.SessionID == client.grant.subjectSessionID ||
				s.sessionCreatedBy(reference.SessionID, client.controllerID))
	}
	if strings.HasPrefix(method, "connection.") {
		return reference.ConnectionID != "" && s.connectionCreatedBy(reference.ConnectionID, client.controllerID)
	}
	return false
}

func (s *Service) createdConnectionCount(controllerID string) int {
	result, err := s.backend.HandleControl(context.Background(), "connection.list", []byte("{}"), controllerID)
	if err != nil {
		return 0
	}
	count := 0
	if payload, ok := result.(map[string]any); ok {
		if connections, ok := payload["connections"].([]map[string]any); ok {
			for _, connection := range connections {
				if connection["createdBy"] == controllerID {
					count++
				}
			}
		}
	}
	return count
}

func (s *Service) createdSessionCount(controllerID string) int {
	result, err := s.backend.HandleControl(context.Background(), "session.list", []byte("{}"), controllerID)
	if err != nil {
		return 0
	}
	count := 0
	if payload, ok := result.(map[string]any); ok {
		if sessions, ok := payload["sessions"].([]map[string]any); ok {
			for _, session := range sessions {
				if session["createdBy"] == controllerID {
					count++
				}
			}
		}
	}
	return count
}

func (s *Service) connectionCreatedBy(connectionID, controllerID string) bool {
	result, err := s.backend.HandleControl(context.Background(), "connection.list", []byte("{}"), controllerID)
	if err != nil {
		return false
	}
	if payload, ok := result.(map[string]any); ok {
		if connections, ok := payload["connections"].([]map[string]any); ok {
			for _, connection := range connections {
				if connection["connectionId"] == connectionID {
					return connection["createdBy"] == controllerID
				}
			}
		}
	}
	return false
}

func (s *Service) sessionCreatedBy(sessionID, controllerID string) bool {
	result, err := s.backend.HandleControl(context.Background(), "session.get", mustJSON(map[string]any{
		"sessionId": sessionID,
	}), controllerID)
	if err != nil {
		return false
	}
	if payload, ok := result.(map[string]any); ok {
		if session, ok := payload["session"].(map[string]any); ok {
			return session["createdBy"] == controllerID
		}
	}
	return false
}

func withinRoot(path, root string) bool {
	realPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(realRoot, realPath)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)))
}

func (s *Service) subscribe(client *controlClient, raw json.RawMessage) (any, *Error) {
	request, err := decodeParams[SubscriptionRequest](raw)
	if err != nil {
		return nil, err
	}
	id := NewID("sub")
	sub := subscription{id: id, streams: make(map[string]bool), sessions: make(map[string]bool)}
	for _, stream := range request.Streams {
		sub.streams[stream] = true
	}
	for _, sessionID := range request.SessionIDs {
		sub.sessions[sessionID] = true
	}
	client.subMu.Lock()
	client.subscriptions[id] = sub
	client.subMu.Unlock()
	for sessionID, from := range request.From {
		result, controlErr := s.backend.HandleControl(context.Background(), "session.replay", mustJSON(map[string]any{
			"sessionId": sessionID, "generation": from.Generation, "sequence": from.Sequence,
		}), client.controllerID)
		if controlErr != nil {
			if controlErr.Code == "output_gap" {
				event, _ := json.Marshal(Event{
					Event: "session.outputGap", EventID: NewID("evt"),
					Timestamp: time.Now().Format(time.RFC3339Nano), SessionID: sessionID,
					Generation: from.Generation, Data: controlErr.Details,
				})
				_ = client.enqueue(FrameEvent, event)
				continue
			}
			continue
		}
		replay, ok := result.(map[string]any)
		if !ok {
			continue
		}
		data, _ := replay["data"].([]byte)
		sequence, _ := replay["sequence"].(uint64)
		generation, _ := replay["generation"].(uint64)
		if len(data) > 0 {
			s.sendOutput(client, id, sessionID, generation, sequence, data)
		}
	}
	return map[string]any{"subscriptionId": id}, nil
}

func mustJSON(value any) json.RawMessage {
	raw, _ := json.Marshal(value)
	return raw
}

func (s *Service) handleBinaryInput(client *controlClient, body []byte) {
	if len(body) < 2 {
		return
	}
	headerLen := int(binary.BigEndian.Uint16(body[:2]))
	if headerLen > len(body)-2 {
		return
	}
	var header InputHeader
	if json.Unmarshal(body[2:2+headerLen], &header) != nil {
		return
	}
	params := mustJSON(map[string]any{
		"sessionId": header.SessionID, "generation": header.Generation,
		"data": body[2+headerLen:],
	})
	result, controlErr := s.backend.HandleControl(context.Background(), "session.sendBytes", params, client.controllerID)
	if controlErr != nil {
		client.respond(Response{ID: header.RequestID, OK: false, Error: controlErr})
		return
	}
	raw, _ := json.Marshal(result)
	client.respond(Response{ID: header.RequestID, OK: true, Result: raw})
}

type delegateRequest struct {
	SubjectSessionID string   `json:"subjectSessionId"`
	ExpiresInMS      int      `json:"expiresInMs"`
	Permissions      []string `json:"permissions"`
	Scope            Scope    `json:"scope"`
}

func (s *Service) delegate(client *controlClient, raw json.RawMessage) (any, *Error) {
	if client.grant.permissions != nil {
		return nil, NewError("permission_denied", "delegated capabilities cannot delegate")
	}
	request, err := decodeParams[delegateRequest](raw)
	if err != nil {
		return nil, err
	}
	if request.SubjectSessionID == "" || request.ExpiresInMS <= 0 {
		return nil, NewError("invalid_argument", "subjectSessionId and positive expiresInMs are required")
	}
	subject, subjectErr := s.backend.HandleControl(context.Background(), "session.get", mustJSON(map[string]any{
		"sessionId": request.SubjectSessionID,
	}), client.controllerID)
	if subjectErr != nil {
		return nil, subjectErr
	}
	subjectConnectionID := ""
	if payload, ok := subject.(map[string]any); ok {
		if session, ok := payload["session"].(map[string]any); ok {
			subjectConnectionID, _ = session["connectionId"].(string)
		}
	}
	if subjectConnectionID == "" {
		return nil, NewError("internal", "subject session connection is unavailable")
	}
	token := NewToken()
	expires := time.Now().Add(time.Duration(request.ExpiresInMS) * time.Millisecond)
	permissions := make(map[string]bool, len(request.Permissions))
	for _, permission := range request.Permissions {
		permissions[permission] = true
	}
	controllerID := NewID("ctl")
	s.mu.Lock()
	s.grants[token] = grant{
		controllerID: controllerID, expires: expires,
		permissions: permissions, scope: request.Scope,
		subjectSessionID: request.SubjectSessionID, subjectConnectionID: subjectConnectionID,
	}
	s.mu.Unlock()
	_, injectErr := s.backend.HandleControl(context.Background(), "capability.inject", mustJSON(map[string]any{
		"sessionId": request.SubjectSessionID,
		"env": map[string]string{
			"MULTICRUM_CONTROL_ENDPOINT":  s.endpoint,
			"MULTICRUM_CONTROL_TOKEN":     token,
			"MULTICRUM_PARENT_SESSION_ID": request.SubjectSessionID,
			"MULTICRUM_SERVER":            s.serverName,
		},
	}), client.controllerID)
	if injectErr != nil {
		s.mu.Lock()
		delete(s.grants, token)
		s.mu.Unlock()
		return nil, injectErr
	}
	return map[string]any{"endpoint": s.endpoint, "token": token, "expiresAt": expires.Format(time.RFC3339Nano)}, nil
}

type awaitRequest struct {
	SessionID  string          `json:"sessionId"`
	Generation uint64          `json:"generation"`
	TimeoutMS  int             `json:"timeoutMs"`
	Condition  json.RawMessage `json:"condition"`
}

func (s *Service) await(client *controlClient, raw json.RawMessage) (any, *Error) {
	request, err := decodeParams[awaitRequest](raw)
	if err != nil {
		return nil, err
	}
	if request.TimeoutMS <= 0 {
		return nil, NewError("invalid_argument", "positive timeoutMs is required")
	}
	deadline := time.Now().Add(time.Duration(request.TimeoutMS) * time.Millisecond)
	waitStarted := time.Now()
	for {
		result, controlErr := s.awaitCondition(client, request.SessionID, request.Generation, request.Condition, waitStarted)
		if controlErr != nil {
			return nil, controlErr
		}
		if result["matched"] == true {
			return result, nil
		}
		if time.Now().After(deadline) {
			return nil, NewError("timeout", "wait deadline expired")
		}
		select {
		case <-client.done:
			return nil, NewError("unavailable", "controller disconnected")
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func (s *Service) awaitCondition(client *controlClient, sessionID string, generation uint64, raw json.RawMessage, started time.Time) (map[string]any, *Error) {
	var condition struct {
		QuietMS int               `json:"quietMs"`
		Any     []json.RawMessage `json:"any"`
		All     []json.RawMessage `json:"all"`
	}
	if err := json.Unmarshal(raw, &condition); err != nil {
		return nil, NewError("invalid_argument", "invalid await condition")
	}
	for _, child := range condition.Any {
		result, controlErr := s.awaitCondition(client, sessionID, generation, child, started)
		if controlErr != nil {
			return nil, controlErr
		}
		if result["matched"] == true {
			return result, nil
		}
	}
	if len(condition.All) > 0 {
		var sequence any
		for _, child := range condition.All {
			result, controlErr := s.awaitCondition(client, sessionID, generation, child, started)
			if controlErr != nil {
				return nil, controlErr
			}
			if result["matched"] != true {
				return map[string]any{"matched": false}, nil
			}
			sequence = result["outputSequence"]
		}
		return map[string]any{"matched": true, "reason": "all", "outputSequence": sequence}, nil
	}
	if condition.QuietMS > 0 {
		s.mu.RLock()
		last := s.outputAt[outputKey(sessionID, generation)]
		s.mu.RUnlock()
		if last.IsZero() {
			last = started
		}
		if time.Since(last) >= time.Duration(condition.QuietMS)*time.Millisecond {
			return map[string]any{"matched": true, "reason": "quiet"}, nil
		}
		return map[string]any{"matched": false}, nil
	}
	result, controlErr := s.backend.HandleControl(context.Background(), "session.awaitCheck", mustJSON(map[string]any{
		"sessionId": sessionID, "generation": generation, "condition": raw,
	}), client.controllerID)
	if controlErr != nil {
		return nil, controlErr
	}
	matched, ok := result.(map[string]any)
	if !ok {
		return nil, NewError("internal", "invalid await result")
	}
	return matched, nil
}

func outputKey(sessionID string, generation uint64) string {
	return fmt.Sprintf("%s/%d", sessionID, generation)
}

func (s *Service) PublishOutput(sessionID string, generation, sequence uint64, data []byte) {
	s.mu.Lock()
	s.outputAt[outputKey(sessionID, generation)] = time.Now()
	s.mu.Unlock()
	s.mu.RLock()
	clients := make([]*controlClient, 0, len(s.clients))
	for client := range s.clients {
		clients = append(clients, client)
	}
	s.mu.RUnlock()
	for _, client := range clients {
		client.subMu.RLock()
		for _, sub := range client.subscriptions {
			if !sub.streams["session.output"] || (len(sub.sessions) > 0 && !sub.sessions[sessionID]) {
				continue
			}
			s.sendOutput(client, sub.id, sessionID, generation, sequence, data)
		}
		client.subMu.RUnlock()
	}
}

func (s *Service) sendOutput(client *controlClient, subscriptionID, sessionID string, generation, sequence uint64, data []byte) {
	header, _ := json.Marshal(OutputHeader{
		SubscriptionID: subscriptionID, SessionID: sessionID,
		Generation: generation, Sequence: sequence, Length: len(data),
		Timestamp: time.Now().Format(time.RFC3339Nano),
	})
	if len(header) > 65535 {
		return
	}
	body := make([]byte, 2+len(header)+len(data))
	binary.BigEndian.PutUint16(body[:2], uint16(len(header)))
	copy(body[2:], header)
	copy(body[2+len(header):], data)
	if !client.enqueue(FrameOutput, body) {
		gap, _ := json.Marshal(Event{
			Event: "session.outputGap", EventID: NewID("evt"),
			Timestamp: time.Now().Format(time.RFC3339Nano), SessionID: sessionID,
			Generation: generation,
			Data:       map[string]any{"availableSequence": sequence + uint64(len(data))},
		})
		_ = client.enqueue(FrameEvent, gap)
	}
}

func (s *Service) PublishEvent(stream string, event Event) {
	if event.EventID == "" {
		event.EventID = NewID("evt")
	}
	if event.Timestamp == "" {
		event.Timestamp = time.Now().Format(time.RFC3339Nano)
	}
	body, _ := json.Marshal(event)
	s.mu.RLock()
	clients := make([]*controlClient, 0, len(s.clients))
	for client := range s.clients {
		clients = append(clients, client)
	}
	s.mu.RUnlock()
	for _, client := range clients {
		client.subMu.RLock()
		for _, sub := range client.subscriptions {
			if !sub.streams[stream] || (event.SessionID != "" && len(sub.sessions) > 0 && !sub.sessions[event.SessionID]) {
				continue
			}
			_ = client.enqueue(FrameEvent, body)
		}
		client.subMu.RUnlock()
	}
}
