package control

import (
	"context"
	"encoding/json"
	"fmt"
)

const (
	Protocol = "multicrum-control"
	Version  = 1

	FrameHello    byte = 0x20
	FrameWelcome  byte = 0x21
	FrameRequest  byte = 0x22
	FrameResponse byte = 0x23
	FrameEvent    byte = 0x24
	FrameOutput   byte = 0x25
	FrameInput    byte = 0x26
	FrameAck      byte = 0x27
)

var Capabilities = []string{
	"binary-input", "binary-output", "output-replay", "agent-state",
	"session-await", "delegation",
}

type Hello struct {
	Protocol           string   `json:"protocol"`
	Version            int      `json:"version"`
	Server             string   `json:"server"`
	ClientName         string   `json:"clientName,omitempty"`
	ClientVersion      string   `json:"clientVersion,omitempty"`
	Token              string   `json:"token"`
	ResumeControllerID string   `json:"resumeControllerId,omitempty"`
	Capabilities       []string `json:"capabilities,omitempty"`
}

type Welcome struct {
	Protocol     string   `json:"protocol"`
	Version      int      `json:"version"`
	ServerID     string   `json:"serverId"`
	ServerName   string   `json:"serverName"`
	ControllerID string   `json:"controllerId"`
	OwnerPID     int      `json:"ownerPid"`
	Capabilities []string `json:"capabilities"`
	Limits       Limits   `json:"limits"`
}

type Limits struct {
	MaxFrameBytes         int `json:"maxFrameBytes"`
	MaxSessions           int `json:"maxSessions"`
	ReplayBytesPerSession int `json:"replayBytesPerSession"`
}

type Request struct {
	ID     string          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type Response struct {
	ID     string          `json:"id"`
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *Error          `json:"error,omitempty"`
}

type Error struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func NewError(code, message string) *Error {
	return &Error{Code: code, Message: message}
}

type Backend interface {
	HandleControl(context.Context, string, json.RawMessage, string) (any, *Error)
}

type Event struct {
	Event      string         `json:"event"`
	EventID    string         `json:"eventId"`
	Timestamp  string         `json:"timestamp"`
	SessionID  string         `json:"sessionId,omitempty"`
	Generation uint64         `json:"generation,omitempty"`
	Data       map[string]any `json:"data,omitempty"`
}

type OutputHeader struct {
	SubscriptionID string `json:"subscriptionId"`
	SessionID      string `json:"sessionId"`
	Generation     uint64 `json:"generation"`
	Sequence       uint64 `json:"sequence"`
	Length         int    `json:"length"`
	Timestamp      string `json:"timestamp"`
}

type InputHeader struct {
	RequestID  string `json:"requestId"`
	SessionID  string `json:"sessionId"`
	Generation uint64 `json:"generation"`
}

type SubscriptionRequest struct {
	Streams    []string                    `json:"streams"`
	SessionIDs []string                    `json:"sessionIds"`
	From       map[string]SubscriptionFrom `json:"from,omitempty"`
}

type SubscriptionFrom struct {
	Generation uint64 `json:"generation"`
	Sequence   uint64 `json:"sequence"`
}

func decodeParams[T any](raw json.RawMessage) (T, *Error) {
	var value T
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return value, NewError("invalid_argument", fmt.Sprintf("invalid params: %v", err))
	}
	return value, nil
}
