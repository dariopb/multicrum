//go:build !windows

package agentdetect

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const maxNativeReportSize = 64 * 1024

var nativeSocketCounter atomic.Uint64

type NativeServer struct {
	path      string
	listener  net.Listener
	publish   func(Update)
	stop      chan struct{}
	done      chan struct{}
	closeOnce sync.Once

	mu       sync.Mutex
	lastSeq  map[string]uint64
	released map[string]bool
}

type nativeRequest struct {
	ID     string       `json:"id"`
	Method string       `json:"method"`
	Params nativeParams `json:"params"`
}

type nativeParams struct {
	PaneID string `json:"pane_id"`
	Source string `json:"source"`
	Agent  string `json:"agent"`
	State  string `json:"state"`
	Seq    uint64 `json:"seq"`
}

func ListenNativeUpdates(publish func(Update)) (*NativeServer, error) {
	dir := filepath.Join(os.TempDir(), "multicrum-"+strconv.Itoa(os.Getuid()))
	if err := os.Mkdir(dir, 0o700); err != nil && !os.IsExist(err) {
		return nil, err
	}
	path := filepath.Join(dir, fmt.Sprintf("agent-%d-%d.sock", os.Getpid(), nativeSocketCounter.Add(1)))
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = listener.Close()
		_ = os.Remove(path)
		return nil, err
	}
	server := &NativeServer{
		path: path, listener: listener, publish: publish,
		stop: make(chan struct{}), done: make(chan struct{}),
		lastSeq: make(map[string]uint64), released: make(map[string]bool),
	}
	go server.serve()
	return server, nil
}

func (s *NativeServer) Endpoint() string {
	if s == nil {
		return ""
	}
	return s.path
}

func (s *NativeServer) Close() {
	if s == nil {
		return
	}
	s.closeOnce.Do(func() {
		close(s.stop)
		_ = s.listener.Close()
		<-s.done
		_ = os.Remove(s.path)
	})
}

func (s *NativeServer) serve() {
	defer close(s.done)
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.stop:
				return
			default:
				continue
			}
		}
		go s.handle(conn)
	}
}

func (s *NativeServer) handle(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(750 * time.Millisecond))
	reader := bufio.NewReaderSize(conn, maxNativeReportSize)
	line, err := reader.ReadSlice('\n')
	if err != nil || len(line) > maxNativeReportSize {
		return
	}
	var request nativeRequest
	if err := json.Unmarshal(line, &request); err != nil {
		return
	}
	update, ok := s.translate(request)
	if !ok {
		s.writeResponse(conn, request.ID, false)
		return
	}
	if s.publish != nil {
		s.publish(update)
	}
	s.writeResponse(conn, request.ID, true)
}

func (s *NativeServer) translate(request nativeRequest) (Update, bool) {
	id, generation, ok := parseNativeTarget(request.Params.PaneID)
	if !ok || request.Params.Source != "crush" || request.Params.Agent != "crush" {
		return Update{}, false
	}

	switch request.Method {
	case "pane.report_agent":
		state := State(request.Params.State)
		if state != StateIdle && state != StateWorking && state != StateBlocked {
			return Update{}, false
		}
		if !s.acceptSequence(request.Params.PaneID, request.Params.Seq) {
			return Update{}, false
		}
		s.mu.Lock()
		s.released[request.Params.PaneID] = false
		s.mu.Unlock()
		status := Status{
			Provider: ProviderCrush, State: state, Source: SourceNative,
			Confidence: ConfidenceHigh, UpdatedAt: time.Now(),
		}
		return Update{ID: id, Generation: generation, Status: &status}, true
	case "pane.release_agent":
		if !s.acceptSequence(request.Params.PaneID, request.Params.Seq) {
			return Update{}, false
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.released[request.Params.PaneID] {
			return Update{}, false
		}
		s.released[request.Params.PaneID] = true
		return Update{ID: id, Generation: generation}, true
	default:
		return Update{}, false
	}
}

func (s *NativeServer) acceptSequence(target string, sequence uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sequence <= s.lastSeq[target] {
		return false
	}
	s.lastSeq[target] = sequence
	return true
}

func (s *NativeServer) writeResponse(conn net.Conn, id string, success bool) {
	response := map[string]any{"id": id}
	if success {
		response["result"] = map[string]string{"type": "ok"}
	} else {
		response["error"] = map[string]string{"code": "invalid_request"}
	}
	_ = json.NewEncoder(conn).Encode(response)
}

func NativeTarget(id string, generation uint64) string {
	return id + ":" + strconv.FormatUint(generation, 10)
}

func parseNativeTarget(target string) (string, uint64, bool) {
	separator := strings.LastIndexByte(target, ':')
	if separator <= 0 || separator == len(target)-1 {
		return "", 0, false
	}
	generation, err := strconv.ParseUint(target[separator+1:], 10, 64)
	if err != nil || generation == 0 {
		return "", 0, false
	}
	return target[:separator], generation, true
}
