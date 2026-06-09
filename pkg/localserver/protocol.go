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
	Protocol  string `json:"protocol"`
	Version   int    `json:"version"`
	Server    string `json:"server"`
	ServerPID int    `json:"serverPid"`
}

type Resize struct {
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}

const Protocol = "multicrum-local"
const Version = 1
