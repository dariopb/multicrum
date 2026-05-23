package ssh_client

import "golang.org/x/crypto/ssh"

// Client is a reusable SSH client factory for opening interactive remote
// sessions with the same resolved options.
type Client struct {
	config ResolvedConfig
}

// New resolves options and returns a reusable client factory.
func New(opts Options) (*Client, error) {
	cfg, err := Resolve(opts)
	if err != nil {
		return nil, err
	}
	return &Client{config: cfg}, nil
}

// Config returns a copy of the resolved client configuration metadata.
func (c *Client) Config() ResolvedConfig { return c.config }

func (c *Client) dial() (*ssh.Client, error) {
	return ssh.Dial("tcp", c.config.Addr, c.config.ClientConfig)
}
