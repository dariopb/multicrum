package control

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"multicrum/pkg/localserver"
)

type Options struct {
	Server             string
	Endpoint           string
	Token              string
	ClientName         string
	ClientVersion      string
	ResumeControllerID string
}

type Client struct {
	conn    net.Conn
	welcome Welcome
	nextID  atomic.Uint64
	writeMu sync.Mutex

	mu      sync.Mutex
	pending map[string]chan Response
	output  chan Output
	events  chan Event
	done    chan struct{}
	err     error
}

type Output struct {
	Header OutputHeader
	Data   []byte
}

func Dial(options Options) (*Client, error) {
	if options.Server == "" {
		options.Server = os.Getenv("MULTICRUM_SERVER")
		if options.Server == "" {
			options.Server = "default"
		}
	}
	if options.Endpoint == "" {
		options.Endpoint = os.Getenv("MULTICRUM_CONTROL_ENDPOINT")
	}
	if options.Token == "" {
		options.Token = os.Getenv("MULTICRUM_CONTROL_TOKEN")
	}
	if options.Endpoint == "" {
		var err error
		options.Endpoint, err = Endpoint(options.Server)
		if err != nil {
			return nil, err
		}
		if options.Token == "" {
			token, err := os.ReadFile(TokenPath(options.Endpoint))
			if err != nil {
				return nil, fmt.Errorf("read control token: %w", err)
			}
			options.Token = strings.TrimSpace(string(token))
		}
	}
	conn, err := dialEndpoint(options.Endpoint)
	if err != nil {
		return nil, err
	}
	hello, _ := json.Marshal(Hello{
		Protocol: Protocol, Version: Version, Server: options.Server,
		ClientName: options.ClientName, ClientVersion: options.ClientVersion,
		Token: options.Token, ResumeControllerID: options.ResumeControllerID,
		Capabilities: Capabilities,
	})
	if err := localserver.WriteFrame(conn, FrameHello, hello); err != nil {
		conn.Close()
		return nil, err
	}
	typ, body, err := localserver.ReadFrame(conn)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if typ != FrameWelcome {
		conn.Close()
		var response Response
		if json.Unmarshal(body, &response) == nil && response.Error != nil {
			return nil, response.Error
		}
		return nil, fmt.Errorf("unexpected welcome frame %d", typ)
	}
	var welcome Welcome
	if err := json.Unmarshal(body, &welcome); err != nil {
		conn.Close()
		return nil, err
	}
	client := &Client{
		conn: conn, welcome: welcome, pending: make(map[string]chan Response),
		output: make(chan Output, 256), events: make(chan Event, 256), done: make(chan struct{}),
	}
	go client.readLoop()
	return client, nil
}

func (c *Client) Welcome() Welcome       { return c.welcome }
func (c *Client) Outputs() <-chan Output { return c.output }
func (c *Client) Events() <-chan Event   { return c.events }

func (c *Client) Close() error {
	return c.conn.Close()
}

func (c *Client) Call(ctx context.Context, method string, params, result any) error {
	id := fmt.Sprintf("req-%d", c.nextID.Add(1))
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	responseCh := make(chan Response, 1)
	c.mu.Lock()
	c.pending[id] = responseCh
	c.mu.Unlock()
	request, _ := json.Marshal(Request{ID: id, Method: method, Params: raw})
	c.writeMu.Lock()
	err = localserver.WriteFrame(c.conn, FrameRequest, request)
	c.writeMu.Unlock()
	if err != nil {
		c.removePending(id)
		return err
	}
	select {
	case response := <-responseCh:
		if response.Error != nil {
			return response.Error
		}
		if result == nil || len(response.Result) == 0 {
			return nil
		}
		return json.Unmarshal(response.Result, result)
	case <-ctx.Done():
		c.removePending(id)
		return ctx.Err()
	case <-c.done:
		c.mu.Lock()
		err := c.err
		c.mu.Unlock()
		if err == nil {
			err = fmt.Errorf("control connection closed")
		}
		return err
	}
}

func (c *Client) SendBytes(ctx context.Context, sessionID string, generation uint64, data []byte) (int, error) {
	id := fmt.Sprintf("req-%d", c.nextID.Add(1))
	header, _ := json.Marshal(InputHeader{RequestID: id, SessionID: sessionID, Generation: generation})
	if len(header) > 65535 {
		return 0, fmt.Errorf("binary input header is too large")
	}
	body := make([]byte, 2+len(header)+len(data))
	binary.BigEndian.PutUint16(body[:2], uint16(len(header)))
	copy(body[2:], header)
	copy(body[2+len(header):], data)
	responseCh := make(chan Response, 1)
	c.mu.Lock()
	c.pending[id] = responseCh
	c.mu.Unlock()
	c.writeMu.Lock()
	err := localserver.WriteFrame(c.conn, FrameInput, body)
	c.writeMu.Unlock()
	if err != nil {
		c.removePending(id)
		return 0, err
	}
	select {
	case response := <-responseCh:
		if response.Error != nil {
			return 0, response.Error
		}
		var result struct {
			Accepted int `json:"accepted"`
		}
		if err := json.Unmarshal(response.Result, &result); err != nil {
			return 0, err
		}
		return result.Accepted, nil
	case <-ctx.Done():
		c.removePending(id)
		return 0, ctx.Err()
	case <-c.done:
		return 0, fmt.Errorf("control connection closed")
	}
}

func (c *Client) removePending(id string) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func (c *Client) readLoop() {
	defer close(c.done)
	defer close(c.output)
	defer close(c.events)
	for {
		typ, body, err := localserver.ReadFrame(c.conn)
		if err != nil {
			c.mu.Lock()
			c.err = err
			c.mu.Unlock()
			return
		}
		switch typ {
		case FrameResponse:
			var response Response
			if json.Unmarshal(body, &response) != nil {
				continue
			}
			c.mu.Lock()
			ch := c.pending[response.ID]
			delete(c.pending, response.ID)
			c.mu.Unlock()
			if ch != nil {
				ch <- response
			}
		case FrameEvent:
			var event Event
			if json.Unmarshal(body, &event) == nil {
				select {
				case c.events <- event:
				default:
				}
			}
		case FrameOutput:
			if len(body) < 2 {
				continue
			}
			n := int(binary.BigEndian.Uint16(body[:2]))
			if n > len(body)-2 {
				continue
			}
			var header OutputHeader
			if json.Unmarshal(body[2:2+n], &header) != nil {
				continue
			}
			data := append([]byte(nil), body[2+n:]...)
			select {
			case c.output <- Output{Header: header, Data: data}:
			default:
			}
		}
	}
}
