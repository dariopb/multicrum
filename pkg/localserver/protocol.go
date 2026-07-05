package localserver

type ClientHello struct {
	Protocol   string `json:"protocol"`
	Version    int    `json:"version"`
	Server     string `json:"server"`
	ClientName string `json:"clientName"`
	ClientKind string `json:"clientKind"`
	Cols       int    `json:"cols"`
	Rows       int    `json:"rows"`
}

type ServerHello struct {
	Protocol  string         `json:"protocol"`
	Version   int            `json:"version"`
	Server    string         `json:"server"`
	ServerPID int            `json:"serverPid"`
	Settings  ServerSettings `json:"settings"`
}

type ServerSettings struct {
	Command                  string `json:"command,omitempty"`
	SSH                      string `json:"ssh,omitempty"`
	SSHKey                   string `json:"sshKey,omitempty"`
	SSHUseDefaultKeys        bool   `json:"sshUseDefaultKeys,omitempty"`
	SSHAgent                 bool   `json:"sshAgent,omitempty"`
	SSHKnownHosts            string `json:"sshKnownHosts,omitempty"`
	SSHInsecureIgnoreHostKey bool   `json:"sshInsecureIgnoreHostKey,omitempty"`
	WS                       string `json:"ws,omitempty"`
	TokenSet                 bool   `json:"tokenSet,omitempty"`
	Config                   string `json:"config,omitempty"`
}

type Resize struct {
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}

type ControlRequest struct {
	Action string `json:"action"`
}

type ControlAck struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

const Protocol = "multicrum-local"
const Version = 1
