package transport

import (
	"embed"
	"encoding/json"
	"net/http"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
)

//go:embed static/fonts/*.woff2 static/xterm/*
var staticFiles embed.FS

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// SessionInfo is sent to browsers to render the tab bar.
type SessionInfo struct {
	ID     int    `json:"id"`
	Title  string `json:"title"`
	Exited bool   `json:"exited"`
}

type ConnectionInfo struct {
	ID           string        `json:"id"`
	Name         string        `json:"name"`
	FocusedID    int           `json:"focusedId"`
	SessionCount int           `json:"sessionCount"`
	Sessions     []SessionInfo `json:"sessions,omitempty"`
}

type MetaMsg struct {
	Server           string           `json:"server,omitempty"`
	ActiveConnection string           `json:"activeConnection,omitempty"`
	Connections      []ConnectionInfo `json:"connections,omitempty"`
	FocusedID        int              `json:"focusedId"`
	Sessions         []SessionInfo    `json:"sessions"`
}

// ControlMsg is received from the browser for session management.
type ControlMsg struct {
	Action     string `json:"action"` // "focus" | "new" | "kill" | "rename" | "exit" | "move" | "save"
	ID         int    `json:"id"`
	To         int    `json:"to,omitempty"` // move: target index
	Title      string `json:"title,omitempty"`
	Connection string `json:"connection,omitempty"`
	Mode       string `json:"mode,omitempty"`     // new: "same" | "local" | "remote"
	Cmd        string `json:"cmd,omitempty"`      // new local command or remote command
	Target     string `json:"target,omitempty"`   // new remote SSH target
	Port       string `json:"port,omitempty"`     // new remote SSH port
	Password   string `json:"password,omitempty"` // new remote SSH password
	Key        string `json:"key,omitempty"`      // new remote SSH identity file
	Choice     string `json:"choice,omitempty"`   // exit: "respawn" | "remove"
}

// ResizeMsg is received from the browser when xterm.js is resized.
type ResizeMsg struct {
	ID   int `json:"id"`
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}

// wsClient is one connected browser.
type wsClient struct {
	mu        sync.Mutex
	conn      *websocket.Conn
	owner     *WSTransport
	sessionID int // which session this client is currently watching
}

func (c *wsClient) write(data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn.WriteMessage(websocket.BinaryMessage, data)
}

func (c *wsClient) sendSnapshot(sessionID int) {
	if c.owner == nil || c.owner.SnapOf == nil {
		return
	}
	snap := c.owner.SnapOf(sessionID)
	if len(snap) == 0 {
		return
	}
	_ = c.write(append([]byte{0x01}, snap...))
}

// WSTransport serves raw PTY sessions to xterm.js browser clients.
// Protocol (server→client):  0x01 + raw PTY bytes
//
//	0x02 + JSON SessionInfo array
//
// Protocol (client→server):  0x00 + raw keystrokes
//
//	0x01 + JSON ControlMsg
type WSTransport struct {
	mu      sync.Mutex
	clients []*wsClient
	echo    *echo.Echo
	server  *http.Server
	token   string

	// Callbacks wired by the UI layer.
	OnInput          func(sessionID int, data []byte) // keystroke from browser
	OnControl        func(msg ControlMsg)             // session management from browser
	OnResize         func(msg ResizeMsg)              // terminal resize from browser
	SnapOf           func(sessionID int) []byte       // raw PTY snapshot for new clients
	Sessions         func() []SessionInfo             // current active-connection session list
	FocusedID        func() int                       // currently focused session
	Server           func() string                    // current server name
	ActiveConnection func() string                    // active connection name
	Connections      func() []ConnectionInfo          // connection list
}

// NewWSTransport starts listening on addr.
func NewWSTransport(addr, token string) (*WSTransport, error) {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	t := &WSTransport{token: token, echo: e}
	e.GET("/ws", t.handleWS)
	e.GET("/static/fonts/:name", t.serveFont)
	e.GET("/static/xterm/:name", t.serveXTermAsset)
	e.GET("/", t.serveIndex)
	t.server = &http.Server{Addr: addr, Handler: e}
	go func() { _ = t.server.ListenAndServe() }()
	return t, nil
}

// SendPTY broadcasts raw PTY output for sessionID to all clients watching it.
func (t *WSTransport) SendPTY(sessionID int, data []byte) {
	msg := append([]byte{0x01}, data...)
	t.mu.Lock()
	clients := make([]*wsClient, len(t.clients))
	copy(clients, t.clients)
	t.mu.Unlock()
	for _, c := range clients {
		if c.sessionID == sessionID {
			_ = c.write(msg)
		}
	}
}

// BroadcastMeta sends updated session list to all connected clients.
func (t *WSTransport) BroadcastMeta() {
	if t.Sessions == nil {
		return
	}
	sessions := t.Sessions()
	focusedID := 0
	if t.FocusedID != nil {
		focusedID = t.FocusedID()
	}
	server := ""
	if t.Server != nil {
		server = t.Server()
	}
	activeConnection := ""
	if t.ActiveConnection != nil {
		activeConnection = t.ActiveConnection()
	}
	var connections []ConnectionInfo
	if t.Connections != nil {
		connections = t.Connections()
	}
	payload, _ := json.Marshal(MetaMsg{Server: server, ActiveConnection: activeConnection, Connections: connections, FocusedID: focusedID, Sessions: sessions})
	msg := append([]byte{0x02}, payload...)
	t.mu.Lock()
	clients := make([]*wsClient, len(t.clients))
	copy(clients, t.clients)
	t.mu.Unlock()
	for _, c := range clients {
		_ = c.write(msg)
	}
}

func (t *WSTransport) handleWS(ctx echo.Context) error {
	if t.token != "" && ctx.QueryParam("token") != t.token {
		return echo.NewHTTPError(http.StatusUnauthorized, "unauthorized")
	}
	conn, err := upgrader.Upgrade(ctx.Response(), ctx.Request(), nil)
	if err != nil {
		return err
	}

	// Start watching the currently focused session.
	focusedID := 0
	if t.FocusedID != nil {
		focusedID = t.FocusedID()
	}
	client := &wsClient{conn: conn, owner: t, sessionID: focusedID}

	t.mu.Lock()
	t.clients = append(t.clients, client)
	t.mu.Unlock()

	// Send current session list + PTY snapshot.
	t.BroadcastMeta()
	client.sendSnapshot(focusedID)

	defer func() {
		t.mu.Lock()
		for i, cl := range t.clients {
			if cl == client {
				t.clients = append(t.clients[:i], t.clients[i+1:]...)
				break
			}
		}
		t.mu.Unlock()
		_ = conn.Close()
	}()

	for {
		_, data, err := conn.ReadMessage()
		if err != nil || len(data) == 0 {
			return nil
		}
		switch data[0] {
		case 0x00: // raw keystrokes → focused PTY
			if t.OnInput != nil {
				t.OnInput(client.sessionID, data[1:])
			}
		case 0x02: // resize
			var rm ResizeMsg
			if json.Unmarshal(data[1:], &rm) == nil && t.OnResize != nil {
				t.OnResize(rm)
			}
		case 0x01: // control message
			var cm ControlMsg
			if json.Unmarshal(data[1:], &cm) == nil {
				if cm.Action == "focus" {
					client.sessionID = cm.ID
					client.sendSnapshot(cm.ID)
				}
				if t.OnControl != nil {
					t.OnControl(cm)
				}
				if cm.Action == "new" && t.FocusedID != nil {
					client.sessionID = t.FocusedID()
					client.sendSnapshot(client.sessionID)
				}
			}
		}
	}
}

func (t *WSTransport) Close() error { return t.server.Close() }

func (t *WSTransport) serveFont(c echo.Context) error {
	name := c.Param("name")
	if strings.Contains(name, "/") || !strings.HasSuffix(name, ".woff2") {
		return echo.NewHTTPError(http.StatusNotFound)
	}
	data, err := staticFiles.ReadFile("static/fonts/" + name)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound)
	}
	return c.Blob(http.StatusOK, "font/woff2", data)
}

func (t *WSTransport) serveXTermAsset(c echo.Context) error {
	name := c.Param("name")
	if strings.Contains(name, "/") {
		return echo.NewHTTPError(http.StatusNotFound)
	}
	contentType := "application/javascript"
	if strings.HasSuffix(name, ".css") {
		contentType = "text/css"
	} else if !strings.HasSuffix(name, ".js") {
		return echo.NewHTTPError(http.StatusNotFound)
	}
	data, err := staticFiles.ReadFile("static/xterm/" + name)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound)
	}
	return c.Blob(http.StatusOK, contentType, data)
}

func (t *WSTransport) serveIndex(c echo.Context) error {
	token := c.QueryParam("token")
	wsq := ""
	if token != "" {
		wsq = "?token=" + token
	}
	return c.HTML(http.StatusOK, indexHTML(wsq))
}

func indexHTML(wsQuery string) string {
	return strings.Replace(`<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8"/>
<meta name="viewport" content="width=device-width,initial-scale=1,viewport-fit=cover"/>
<title>multicrum</title>
<script>
(function(){
  try{
    var d={theme:"light",accent:"#7c3aed",uiFont:"system",topbarFont:"system",terminalFont:"cascadia",fontSize:14,topbarFontSize:12,terminalFontSize:14,terminalBg:"#0b0b10",scrollback:4000,viewportMode:"device",viewportWidth:1280};
    var raw=JSON.parse(localStorage.getItem("multicrum-settings")||"{}");
    if(raw.font && !raw.uiFont) raw.uiFont=raw.font;
    if(raw.fontMono && !raw.terminalFont) raw.terminalFont=raw.fontMono;
    var s=Object.assign({},d,raw);
    var root=document.documentElement;
    var theme=s.theme||"light";
    var mono=s.terminalFont==="ui"?'ui-monospace,"SF Mono",Menlo,Consolas,monospace':(s.terminalFont==="roboto-mono"?'"Roboto Mono",ui-monospace,"SF Mono",Menlo,Consolas,monospace':'"Cascadia Mono",ui-monospace,"SF Mono",Menlo,Consolas,monospace');
    var system='-apple-system,BlinkMacSystemFont,"Segoe UI",Inter,Roboto,"Helvetica Neue",Arial,sans-serif';
    var fontFor=function(v){ return v==="inter"?'Inter,"Helvetica Neue",Helvetica,Arial,sans-serif':(v==="roboto"?'Roboto,"Helvetica Neue",Arial,sans-serif':(v==="roboto-mono"?'"Roboto Mono",ui-monospace,"SF Mono",Menlo,Consolas,monospace':(v==="cascadia"||v==="mono"?mono:system))); };
    if(theme==="system") theme=matchMedia("(prefers-color-scheme: dark)").matches?"dark":"light";
    root.setAttribute("data-theme",theme);
    root.setAttribute("data-uifont",s.uiFont||"system");
    root.setAttribute("data-topbarfont",s.topbarFont||"system");
    root.setAttribute("data-terminalfont",s.terminalFont||"cascadia");
    root.style.setProperty("--accent-violet",s.accent||"#7c3aed");
    root.style.setProperty("--font",fontFor(s.uiFont));
    root.style.setProperty("--topbar-font",fontFor(s.topbarFont));
    root.style.setProperty("--font-mono",mono);
    root.style.setProperty("--font-size-base",(s.fontSize||14)+"px");
    root.style.setProperty("--topbar-font-size",(s.topbarFontSize||12)+"px");
    root.style.setProperty("--terminal-font-size",(s.terminalFontSize||14)+"px");
    root.style.setProperty("--terminal-bg",s.terminalBg||"#0b0b10");
  }catch(e){}
})();
</script>
<link rel="stylesheet" href="/static/xterm/xterm.css"/>
<script src="/static/xterm/xterm.js"></script>
<script src="/static/xterm/addon-fit.js"></script>
<style>
:root{
  --bg:#17121f;
  --panel:#252033;
  --panel-strong:#1f102fcc;
  --border:#d946ef55;
  --border-strong:#f0abfc88;
  --text:#e8dff5;
  --text-muted:#f5d0fe99;
  --text-soft:#9ca3af;
  --accent-violet:#7c3aed;
  --accent-pink:#ec4899;
  --terminal-bg:#0b0b10;
  --row-hover:#332445;
  --row-selected:#3b1f6b;
  --pill-green-bg:#14532daa;
  --pill-green-fg:#bbf7d0;
  --pill-red-bg:#7f1d1daa;
  --pill-red-fg:#fecaca;
  --pill-amber-bg:#422006aa;
  --pill-amber-fg:#fde68a;
  --pill-gray-bg:#333344;
  --pill-gray-fg:#cbd5e1;
  --font:-apple-system,BlinkMacSystemFont,"Segoe UI",Inter,Roboto,"Helvetica Neue",Arial,sans-serif;
  --topbar-font:var(--font);
  --font-mono:"Cascadia Mono",ui-monospace,"SF Mono",Menlo,Consolas,monospace;
  --font-size-base:14px;
  --topbar-font-size:12px;
  --terminal-font-size:14px;
}
html[data-theme="light"]{
  --bg:#fafafa;
  --panel:#ffffff;
  --panel-strong:#f5f3ff;
  --border:#e5e7eb;
  --border-strong:#d1d5db;
  --text:#111827;
  --text-muted:#6b7280;
  --text-soft:#9ca3af;
  --terminal-bg:#ffffff;
  --row-hover:#f9fafb;
  --row-selected:#ede9fe;
  --pill-green-bg:#dcfce7;
  --pill-green-fg:#166534;
  --pill-red-bg:#fee2e2;
  --pill-red-fg:#991b1b;
  --pill-amber-bg:#fef3c7;
  --pill-amber-fg:#b45309;
  --pill-gray-bg:#f3f4f6;
  --pill-gray-fg:#374151;
}
html[data-theme="dark"]{
  --bg:#0b1220;
  --panel:#111827;
  --panel-strong:#0f172a;
  --border:#1f2937;
  --border-strong:#374151;
  --text:#e5e7eb;
  --text-muted:#9ca3af;
  --text-soft:#6b7280;
  --terminal-bg:#0b0b10;
  --row-hover:#1f2937;
  --row-selected:#312e81;
}
html[data-uifont="inter"]{--font:Inter,"Helvetica Neue",Helvetica,Arial,sans-serif}
html[data-uifont="roboto"]{--font:Roboto,"Helvetica Neue",Arial,sans-serif}
html[data-uifont="cascadia"],html[data-uifont="roboto-mono"]{--font:var(--font-mono)}
html[data-topbarfont="inter"]{--topbar-font:Inter,"Helvetica Neue",Helvetica,Arial,sans-serif}
html[data-topbarfont="roboto"]{--topbar-font:Roboto,"Helvetica Neue",Arial,sans-serif}
html[data-topbarfont="cascadia"],html[data-topbarfont="roboto-mono"]{--topbar-font:var(--font-mono)}
html[data-terminalfont="ui"]{--font-mono:ui-monospace,"SF Mono",Menlo,Consolas,monospace}
html[data-terminalfont="roboto-mono"]{--font-mono:"Roboto Mono",ui-monospace,"SF Mono",Menlo,Consolas,monospace}
@font-face{font-family:"Cascadia Mono";src:url("/static/fonts/CascadiaMono.woff2") format("woff2-variations"),url("/static/fonts/CascadiaMono.woff2") format("woff2");font-weight:200 700;font-style:normal;font-display:swap}
@font-face{font-family:"Cascadia Mono";src:url("/static/fonts/CascadiaMonoItalic.woff2") format("woff2-variations"),url("/static/fonts/CascadiaMonoItalic.woff2") format("woff2");font-weight:200 700;font-style:italic;font-display:swap}
@font-face{font-family:"Roboto";src:url("/static/fonts/RobotoFlex.woff2") format("woff2-variations"),url("/static/fonts/RobotoFlex.woff2") format("woff2");font-weight:100 1000;font-style:normal;font-display:swap}
@font-face{font-family:"Roboto Mono";src:url("/static/fonts/RobotoMono.woff2") format("woff2-variations"),url("/static/fonts/RobotoMono.woff2") format("woff2");font-weight:100 700;font-style:normal;font-display:swap}
*{box-sizing:border-box;margin:0;padding:0}
html,body{height:100%}
body{display:flex;flex-direction:column;height:100vh;background:var(--bg);font-family:var(--font);font-size:var(--font-size-base);color:var(--text)}
#tabbar{display:flex;align-items:stretch;background:linear-gradient(90deg,var(--panel-strong),color-mix(in srgb,var(--accent-violet) 48%,var(--panel)) 45%,color-mix(in srgb,var(--accent-pink) 48%,var(--panel)));border-bottom:1px solid var(--border);box-shadow:0 4px 18px #0008;min-height:calc(var(--topbar-font-size) + 16px);padding:4px 8px;gap:4px;flex-shrink:0;font-family:var(--topbar-font);font-size:var(--topbar-font-size);position:relative}
#menu-wrap{position:relative;flex-shrink:0}
#menu-pop{display:none;position:absolute;top:100%;left:0;margin-top:4px;background:var(--panel);border:1px solid var(--border-strong);border-radius:7px;box-shadow:0 8px 24px #000a;z-index:50;min-width:200px;padding:4px;flex-direction:column}
#menu-pop.open{display:flex}
#keys-pop{display:none;position:absolute;top:100%;left:0;margin-top:4px;background:var(--panel);border:1px solid var(--border-strong);border-radius:7px;box-shadow:0 8px 24px #000a;z-index:50;padding:6px;flex-direction:column;gap:4px;max-width:calc(100vw - 16px);overflow-x:auto;scrollbar-width:thin}
#keys-pop.open{display:flex}
.keys-row{display:flex;gap:4px;flex-wrap:nowrap}
.key-btn{padding:6px 10px;min-width:38px;border:1px solid var(--border-strong);border-radius:5px;background:var(--panel-strong);color:var(--text);font-family:var(--font-mono);font-size:12px;cursor:pointer;white-space:nowrap;flex:0 0 auto}
.key-btn:hover{background:color-mix(in srgb,var(--accent-violet) 30%,var(--panel-strong));border-color:var(--accent-violet)}
.menu-item{display:flex;align-items:center;justify-content:space-between;gap:12px;background:transparent;border:0;color:var(--text);padding:7px 10px;border-radius:5px;cursor:pointer;font:inherit;text-align:left}
.menu-item:hover{background:var(--row-hover)}
.menu-item .kbd{font-family:var(--font-mono);font-size:11px;color:var(--text-soft)}
#tab-list{display:flex;align-items:stretch;gap:0;flex:1 1 auto;min-width:0;overflow-x:auto;scrollbar-width:none}
#tab-list::-webkit-scrollbar{display:none}
.tab-pill{display:inline-flex;align-items:center;height:auto;padding:4px 12px;border:0;border-radius:0;cursor:pointer;font:inherit;color:var(--text-muted);background:transparent;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;max-width:240px;flex:0 0 auto}
.tab-pill:hover{background:color-mix(in srgb,var(--accent-violet) 20%,transparent)}
.tab-pill.active{background:var(--accent-violet);color:#fff;font-weight:700}
.tab-pill.exited{color:#fb7185;text-decoration:line-through}
.tab-newtab{color:var(--text-muted);font-family:var(--font-mono);flex-shrink:0}
.tab-newtab:hover{background:color-mix(in srgb,var(--accent-violet) 30%,transparent);color:#fff}
#brand{display:inline-flex;align-items:center;padding:4px 12px;font-weight:700;color:#fff;background:color-mix(in srgb,var(--accent-pink) 60%,transparent);margin-left:auto;flex-shrink:0}
#connection-state{display:inline-flex;align-items:center;height:auto;padding:0 8px;border-radius:5px;border:1px solid color-mix(in srgb,var(--pill-amber-fg) 45%,transparent);background:var(--pill-amber-bg);color:var(--pill-amber-fg);white-space:nowrap;font-size:11px;margin:0 4px}
#connection-state.connected{border-color:color-mix(in srgb,var(--pill-green-fg) 45%,transparent);background:var(--pill-green-bg);color:var(--pill-green-fg)}
#connection-state.disconnected{border-color:color-mix(in srgb,var(--pill-red-fg) 45%,transparent);background:var(--pill-red-bg);color:var(--pill-red-fg)}
.btn{display:inline-flex;align-items:center;justify-content:center;min-height:calc(1em + 15px);padding:.35em 1em;border-radius:7px;cursor:pointer;font-size:inherit;font-family:inherit;line-height:1.2;border:1px solid var(--border);background:color-mix(in srgb,var(--accent-violet) 24%,var(--panel));color:var(--text);box-shadow:0 1px 8px #0004;transition:background .12s,border-color .12s,transform .12s}
.btn:hover{background:color-mix(in srgb,var(--accent-violet) 36%,var(--panel));border-color:var(--border-strong);transform:translateY(-1px)}
.btn:disabled{opacity:.45;cursor:not-allowed;transform:none}
.btn-green{background:color-mix(in srgb,var(--accent-violet) 28%,var(--panel));border-color:var(--border)}.btn-green:hover{background:color-mix(in srgb,var(--accent-violet) 42%,var(--panel))}
.btn-red{background:color-mix(in srgb,#be123c 30%,var(--panel));border-color:color-mix(in srgb,#fb7185 45%,var(--border));color:var(--pill-red-fg)}.btn-red:hover{background:color-mix(in srgb,#be123c 45%,var(--panel))}
.btn-blue{background:color-mix(in srgb,var(--accent-violet) 34%,var(--panel));border-color:color-mix(in srgb,var(--accent-violet) 55%,var(--border))}.btn-blue:hover{background:color-mix(in srgb,var(--accent-violet) 48%,var(--panel))}
#hint,#status-help{display:none}#terminal{flex:1 1 auto;min-height:0;overflow:hidden;padding:0 0 0 6px;background:var(--terminal-bg)}
#terminal .xterm{font-family:var(--font-mono);height:100%}
#terminal .xterm-viewport{overflow-y:scroll!important;scrollbar-width:none}
#terminal .xterm-viewport::-webkit-scrollbar{display:none}
body.ws-connecting #terminal,body.ws-disconnected #terminal,body.ws-connecting #tabbar,body.ws-disconnected #tabbar,body.ws-connecting #statusbar,body.ws-disconnected #statusbar{filter:grayscale(.55);opacity:.55;pointer-events:none}
#reconnect-overlay{display:none;position:fixed;inset:0;z-index:90;align-items:center;justify-content:center;background:rgba(0,0,0,.28);color:var(--text);font-family:var(--font)}
body.ws-connecting #reconnect-overlay,body.ws-disconnected #reconnect-overlay{display:flex}
#reconnect-box{display:flex;align-items:center;gap:12px;padding:14px 18px;border-radius:10px;border:1px solid var(--border-strong);background:var(--panel);box-shadow:0 8px 32px #000a;font-weight:700}
.spinner{width:18px;height:18px;border:3px solid color-mix(in srgb,var(--accent-violet) 25%,transparent);border-top-color:var(--accent-violet);border-radius:50%;animation:spin .9s linear infinite}
@keyframes spin{to{transform:rotate(360deg)}}
#statusbar{display:flex;align-items:center;gap:8px;min-height:24px;padding:0 10px;background:var(--panel-strong);border-top:1px solid var(--border);color:var(--text-muted);font-family:var(--topbar-font);font-size:12px;flex-shrink:0;white-space:nowrap;overflow:hidden}
#status-main{font-weight:700;color:var(--accent-violet);display:flex;align-items:center;gap:6px;min-width:0;overflow:hidden}
.conn-pill{display:inline-flex;align-items:center;padding:2px 8px;border-radius:999px;background:var(--panel-strong);color:var(--text-muted);border:1px solid var(--border);white-space:nowrap;cursor:pointer;font-size:11px}
.conn-pill.active{background:color-mix(in srgb,var(--accent-violet) 72%,var(--panel));color:#fff;border-color:var(--accent-violet)}
#modal-overlay{display:none;position:fixed;inset:0;background:rgba(0,0,0,.6);z-index:100;align-items:center;justify-content:center}
#modal-overlay.open{display:flex}
#modal{background:var(--panel);border:1px solid var(--border-strong);border-radius:10px;padding:16px;min-width:320px;max-width:620px;width:90%;max-height:90vh;display:flex;flex-direction:column;box-shadow:0 8px 32px #000a;color:var(--text);font-family:var(--font)}
#modal-body{overflow-y:auto;min-height:0;flex:1 1 auto;margin:-4px -4px 0;padding:4px}
#modal h2{font-size:13px;color:var(--text-muted);margin-bottom:10px;font-weight:normal;text-transform:uppercase;letter-spacing:.08em}
#session-filter,#rename-input,.new-input,.settings-field select,.settings-field input[type="text"],.settings-field input[type="color"]{width:100%;background:var(--panel-strong);border:1px solid var(--border-strong);border-radius:5px;color:var(--text);padding:7px 9px;margin-bottom:10px;font-family:inherit;font-size:13px}
.settings-field input[type="range"]{width:100%}
.choice-row{display:block;padding:7px 9px;margin:4px 0;border:1px solid transparent;border-radius:5px;color:var(--text);cursor:pointer}
.choice-row.selected{background:var(--row-selected);border-color:color-mix(in srgb,var(--accent-violet) 70%,var(--border));color:var(--text)}
.choice-row:focus-within{outline:1px dashed var(--accent-violet);outline-offset:2px}
.choice-row input{margin-right:8px}
#exit-form{display:flex;gap:10px;justify-content:center}
#exit-form .btn.selected{outline:2px solid var(--accent-violet);border-color:var(--border-strong)}
#exit-form .btn:focus:not(.selected){outline:1px dashed var(--accent-violet);outline-offset:2px}
#session-list{display:flex;flex-direction:column;gap:4px;max-height:320px;overflow-y:auto}
.sess-item{display:flex;align-items:center;gap:8px;padding:7px 10px;border-radius:5px;cursor:pointer;border:1px solid transparent}
.sess-item:hover{background:var(--row-hover);border-color:var(--border)}
.sess-item.active{background:var(--row-selected);border-color:color-mix(in srgb,var(--accent-violet) 70%,var(--border))}
.sess-item.exited{opacity:.5}
.sess-num{font-size:11px;color:var(--text-soft);min-width:18px;text-align:right}
.sess-title{flex:1;font-size:13px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.sess-badge{font-size:10px;padding:1px 5px;border-radius:3px;background:var(--pill-gray-bg);color:var(--pill-gray-fg)}
.sess-badge.running{background:var(--pill-green-bg);color:var(--pill-green-fg)}
.sess-badge.exited{background:var(--pill-red-bg);color:var(--pill-red-fg)}
#modal-footer{margin-top:12px;font-size:11px;color:var(--text-soft);text-align:center}
.settings-section{padding:12px 0;border-top:1px solid var(--border)}
.settings-section:first-child{border-top:0;padding-top:0}
.settings-section-title{font-size:12px;font-weight:600;color:var(--text-muted);margin:0 0 10px;text-transform:uppercase;letter-spacing:.08em}
.settings-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:12px}
.settings-field{display:flex;flex-direction:column;gap:6px;font-size:12px;color:var(--text-muted)}
.settings-field code,.settings-preview code{font-family:var(--font-mono);font-size:12px;color:var(--text)}
.color-row{display:flex;align-items:center;gap:8px}.color-row input{height:34px;padding:2px;margin:0;max-width:72px}.color-row code{min-width:70px}
.settings-actions{display:flex;justify-content:flex-end;gap:8px;margin-top:12px}
.key-btn{padding:8px 10px;border:1px solid var(--border-strong);border-radius:6px;background:var(--panel-strong);color:var(--text);font-family:var(--font-mono);font-size:12px;cursor:pointer}
.key-btn:hover{background:color-mix(in srgb,var(--accent-violet) 30%,var(--panel-strong));border-color:var(--accent-violet)}
.settings-preview{border:1px solid var(--border);border-radius:8px;padding:10px;background:var(--panel-strong)}
.settings-preview-row{display:flex;gap:8px;align-items:center;margin:6px 0}.settings-preview-row.mono{font-family:var(--font-mono);font-size:var(--terminal-font-size)}
.pill{font-size:11px;padding:2px 7px;border-radius:999px}.pill.green{background:var(--pill-green-bg);color:var(--pill-green-fg)}.pill.amber{background:var(--pill-amber-bg);color:var(--pill-amber-fg)}.pill.red{background:var(--pill-red-bg);color:var(--pill-red-fg)}.pill.gray{background:var(--pill-gray-bg);color:var(--pill-gray-fg)}
@media(max-width:900px){#brand{display:none}}
@media(max-width:620px){.settings-grid{grid-template-columns:1fr}}
</style>
</head>
<body>
<div id="tabbar">
  <div id="menu-wrap">
    <button id="btn-menu" class="btn btn-blue" title="Menu">☰</button>
    <div id="menu-pop">
      <button id="m-sessions" class="menu-item">☰ Sessions <span class="kbd">Alt-S</span></button>
      <button id="m-connections" class="menu-item">☰ Connections <span class="kbd">Ctrl-Alt-O</span></button>
      <button id="m-prevconn" class="menu-item">‹ Prev connection <span class="kbd">Ctrl-Alt-[</span></button>
      <button id="m-nextconn" class="menu-item">› Next connection <span class="kbd">Ctrl-Alt-]</span></button>
      <button id="m-newconn" class="menu-item">+ New connection <span class="kbd">Ctrl-Alt-C</span></button>
      <button id="m-renameconn" class="menu-item">✎ Rename connection <span class="kbd">Ctrl-Alt-E</span></button>
      <button id="m-prev" class="menu-item">‹ Prev session <span class="kbd">Ctrl-Alt-←</span></button>
      <button id="m-next" class="menu-item">› Next session <span class="kbd">Ctrl-Alt-→</span></button>
      <button id="m-new" class="menu-item">+ New <span class="kbd">Alt-N</span></button>
      <button id="m-kill" class="menu-item">✕ Kill <span class="kbd">Alt-K</span></button>
      <button id="m-rename" class="menu-item">✎ Rename <span class="kbd">Alt-R</span></button>
      <button id="m-save" class="menu-item">💾 Save layout <span class="kbd">Alt-P</span></button>
      <button id="m-mouse" class="menu-item">🖱 Mouse: <span id="m-mouse-mode">app</span> <span class="kbd">Alt-M</span></button>
      <button id="m-settings" class="menu-item">⚙ Settings <span class="kbd">Alt-,</span></button>
    </div>
  </div>
  <div id="keys-wrap" style="position:relative;flex-shrink:0">
    <button id="btn-keys" class="btn btn-blue" title="Send key">⌨</button>
    <div id="keys-pop"></div>
  </div>
  <div id="tab-list"></div>
  <button id="btn-newtab" class="tab-pill tab-newtab" title="New session (Alt+N)">[+] Alt+N</button>
  <span id="brand">multicrum</span>
</div>
<div id="terminal"></div>
<div id="statusbar"><span id="status-main">session 1 │ connecting │ 0x0</span></div>
<div id="reconnect-overlay"><div id="reconnect-box"><span class="spinner"></span><span id="reconnect-text">Connecting to multicrum…</span></div></div>

<div id="modal-overlay" tabindex="-1">
  <div id="modal">
    <h2 id="modal-title">Sessions</h2>
    <div id="modal-body">
    <input id="session-filter" placeholder="Filter sessions..." autocomplete="off"/>
    <input id="rename-input" placeholder="Session name" autocomplete="off" style="display:none"/>
    <div id="new-session-form" style="display:none">
      <label class="choice-row selected"><input type="radio" name="new-mode" value="same" checked> Same as current/default</label>
      <label class="choice-row"><input type="radio" name="new-mode" value="local"> Local command</label>
      <input id="new-local-cmd" class="new-input" placeholder="Local command (optional)" autocomplete="off"/>
      <label class="choice-row"><input type="radio" name="new-mode" value="remote"> Remote SSH</label>
      <input id="new-ssh-target" class="new-input" placeholder="user@host" autocomplete="off"/>
      <input id="new-ssh-port" class="new-input" placeholder="SSH port" autocomplete="off" value="22"/>
      <input id="new-ssh-passwd" class="new-input" placeholder="Password (optional)" autocomplete="off" type="password"/>
      <input id="new-ssh-key" class="new-input" placeholder="Key file (optional)" autocomplete="off"/>
      <input id="new-remote-cmd" class="new-input" placeholder="Remote command (optional)" autocomplete="off"/>
    </div>
    <div id="exit-form" style="display:none">
      <button id="exit-respawn" class="btn btn-green selected">Respawn</button>
      <button id="exit-remove" class="btn btn-red">Remove</button>
    </div>
    <div id="settings-form" style="display:none">
      <section class="settings-section">
        <h3 class="settings-section-title">Appearance</h3>
        <div class="settings-grid">
          <label class="settings-field"><span>Theme</span><select id="set-theme"><option value="system">Match system</option><option value="light">Light</option><option value="dark">Dark</option></select></label>
          <label class="settings-field"><span>Accent color</span><div class="color-row"><input type="color" id="set-accent"/><code id="set-accent-val">#7c3aed</code></div></label>
          <label class="settings-field"><span>Console background</span><div class="color-row"><input type="color" id="set-terminalbg"/><code id="set-terminalbg-val">#0b0b10</code></div></label>
          <label class="settings-field"><span>Color scheme</span><select id="set-palette"><option value="xterm">xterm (default)</option><option value="vscode">VS Code Dark+</option><option value="onedark">One Dark</option><option value="solarized-dark">Solarized Dark</option><option value="gruvbox-dark">Gruvbox Dark</option></select></label>
        </div>
      </section>
      <section class="settings-section">
        <h3 class="settings-section-title">Typography</h3>
        <div class="settings-grid">
          <label class="settings-field"><span>UI font</span><select id="set-uifont"><option value="system">System UI</option><option value="inter">Inter / Helvetica</option><option value="roboto">Roboto</option><option value="cascadia">Cascadia Mono</option><option value="roboto-mono">Roboto Mono</option></select></label>
          <label class="settings-field"><span>UI size: <code id="set-fontsize-val">14</code> px</span><input type="range" id="set-fontsize" min="11" max="20" step="1"/></label>
          <label class="settings-field"><span>Top font</span><select id="set-topbarfont"><option value="system">System UI</option><option value="inter">Inter / Helvetica</option><option value="roboto">Roboto</option><option value="cascadia">Cascadia Mono</option><option value="roboto-mono">Roboto Mono</option></select></label>
          <label class="settings-field"><span>Top size: <code id="set-topbarsize-val">12</code> px</span><input type="range" id="set-topbarsize" min="10" max="24" step="1"/></label>
          <label class="settings-field"><span>Terminal font</span><select id="set-terminalfont"><option value="cascadia">Cascadia Mono (bundled)</option><option value="roboto-mono">Roboto Mono (bundled)</option><option value="ui">ui-monospace / system</option></select></label>
          <label class="settings-field"><span>Terminal size: <code id="set-terminalsize-val">14</code> px</span><input type="range" id="set-terminalsize" min="10" max="24" step="1"/></label>
          <label class="settings-field"><span>Scrollback: <code id="set-scrollback-val">4000</code> lines</span><input type="number" id="set-scrollback" min="0" max="100000" step="500"/></label>
          <label class="settings-field"><span>Viewport</span><select id="set-viewportmode"><option value="device">Match device width</option><option value="fixed">Fixed CSS px width</option></select></label>
          <label class="settings-field"><span>Viewport width: <code id="set-viewportwidth-val">1280</code> px</span><input type="number" id="set-viewportwidth" min="320" max="4096" step="32"/></label>
        </div>
      </section>
      <section class="settings-section">
        <h3 class="settings-section-title">Live preview</h3>
        <div class="settings-preview">
          <div class="settings-preview-row"><span class="pill green">running</span><span class="pill amber">connecting</span><span class="pill red">exited</span><span class="pill gray">idle</span></div>
          <div class="settings-preview-row mono">multicrum session 01234567-89ab-cdef</div>
        </div>
        <div class="settings-actions"><button id="settings-reset" class="btn" type="button">Reset to defaults</button><button id="settings-close" class="btn btn-blue" type="button">Close</button></div>
      </section>
    </div>
    <div id="session-list"></div>
    </div>
    <div id="modal-footer">Type to filter &nbsp; ↑↓ navigate &nbsp; Enter select &nbsp; Esc close</div>
  </div>
</div>

<script>
const SETTINGS_KEY = 'multicrum-settings';
const DEFAULT_SETTINGS = {theme:'light',accent:'#7c3aed',uiFont:'system',topbarFont:'system',terminalFont:'cascadia',fontSize:14,topbarFontSize:12,terminalFontSize:14,terminalBg:'#0b0b10',palette:'vscode',scrollback:4000,viewportMode:'device',viewportWidth:1280};
const PALETTES = {
  'xterm': null,
  'vscode': {background:'#1e1e1e',foreground:'#cccccc',cursor:'#aeafad',selectionBackground:'#264f78',
    black:'#000000',red:'#cd3131',green:'#0dbc79',yellow:'#e5e510',blue:'#2472c8',magenta:'#bc3fbc',cyan:'#11a8cd',white:'#e5e5e5',
    brightBlack:'#666666',brightRed:'#f14c4c',brightGreen:'#23d18b',brightYellow:'#f5f543',brightBlue:'#3b8eea',brightMagenta:'#d670d6',brightCyan:'#29b8db',brightWhite:'#e5e5e5'},
  'onedark': {background:'#282c34',foreground:'#abb2bf',cursor:'#528bff',selectionBackground:'#3e4451',
    black:'#282c34',red:'#e06c75',green:'#98c379',yellow:'#e5c07b',blue:'#61afef',magenta:'#c678dd',cyan:'#56b6c2',white:'#abb2bf',
    brightBlack:'#5c6370',brightRed:'#e06c75',brightGreen:'#98c379',brightYellow:'#e5c07b',brightBlue:'#61afef',brightMagenta:'#c678dd',brightCyan:'#56b6c2',brightWhite:'#ffffff'},
  'solarized-dark': {background:'#002b36',foreground:'#839496',cursor:'#93a1a1',selectionBackground:'#073642',
    black:'#073642',red:'#dc322f',green:'#859900',yellow:'#b58900',blue:'#268bd2',magenta:'#d33682',cyan:'#2aa198',white:'#eee8d5',
    brightBlack:'#586e75',brightRed:'#cb4b16',brightGreen:'#586e75',brightYellow:'#657b83',brightBlue:'#839496',brightMagenta:'#6c71c4',brightCyan:'#93a1a1',brightWhite:'#fdf6e3'},
  'gruvbox-dark': {background:'#282828',foreground:'#ebdbb2',cursor:'#ebdbb2',selectionBackground:'#504945',
    black:'#282828',red:'#cc241d',green:'#98971a',yellow:'#d79921',blue:'#458588',magenta:'#b16286',cyan:'#689d6a',white:'#a89984',
    brightBlack:'#928374',brightRed:'#fb4934',brightGreen:'#b8bb26',brightYellow:'#fabd2f',brightBlue:'#83a598',brightMagenta:'#d3869b',brightCyan:'#8ec07c',brightWhite:'#ebdbb2'}
};
function paletteTheme(s){ const p = PALETTES[s.palette||'vscode']; if(!p) return {}; const out = Object.assign({}, p); if(s.terminalBg) out.background = s.terminalBg; return out; }
function normalizeSettings(s){ if(s.font && !s.uiFont) s.uiFont = s.font; if(s.fontMono && !s.terminalFont) s.terminalFont = s.fontMono; return s; }
function loadSettings(){ try{ const raw = normalizeSettings(JSON.parse(localStorage.getItem(SETTINGS_KEY)||'{}')); return Object.assign({}, DEFAULT_SETTINGS, raw); }catch(e){ return Object.assign({}, DEFAULT_SETTINGS); } }
function saveSettings(s){ localStorage.setItem(SETTINGS_KEY, JSON.stringify(s)); }
function monoFontFamily(s){ if(s.terminalFont === 'ui') return 'ui-monospace, SF Mono, Menlo, Consolas, monospace'; if(s.terminalFont === 'roboto-mono') return 'Roboto Mono, ui-monospace, SF Mono, Menlo, Consolas, monospace'; return 'Cascadia Mono, ui-monospace, SF Mono, Menlo, Consolas, monospace'; }
function fontFamilyChoice(value, s){ if(value === 'inter') return 'Inter, Helvetica Neue, Helvetica, Arial, sans-serif'; if(value === 'roboto') return 'Roboto, Helvetica Neue, Arial, sans-serif'; if(value === 'roboto-mono') return 'Roboto Mono, ui-monospace, SF Mono, Menlo, Consolas, monospace'; if(value === 'cascadia' || value === 'mono') return monoFontFamily(s); return '-apple-system, BlinkMacSystemFont, Segoe UI, Inter, Roboto, Helvetica Neue, Arial, sans-serif'; }
function uiFontFamily(s){ return fontFamilyChoice(s.uiFont, s); }
function topbarFontFamily(s){ return fontFamilyChoice(s.topbarFont, s); }
const initialSettings = loadSettings();
const term = new Terminal({cursorBlink:true,scrollback:Number(initialSettings.scrollback||4000),fontSize:Number(initialSettings.terminalFontSize||14),fontFamily:monoFontFamily(initialSettings),theme:Object.assign({background:getComputedStyle(document.documentElement).getPropertyValue('--terminal-bg').trim()||'#0b0b10'}, paletteTheme(initialSettings))});
const fitAddon = new FitAddon.FitAddon();
term.loadAddon(fitAddon);
term.open(document.getElementById('terminal'));

// Size xterm before opening the WebSocket so incoming snapshot bytes are
// written to a properly-dimensioned terminal, not a 0×0 one.
let ws;
let reconnectTimer = null;
let connected = false;
let sessions = [];
let connections = [];
let activeConnection = '';
let serverName = '';
let filteredSessions = [];
let focusedID = 0;
let modalOpen = false;
let modalMode = 'sessions';
let modalCursor = 0;
let sessionFilter = '';
let sessionFiltering = false;
let sessionRenaming = false;
let renameSessionTarget = -1;
let renameConnectionTarget = '';
let connectionFilter = '';
let connectionFiltering = false;
let connectionMoving = false;
let moveMode = false;
let moveStart = -1;
let newChoice = 0;
let newReturnMode = '';
let exitChoice = 0;
const newModes = ['same','local','remote'];
// Mouse mode: 'app' = forward wheel/clicks to child PTY when it enables mouse
// tracking (default); 'select' = always keep wheel for local scrollback and
// release mouse clicks for native text selection, no matter what the child
// requested. Persisted across reloads.
let mouseMode = (localStorage.getItem('multicrum-mouse-mode') === 'select') ? 'select' : 'app';

function applyViewport(s){
  let meta = document.querySelector('meta[name="viewport"]');
  if(!meta){ meta = document.createElement('meta'); meta.name='viewport'; document.head.appendChild(meta); }
  if(s.viewportMode === 'fixed'){
    const w = Math.max(320, Number(s.viewportWidth || 1280));
    meta.content = 'width='+w+',viewport-fit=cover';
  } else {
    meta.content = 'width=device-width,initial-scale=1,viewport-fit=cover';
  }
}

function applySettingsToDOM(s){
  const root = document.documentElement;
  let theme = s.theme || 'light';
  if(theme === 'system') theme = matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
  root.setAttribute('data-theme', theme);
  root.setAttribute('data-uifont', s.uiFont || 'system');
  root.setAttribute('data-topbarfont', s.topbarFont || 'system');
  root.setAttribute('data-terminalfont', s.terminalFont || 'cascadia');
  applyViewport(s);
  root.style.setProperty('--accent-violet', s.accent || '#7c3aed');
  root.style.setProperty('--font', uiFontFamily(s));
  root.style.setProperty('--topbar-font', topbarFontFamily(s));
  root.style.setProperty('--font-mono', monoFontFamily(s));
  root.style.setProperty('--font-size-base', (s.fontSize || 14) + 'px');
  root.style.setProperty('--topbar-font-size', (s.topbarFontSize || 12) + 'px');
  root.style.setProperty('--terminal-font-size', (s.terminalFontSize || 14) + 'px');
  root.style.setProperty('--terminal-bg', s.terminalBg || '#0b0b10');
}
function applySettingsToTerminal(s){
  term.options.fontFamily = monoFontFamily(s);
  term.options.fontSize = Number(s.terminalFontSize || 14);
  term.options.scrollback = Number(s.scrollback || 4000);
  const bg = s.terminalBg || getComputedStyle(document.documentElement).getPropertyValue('--terminal-bg').trim() || '#0b0b10';
  term.options.theme = Object.assign({}, paletteTheme(s), {background:bg});
  if(document.fonts && document.fonts.load){
    const primary = monoFontFamily(s).split(',')[0].trim().replace(/^['"]|['"]$/g, '');
    document.fonts.load((s.terminalFontSize || 14) + 'px "' + primary + '"').finally(() => { fitAndResize(); });
  } else {
    fitAndResize();
  }
}
function syncSettingsForm(s){
  const map = {'set-theme':s.theme,'set-accent':s.accent,'set-terminalbg':s.terminalBg,'set-uifont':s.uiFont,'set-topbarfont':s.topbarFont,'set-terminalfont':s.terminalFont,'set-fontsize':s.fontSize,'set-topbarsize':s.topbarFontSize,'set-terminalsize':s.terminalFontSize,'set-palette':s.palette,'set-scrollback':s.scrollback,'set-viewportmode':s.viewportMode,'set-viewportwidth':s.viewportWidth};
  Object.keys(map).forEach(id => { const el = document.getElementById(id); if(el && map[id] !== undefined) el.value = map[id]; });
  const acc = document.getElementById('set-accent-val'); if(acc) acc.textContent = s.accent;
  const bg = document.getElementById('set-terminalbg-val'); if(bg) bg.textContent = s.terminalBg;
  const fs = document.getElementById('set-fontsize-val'); if(fs) fs.textContent = s.fontSize;
  const tops = document.getElementById('set-topbarsize-val'); if(tops) tops.textContent = s.topbarFontSize;
  const ts = document.getElementById('set-terminalsize-val'); if(ts) ts.textContent = s.terminalFontSize;
  const sb = document.getElementById('set-scrollback-val'); if(sb) sb.textContent = s.scrollback;
  const vw = document.getElementById('set-viewportwidth-val'); if(vw) vw.textContent = s.viewportWidth;
  const vwRow = document.getElementById('set-viewportwidth'); if(vwRow) vwRow.closest('.settings-field').style.display = (s.viewportMode === 'fixed') ? '' : 'none';
}
function applySetting(key, value){
  const s = loadSettings();
  if(key === 'fontSize' || key === 'topbarFontSize' || key === 'terminalFontSize' || key === 'scrollback' || key === 'viewportWidth') value = parseInt(value, 10);
  s[key] = value;
  saveSettings(s);
  applySettingsToDOM(s);
  applySettingsToTerminal(s);
  syncSettingsForm(s);
}
function resetSettings(){
  const s = Object.assign({}, DEFAULT_SETTINGS);
  saveSettings(s);
  applySettingsToDOM(s);
  applySettingsToTerminal(s);
  syncSettingsForm(s);
}
applySettingsToDOM(initialSettings);
if(window.matchMedia){
  matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => {
    const s = loadSettings();
    if(s.theme === 'system') { applySettingsToDOM(s); applySettingsToTerminal(s); }
  });
}

function setConnectionState(state){
  connected = state === 'connected';
  document.body.dataset.conn = state;
  document.body.classList.toggle('ws-connected', state === 'connected');
  document.body.classList.toggle('ws-connecting', state === 'connecting');
  document.body.classList.toggle('ws-disconnected', state === 'disconnected');
  const text = document.getElementById('reconnect-text');
  if(text) text.textContent = state === 'connected' ? '' : (state === 'connecting' ? 'Connecting to '+location.host+'…' : 'Disconnected. Reconnecting in 10s…');
  updateStatusBar();
  renderTabs();
}

function send(data){ if(ws && ws.readyState===1) ws.send(data); }
function control(obj){ const b=new TextEncoder().encode(JSON.stringify(obj)); const m=new Uint8Array(1+b.length); m[0]=0x01; m.set(b,1); send(m); }
function keystroke(str){ const b=new TextEncoder().encode(str); const m=new Uint8Array(1+b.length); m[0]=0x00; m.set(b,1); send(m); }

const terminalWriter = (() => {
  const decoder = new TextDecoder();
  let queue = [];
  let queuedBytes = 0;
  let scheduled = false;
  let syncDepth = 0;
  let syncText = '';

  function flush(){
    scheduled = false;
    if(queue.length === 0) return;
    const text = queue.join('');
    queue = [];
    queuedBytes = 0;
    term.write(text);
  }

  function enqueue(text){
    if(!text) return;
    queue.push(text);
    queuedBytes += text.length;
    if(!scheduled || queuedBytes > 65536){
      scheduled = true;
      requestAnimationFrame(flush);
    }
  }

  function processText(text){
    let i = 0;
    while(i < text.length){
      const start = text.indexOf('\x1b[?2026h', i);
      const end = text.indexOf('\x1b[?2026l', i);
      if(syncDepth > 0){
        if(end === -1){ syncText += text.slice(i); break; }
        syncText += text.slice(i, end + 8);
        syncDepth = Math.max(0, syncDepth - 1);
        i = end + 8;
        if(syncDepth === 0){ enqueue(syncText); syncText = ''; }
        continue;
      }
      if(start !== -1 && (end === -1 || start < end)){
        enqueue(text.slice(i, start));
        syncDepth++;
        syncText = text.slice(start, start + 8);
        i = start + 8;
        continue;
      }
      enqueue(text.slice(i));
      break;
    }
  }

  return {
    write(data){ processText(decoder.decode(data, {stream:true})); },
    reset(){ queue = []; queuedBytes = 0; syncDepth = 0; syncText = ''; scheduled = false; }
  };
})();

function scheduleReconnect(){
  if(reconnectTimer) return;
  reconnectTimer = setTimeout(() => { reconnectTimer = null; startWebSocket(); }, 10000);
}

function startWebSocket(){
  if(ws && (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING)) return;
  const scheme = location.protocol === 'https:' ? 'wss://' : 'ws://';
  ws = new WebSocket(scheme+location.host+'/ws__WS_QUERY__');
  ws.binaryType = 'arraybuffer';
  ws.onopen = () => { if(reconnectTimer){ clearTimeout(reconnectTimer); reconnectTimer = null; } setConnectionState('connected'); sendResize(); };
  ws.onclose = () => { setConnectionState('disconnected'); scheduleReconnect(); };
  ws.onerror = () => { setConnectionState('disconnected'); scheduleReconnect(); };
  setConnectionState('connecting');

  ws.onmessage = e => {
  const buf = new Uint8Array(e.data);
  if(buf[0]===0x01){ terminalWriter.write(buf.slice(1)); }
  else if(buf[0]===0x02){
    const meta = JSON.parse(new TextDecoder().decode(buf.slice(1)));
    if(Array.isArray(meta)){
      sessions = meta;
    } else {
      sessions = meta.sessions || [];
      connections = meta.connections || [];
      const prevConnection = activeConnection;
      activeConnection = meta.activeConnection || '';
      serverName = meta.server || '';
      if(typeof meta.focusedId === 'number' && (focusedID !== meta.focusedId || prevConnection !== activeConnection)){
        focusedID = meta.focusedId;
        // Drain any still-queued bytes from the previous session before
        // clearing, otherwise leftover ESC-mid-sequence bytes will be
        // applied to the cleared screen and show as escape garbage.
        terminalWriter.reset();
        term.reset();
        control({action:'focus',id:focusedID});
        sendResize();
        term.focus();
      }
    }
    updateLabel();
    if(modalOpen && modalMode === 'sessions') renderModal();
    if(modalOpen && modalMode === 'connections') renderConnectionsModal();
  }
};
}

function updateLabel(){
  const s = sessions.find(s=>s.id===focusedID);
  renderTabs();
  updateStatusBar();
  if(s && s.exited && !modalOpen) openExitModal(s.id);
}

function renderTabs(){
  const list = document.getElementById('tab-list');
  list.innerHTML = '';
  let active = null;
  sessions.forEach(s => {
    const b = document.createElement('button');
    b.className = 'tab-pill' + (s.id===focusedID?' active':'') + (s.exited?' exited':'');
    b.title = (s.title||'Session '+(s.id+1));
    b.textContent = (s.title||'Session '+(s.id+1)) + (s.exited?' ✗':'');
    b.onclick = () => focusSession(s.id);
    if(s.id===focusedID) active = b;
    list.appendChild(b);
  });
  if(active) active.scrollIntoView({block:'nearest',inline:'nearest'});
}

function updateStatusBar(){
  const root = document.getElementById('status-main');
  root.innerHTML = '';
  const prefix = document.createElement('span');
  prefix.textContent = 'server:'+(serverName||'default')+' │ conn ';
  root.appendChild(prefix);
  if(connections.length){
    connections.forEach(c => {
      const b = document.createElement('button');
      b.type = 'button';
      b.className = 'conn-pill' + (c.name===activeConnection || c.id===activeConnection ? ' active' : '');
      b.textContent = c.name || c.id || 'connection';
      b.onclick = () => { control({action:'focusConnection',connection:c.name||c.id}); term.focus(); };
      root.appendChild(b);
    });
  } else {
    const b = document.createElement('span');
    b.className = 'conn-pill active';
    b.textContent = activeConnection || 'default';
    root.appendChild(b);
  }
  const suffix = document.createElement('span');
  suffix.textContent = ' │ '+term.cols+'x'+term.rows+' │ mouse:'+mouseMode+' ';
  root.appendChild(suffix);
}

function focusSession(id){
  if(!connected) return;
  focusedID = id;
  term.clear();
  control({action:'focus',id});
  sendResize();
  updateLabel();
  term.focus();
}

function switchSession(delta){
  if(!sessions.length) return;
  const i = Math.max(0, sessions.findIndex(s=>s.id===focusedID));
  const next = sessions[(i + delta + sessions.length) % sessions.length];
  if(next) focusSession(next.id);
}
function filteredSessionCursorForID(id){ const found = filteredSessions.findIndex(s => s.id === id); return found >= 0 ? found : Math.max(0, Math.min(modalCursor, filteredSessions.length-1)); }
function moveSessionAtCursor(delta){
  const cur = filteredSessions[modalCursor];
  if(!cur) return;
  const from = sessions.findIndex(s => s.id === cur.id);
  const to = from + delta;
  if(from < 0 || to < 0 || to >= sessions.length) return;
  control({action:'move',id:cur.id,to});
  const moved = sessions.splice(from, 1)[0];
  sessions.splice(to, 0, moved);
  renderTabs();
  renderModal();
  modalCursor = filteredSessionCursorForID(cur.id);
  renderModal();
}

function currentConnectionName(){ return activeConnection || (connections[0] && (connections[0].name || connections[0].id)) || 'default'; }
function allConnections(){ return connections.length ? connections : [{id:activeConnection||'default',name:activeConnection||'default',sessionCount:sessions.length}]; }
function filteredConnectionsList(){
  const q = connectionFilter.trim().toLowerCase();
  return allConnections().map((c,i)=>Object.assign({__index:i}, c)).filter(c => !q || (((c.name||c.id||'connection')+' '+(c.__index+1)).toLowerCase().includes(q)));
}
function connectionAtCursor(){ const list = filteredConnectionsList(); return list[Math.max(0, Math.min(modalCursor, list.length-1))]; }
function connectionNameAtCursor(){ const c = connectionAtCursor(); return (c && (c.name || c.id)) || currentConnectionName(); }
function filteredCursorForConnectionIndex(index){ const list = filteredConnectionsList(); const found = list.findIndex(c => c.__index === index); return found >= 0 ? found : Math.max(0, Math.min(modalCursor, list.length-1)); }
function focusConnection(name){ if(!name) return; control({action:'focusConnection',connection:name}); term.focus(); }
function newConnection(){
  const suggested = 'conn-' + (connections.length + 1);
  const name = prompt('New connection', suggested);
  if(name && name.trim()){
    control({action:'newConnection',connection:name.trim()});
    if(modalOpen && modalMode === 'connections') requestAnimationFrame(() => openConnections());
  }
}
function removeConnection(name){
  const cur = name || currentConnectionName();
  if(connections.length <= 1 || !cur) return;
  if(confirm('Remove connection "'+cur+'" and close its sessions?')) control({action:'removeConnection',connection:cur});
}
function moveConnectionAtCursor(delta){
  const c = connectionAtCursor();
  if(!c || !connections.length) return;
  const from = c.__index;
  const to = from + delta;
  if(to < 0 || to >= connections.length) return;
  const name = c.name || c.id;
  control({action:'moveConnection',connection:name,to});
  const moved = connections.splice(from, 1)[0];
  connections.splice(to, 0, moved);
  modalCursor = filteredCursorForConnectionIndex(to);
  renderConnectionsModal();
  updateStatusBar();
}

function newSession(){ newReturnMode = ''; openNewSession(); }

function updateNewChoice(){
  document.querySelectorAll('.choice-row').forEach((row,i)=>row.classList.toggle('selected', i===newChoice));
  document.querySelector('input[name="new-mode"][value="'+newModes[newChoice]+'"]').checked = true;
}
function setNewChoice(i){ newChoice = Math.max(0, Math.min(newModes.length-1, i)); updateNewChoice(); }

function updateExitChoice(){
  document.getElementById('exit-respawn').classList.toggle('selected', exitChoice===0);
  document.getElementById('exit-remove').classList.toggle('selected', exitChoice===1);
}
function setExitChoice(i){ exitChoice = Math.max(0, Math.min(1, i)); updateExitChoice(); }

function submitNewSession(){
  if(!connected) return;
  const mode = newModes[newChoice];
  const ret = newReturnMode;
  control({
    action:'new',
    id:0,
    mode,
    cmd: mode === 'local' ? document.getElementById('new-local-cmd').value : document.getElementById('new-remote-cmd').value,
    target: document.getElementById('new-ssh-target').value,
    port: document.getElementById('new-ssh-port').value || '22',
    password: document.getElementById('new-ssh-passwd').value,
    key: document.getElementById('new-ssh-key').value,
  });
  term.clear();
  closeModal();
  if(ret === 'sessions') requestAnimationFrame(() => openModal());
}

function openNewSession(){
  modalOpen = true;
  modalMode = 'new';
  document.getElementById('modal-title').textContent = 'New Session';
  document.getElementById('session-filter').style.display = 'none';
  document.getElementById('rename-input').style.display = 'none';
  document.getElementById('session-list').style.display = 'none';
  document.getElementById('new-session-form').style.display = '';
  document.getElementById('exit-form').style.display = 'none';
  document.getElementById('settings-form').style.display = 'none';
  document.getElementById('modal-footer').textContent = 'Enter start   Esc cancel   ↑/↓ choose   Tab fields';
  const sshPort = document.getElementById('new-ssh-port');
  if(sshPort && !sshPort.value) sshPort.value = '22';
  setNewChoice(0);
  document.getElementById('modal-overlay').classList.add('open');
  updateStatusBar();
  document.querySelector('input[name="new-mode"][value="same"]').focus();
}

// ── Modal ────────────────────────────────────────────────────────────────────

function openModal(){
  modalOpen = true;
  modalMode = 'sessions';
  sessionFiltering = false;
  sessionRenaming = false;
  renameSessionTarget = -1;
  document.getElementById('modal-title').textContent = 'Sessions';
  document.getElementById('session-filter').style.display = 'none';
  document.getElementById('rename-input').style.display = 'none';
  document.getElementById('new-session-form').style.display = 'none';
  document.getElementById('exit-form').style.display = 'none';
  document.getElementById('settings-form').style.display = 'none';
  document.getElementById('session-list').style.display = '';
  filteredSessions = sessions;
  moveMode = false;
  moveStart = -1;
  modalCursor = Math.max(0, sessions.findIndex(s=>s.id===focusedID));
  renderModal();
  document.getElementById('modal-overlay').classList.add('open');
  updateStatusBar();
  document.getElementById('modal-overlay').focus();
}

function openRename(){ openModal(); }

function openConnections(){
  modalOpen = true;
  modalMode = 'connections';
  connectionFiltering = false;
  connectionMoving = false;
  renameConnectionTarget = '';
  document.getElementById('modal-title').textContent = 'Connections';
  document.getElementById('session-filter').style.display = 'none';
  document.getElementById('rename-input').style.display = 'none';
  document.getElementById('new-session-form').style.display = 'none';
  document.getElementById('exit-form').style.display = 'none';
  document.getElementById('settings-form').style.display = 'none';
  document.getElementById('session-list').style.display = '';
  modalCursor = filteredCursorForConnectionIndex(Math.max(0, connections.findIndex(c => c.name===activeConnection || c.id===activeConnection)));
  renderConnectionsModal();
  document.getElementById('modal-overlay').classList.add('open');
  updateStatusBar();
  document.getElementById('modal-overlay').focus();
}

function openRenameConnection(name){
  renameConnectionTarget = name || currentConnectionName();
  modalOpen = true;
  modalMode = 'renameConnection';
  connectionFiltering = false;
  connectionMoving = false;
  document.getElementById('modal-title').textContent = 'Rename Connection';
  document.getElementById('session-filter').style.display = 'none';
  document.getElementById('rename-input').style.display = '';
  document.getElementById('new-session-form').style.display = 'none';
  document.getElementById('exit-form').style.display = 'none';
  document.getElementById('settings-form').style.display = 'none';
  document.getElementById('session-list').style.display = 'none';
  document.getElementById('modal-footer').textContent = 'Enter save   Esc cancel';
  document.getElementById('rename-input').value = renameConnectionTarget;
  document.getElementById('modal-overlay').classList.add('open');
  updateStatusBar();
  document.getElementById('rename-input').focus();
  document.getElementById('rename-input').select();
}

function openExitModal(sessionID){
  modalOpen = true;
  modalMode = 'exit';
  focusedID = sessionID;
  document.getElementById('modal-title').textContent = 'Session Exited';
  document.getElementById('session-filter').style.display = 'none';
  document.getElementById('rename-input').style.display = 'none';
  document.getElementById('new-session-form').style.display = 'none';
  document.getElementById('session-list').style.display = 'none';
  document.getElementById('settings-form').style.display = 'none';
  document.getElementById('exit-form').style.display = '';
  document.getElementById('modal-footer').textContent = 'Enter confirm   ←/→ choose   Esc close';
  setExitChoice(0);
  document.getElementById('modal-overlay').classList.add('open');
  updateStatusBar();
  document.getElementById('exit-respawn').focus();
}



function openSettings(){
  modalOpen = true;
  modalMode = 'settings';
  document.getElementById('modal-title').textContent = 'Settings';
  document.getElementById('session-filter').style.display = 'none';
  document.getElementById('rename-input').style.display = 'none';
  document.getElementById('new-session-form').style.display = 'none';
  document.getElementById('exit-form').style.display = 'none';
  document.getElementById('session-list').style.display = 'none';
  document.getElementById('settings-form').style.display = '';
  document.getElementById('modal-footer').textContent = 'Changes save automatically   Esc close';
  syncSettingsForm(loadSettings());
  document.getElementById('modal-overlay').classList.add('open');
  updateStatusBar();
  document.getElementById('set-theme').focus();
}

const KEY_ROWS = [
  [
    {label:'^C', title:'Ctrl-C (interrupt)', bytes:'\x03'},
    {label:'^A', title:'Ctrl-A',              bytes:'\x01'},
    {label:'^Z', title:'Ctrl-Z (suspend)',    bytes:'\x1a'},
    {label:'^V', title:'Ctrl-V',              bytes:'\x16'},
    {label:'Esc',title:'Escape',              bytes:'\x1b'},
    {label:'⇥', title:'Tab',                   bytes:'\t'},
    {label:'⌫', title:'Backspace',             bytes:'\x7f'},
    {label:'⏎', title:'Enter',                 bytes:'\r'},
  ],
  [
    {label:'F1', bytes:'\x1bOP'},   {label:'F2', bytes:'\x1bOQ'},
    {label:'F3', bytes:'\x1bOR'},   {label:'F4', bytes:'\x1bOS'},
    {label:'F5', bytes:'\x1b[15~'}, {label:'F6', bytes:'\x1b[17~'},
    {label:'F7', bytes:'\x1b[18~'}, {label:'F8', bytes:'\x1b[19~'},
    {label:'F9', bytes:'\x1b[20~'}, {label:'F10',bytes:'\x1b[21~'},
    {label:'F11',bytes:'\x1b[23~'}, {label:'F12',bytes:'\x1b[24~'},
  ],
  [
    {label:'←',  title:'Left',     bytes:'\x1b[D'},
    {label:'↓',  title:'Down',     bytes:'\x1b[B'},
    {label:'↑',  title:'Up',       bytes:'\x1b[A'},
    {label:'→',  title:'Right',    bytes:'\x1b[C'},
    {label:'⇱',  title:'Home',     bytes:'\x1b[H'},
    {label:'⇲',  title:'End',      bytes:'\x1b[F'},
    {label:'PgUp',title:'Page Up', bytes:'\x1b[5~'},
    {label:'PgDn',title:'Page Down',bytes:'\x1b[6~'},
    {label:'Ins',title:'Insert',   bytes:'\x1b[2~'},
    {label:'Del',title:'Delete',   bytes:'\x1b[3~'},
  ],
];

function buildKeysPanel(){
  const root = document.getElementById('keys-pop');
  root.innerHTML = '';
  KEY_ROWS.forEach(row => {
    const r = document.createElement('div');
    r.className = 'keys-row';
    row.forEach(k => {
      const b = document.createElement('button');
      b.type = 'button';
      b.className = 'key-btn';
      b.textContent = k.label;
      if(k.title) b.title = k.title;
      b.onmousedown = e => e.preventDefault();
      b.onclick = () => { keystroke(k.bytes); term.focus(); };
      r.appendChild(b);
    });
    root.appendChild(r);
  });
}

function openKeysPanel(open){
  const p = document.getElementById('keys-pop');
  const want = (open === undefined) ? !p.classList.contains('open') : open;
  p.classList.toggle('open', want);
}



function closeModal(){
  modalOpen = false;
  sessionFiltering = false;
  sessionRenaming = false;
  renameSessionTarget = -1;
  renameConnectionTarget = '';
  connectionFiltering = false;
  connectionMoving = false;
  document.getElementById('modal-overlay').classList.remove('open');
  document.getElementById('modal-footer').textContent = 'Type to filter   ↑↓ navigate   Enter select   Esc close';
  updateStatusBar();
  term.focus();
}

function renderModal(){
  if(modalMode !== 'sessions') return;
  const el = document.getElementById('session-list');
  el.innerHTML = '';
  const q = sessionFilter.trim().toLowerCase();
  filteredSessions = sessions.filter(s => !q || ((s.title||'')+' '+(s.id+1)).toLowerCase().includes(q));
  if(modalCursor < 0) modalCursor = 0;
  if(modalCursor >= filteredSessions.length) modalCursor = Math.max(0, filteredSessions.length-1);
  if(sessionFilter.trim()){
    const badge = document.createElement('div');
    badge.className = 'sess-badge';
    badge.style.background = 'var(--accent-pink)';
    badge.style.color = '#fff';
    badge.style.margin = '0 0 6px 0';
    badge.textContent = 'Filter: '+sessionFilter;
    el.appendChild(badge);
  }
  if(sessionRenaming){
    const input = document.getElementById('rename-input');
    input.style.display = '';
  } else {
    document.getElementById('rename-input').style.display = 'none';
  }
  filteredSessions.forEach((s, i) => {
    const row = document.createElement('div');
    row.className = 'sess-item' + (s.id===focusedID?' active':'') + (s.exited?' exited':'');
    if(i === modalCursor) row.style.outline = moveMode ? '2px solid #ffaa33' : '1px solid #6af';
    if(i === modalCursor && moveMode) row.style.background = 'color-mix(in srgb, #ffaa33 35%, transparent)';
    row.innerHTML =
      '<span class="sess-num">'+(s.id+1)+'</span>'+
      '<span class="sess-title">'+escHtml(s.title||('Session '+(s.id+1)))+'</span>'+
      '<span class="sess-badge '+(s.exited?'exited':'running')+'">'+(s.exited?'exited':'running')+'</span>';
    row.onclick = () => { focusSession(s.id); closeModal(); };
    el.appendChild(row);
  });
  if(!filteredSessions.length){
    const empty = document.createElement('div');
    empty.className = 'sess-item';
    empty.textContent = 'No matching sessions';
    el.appendChild(empty);
  }
  const rows = el.querySelectorAll('.sess-item');
  if(rows[modalCursor]) rows[modalCursor].scrollIntoView({block:'nearest'});
  const footer = document.getElementById('modal-footer');
  footer.textContent = sessionRenaming ? 'Rename: Enter save   Esc cancel   Backspace edits' : (sessionFiltering ? 'Filter: type pattern   Enter apply   Esc actions   Ctrl+U clear' : (moveMode ? 'Move: ↑/↓ reorder   M/Esc stop moving' : '↑↓ navigate   Enter focus   N new   R rename   M move   F filter   Del/X remove   Esc close'));
}

function renderConnectionsModal(){
  if(modalMode !== 'connections') return;
  const el = document.getElementById('session-list');
  el.innerHTML = '';
  const list = filteredConnectionsList();
  if(modalCursor < 0) modalCursor = 0;
  if(modalCursor >= list.length) modalCursor = Math.max(0, list.length-1);
  if(connectionFilter.trim()){
    const badge = document.createElement('div');
    badge.className = 'sess-badge';
    badge.style.background = 'var(--accent-pink)';
    badge.style.color = '#fff';
    badge.style.margin = '0 0 6px 0';
    badge.textContent = 'Filter: '+connectionFilter;
    el.appendChild(badge);
  }
  list.forEach((c, i) => {
    const name = c.name || c.id || 'connection';
    const row = document.createElement('div');
    row.className = 'sess-item' + (name===activeConnection || c.id===activeConnection ? ' active' : '');
    if(i === modalCursor) row.style.outline = connectionMoving ? '2px solid #ffaa33' : '1px solid #6af';
    if(i === modalCursor && connectionMoving) row.style.background = 'color-mix(in srgb, #ffaa33 35%, transparent)';
    row.innerHTML =
      '<span class="sess-num">'+(c.__index+1)+'</span>'+
      '<span class="sess-title">'+escHtml(name)+'</span>'+
      '<span class="sess-badge running">'+(c.sessionCount||0)+' sessions</span>';
    row.onclick = () => { focusConnection(name); closeModal(); };
    el.appendChild(row);
  });
  if(!list.length){
    const empty = document.createElement('div');
    empty.className = 'sess-item';
    empty.textContent = 'No matching connections';
    el.appendChild(empty);
  }
  const rows = el.querySelectorAll('.sess-item');
  if(rows[modalCursor]) rows[modalCursor].scrollIntoView({block:'nearest'});
  document.getElementById('modal-footer').textContent = connectionMoving ? 'Move: ↑↓ reorder   M/Esc stop moving' : '↑↓ navigate   Enter focus   N new   R rename   M move   F filter   Del/X remove   Esc close';
}

function escHtml(s){ return s.replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;'); }

// Keyboard nav inside modal
document.getElementById('modal-overlay').addEventListener('keydown', e => {
  if(!modalOpen) return;
  if(e.key==='Escape' && modalMode !== 'sessions' && modalMode !== 'connections' && modalMode !== 'new'){ e.preventDefault(); closeModal(); return; }
  if(modalMode === 'rename'){
    if(e.key==='Enter'){
      e.preventDefault();
      control({action:'rename',id:focusedID,title:document.getElementById('rename-input').value});
      closeModal();
    }
    return;
  }
  if(modalMode === 'renameConnection'){
    if(e.key==='Enter'){
      e.preventDefault();
      control({action:'renameConnection',connection:renameConnectionTarget||currentConnectionName(),title:document.getElementById('rename-input').value});
      closeModal();
    }
    return;
  }
  if(modalMode === 'new'){
    if(e.key==='Escape' && newReturnMode === 'sessions'){ e.preventDefault(); closeModal(); requestAnimationFrame(() => openModal()); return; }
    if(e.key==='ArrowDown' || e.key==='ArrowRight'){ e.preventDefault(); setNewChoice(newChoice+1); return; }
    if(e.key==='ArrowUp' || e.key==='ArrowLeft'){ e.preventDefault(); setNewChoice(newChoice-1); return; }
    if(e.key==='Tab'){ return; }
    if(e.key==='Enter'){ e.preventDefault(); submitNewSession(); return; }
    return;
  }
  if(modalMode === 'settings'){
    if(e.key==='Enter' && e.target.tagName !== 'TEXTAREA'){ e.preventDefault(); }
    return;
  }
  if(modalMode === 'exit'){
    if(e.key==='ArrowRight' || e.key==='Tab'){ e.preventDefault(); setExitChoice(1-exitChoice); return; }
    if(e.key==='ArrowLeft'){ e.preventDefault(); setExitChoice(1-exitChoice); return; }
    if(e.key==='Enter'){
      e.preventDefault();
      if(exitChoice===1 && sessions.length<=1 && !confirm('Remove the last session and close this connection/server?')) return;
      control({action:'exit',id:focusedID,choice:exitChoice===0?'respawn':'remove'});
      if(exitChoice===0) sendResize();
      closeModal();
      return;
    }
    if(e.key==='Escape'){ e.preventDefault(); return; }
    return;
  }
  if(modalMode === 'connections'){
    const list = filteredConnectionsList();
    if(connectionFiltering){
      if(e.key==='Escape' || e.key==='Enter'){ e.preventDefault(); connectionFiltering = false; renderConnectionsModal(); document.getElementById('modal-overlay').focus(); return; }
      if(e.key==='Backspace'){ e.preventDefault(); connectionFilter = connectionFilter.slice(0, -1); modalCursor = 0; renderConnectionsModal(); return; }
      if((e.ctrlKey || e.metaKey) && (e.key||'').toLowerCase()==='u'){ e.preventDefault(); connectionFilter = ''; modalCursor = 0; renderConnectionsModal(); return; }
      if(e.key && e.key.length===1 && !e.ctrlKey && !e.metaKey){ e.preventDefault(); connectionFilter += e.key; modalCursor = 0; renderConnectionsModal(); return; }
      return;
    }
    if(e.key==='Escape'){ if(connectionMoving){ e.preventDefault(); connectionMoving=false; renderConnectionsModal(); return; } }
    if(e.key==='ArrowDown' && list.length){ e.preventDefault(); if(connectionMoving){ moveConnectionAtCursor(1); } else { modalCursor=(modalCursor+1)%list.length; renderConnectionsModal(); } return; }
    if(e.key==='ArrowUp' && list.length){ e.preventDefault(); if(connectionMoving){ moveConnectionAtCursor(-1); } else { modalCursor=(modalCursor-1+list.length)%list.length; renderConnectionsModal(); } return; }
    if(e.key==='Enter'){ e.preventDefault(); focusConnection(connectionNameAtCursor()); closeModal(); return; }
    if(e.key==='n' || e.key==='N'){ e.preventDefault(); newConnection(); return; }
    if(e.key==='r' || e.key==='R'){ e.preventDefault(); openRenameConnection(connectionNameAtCursor()); return; }
    if(e.key==='m' || e.key==='M'){ e.preventDefault(); connectionMoving = !connectionMoving; renderConnectionsModal(); return; }
    if(e.key==='f' || e.key==='F'){ e.preventDefault(); connectionFiltering = true; renderConnectionsModal(); return; }
    if(e.key==='Delete' || e.key==='x' || e.key==='X'){ e.preventDefault(); removeConnection(connectionNameAtCursor()); return; }
    return;
  }
  if(modalMode === 'sessions'){
    if(sessionRenaming){
      if(e.key==='Enter'){
        e.preventDefault();
        control({action:'rename',id:renameSessionTarget,title:document.getElementById('rename-input').value});
        sessionRenaming = false; renameSessionTarget = -1; document.getElementById('rename-input').style.display = 'none'; renderModal(); return;
      }
      if(e.key==='Escape'){ e.preventDefault(); sessionRenaming = false; renameSessionTarget = -1; renderModal(); document.getElementById('modal-overlay').focus(); return; }
      return;
    }
    if(sessionFiltering){
      if(e.key==='Escape' || e.key==='Enter'){ e.preventDefault(); sessionFiltering = false; renderModal(); document.getElementById('modal-overlay').focus(); return; }
      if(e.key==='Backspace'){ e.preventDefault(); sessionFilter = sessionFilter.slice(0, -1); modalCursor = 0; renderModal(); return; }
      if((e.ctrlKey || e.metaKey) && (e.key||'').toLowerCase()==='u'){ e.preventDefault(); sessionFilter = ''; modalCursor = 0; renderModal(); return; }
      if(e.key && e.key.length===1 && !e.ctrlKey && !e.metaKey){ e.preventDefault(); sessionFilter += e.key; modalCursor = 0; renderModal(); return; }
      return;
    }
    if(e.key==='Escape'){ if(moveMode){ e.preventDefault(); moveMode=false; renderModal(); return; } }
    if(e.key==='ArrowDown' && filteredSessions.length){ e.preventDefault(); if(moveMode){ moveSessionAtCursor(1); } else { modalCursor=(modalCursor+1)%filteredSessions.length; renderModal(); } return; }
    if(e.key==='ArrowUp' && filteredSessions.length){ e.preventDefault(); if(moveMode){ moveSessionAtCursor(-1); } else { modalCursor=(modalCursor-1+filteredSessions.length)%filteredSessions.length; renderModal(); } return; }
    if(e.key==='Enter'){ e.preventDefault(); if(filteredSessions[modalCursor]){ focusSession(filteredSessions[modalCursor].id); closeModal(); } return; }
    if(e.key==='n' || e.key==='N'){ e.preventDefault(); newReturnMode = 'sessions'; openNewSession(); return; }
    if(e.key==='r' || e.key==='R'){ e.preventDefault(); const s=filteredSessions[modalCursor]; if(s){ sessionRenaming=true; renameSessionTarget=s.id; document.getElementById('rename-input').value=s.title||('Session '+(s.id+1)); renderModal(); document.getElementById('rename-input').focus(); document.getElementById('rename-input').select(); } return; }
    if(e.key==='m' || e.key==='M'){ e.preventDefault(); moveMode=!moveMode; renderModal(); return; }
    if(e.key==='f' || e.key==='F'){ e.preventDefault(); sessionFiltering=true; renderModal(); return; }
    if(e.key==='Delete' || e.key==='x' || e.key==='X'){ e.preventDefault(); const s=filteredSessions[modalCursor]; if(s && sessions.length>1 && confirm('Delete session "'+(s.title||('Session '+(s.id+1)))+'"?')){ control({action:'kill',id:s.id}); } return; }
    return;
  }
});

document.getElementById('session-filter').addEventListener('input', () => { modalCursor = 0; renderModal(); });

// Close on backdrop click
document.getElementById('modal-overlay').addEventListener('click', e => {
  if(e.target === document.getElementById('modal-overlay')) closeModal();
});

document.querySelectorAll('input[name="new-mode"]').forEach((el,i)=>el.onchange=()=>setNewChoice(i));
document.getElementById('exit-respawn').onclick = () => { setExitChoice(0); control({action:'exit',id:focusedID,choice:'respawn'}); sendResize(); closeModal(); };
document.getElementById('exit-remove').onclick = () => { setExitChoice(1); if(sessions.length<=1 && !confirm('Remove the last session and close this connection/server?')) return; control({action:'exit',id:focusedID,choice:'remove'}); closeModal(); };
document.getElementById('set-theme').onchange = e => applySetting('theme', e.target.value);
document.getElementById('set-accent').oninput = e => applySetting('accent', e.target.value);
document.getElementById('set-terminalbg').oninput = e => applySetting('terminalBg', e.target.value);
document.getElementById('set-uifont').onchange = e => applySetting('uiFont', e.target.value);
document.getElementById('set-topbarfont').onchange = e => applySetting('topbarFont', e.target.value);
document.getElementById('set-terminalfont').onchange = e => applySetting('terminalFont', e.target.value);
document.getElementById('set-fontsize').oninput = e => applySetting('fontSize', e.target.value);
document.getElementById('set-topbarsize').oninput = e => applySetting('topbarFontSize', e.target.value);
document.getElementById('set-terminalsize').oninput = e => applySetting('terminalFontSize', e.target.value);
document.getElementById('set-palette').onchange = e => applySetting('palette', e.target.value);
document.getElementById('set-scrollback').oninput = e => applySetting('scrollback', e.target.value);
document.getElementById('set-viewportmode').onchange = e => { applySetting('viewportMode', e.target.value); fitAndResize(); };
document.getElementById('set-viewportwidth').oninput = e => { applySetting('viewportWidth', e.target.value); fitAndResize(); };
document.getElementById('settings-reset').onclick = resetSettings;
document.getElementById('settings-close').onclick = closeModal;
function openMenu(open){
  const p = document.getElementById('menu-pop');
  if(open === undefined) open = !p.classList.contains('open');
  p.classList.toggle('open', open);
}
document.getElementById('btn-menu').onclick = (e) => { e.stopPropagation(); openMenu(); };
document.addEventListener('click', e => { if(!e.target.closest('#menu-wrap')) openMenu(false); });
document.getElementById('m-sessions').onclick = () => { openMenu(false); openModal(); };
document.getElementById('m-connections').onclick = () => { openMenu(false); openConnections(); };
document.getElementById('m-prevconn').onclick = () => { openMenu(false); control({action:'prevConnection'}); term.focus(); };
document.getElementById('m-nextconn').onclick = () => { openMenu(false); control({action:'nextConnection'}); term.focus(); };
document.getElementById('m-newconn').onclick = () => { openMenu(false); newConnection(); term.focus(); };
document.getElementById('m-renameconn').onclick = () => { openMenu(false); openRenameConnection(currentConnectionName()); };
document.getElementById('m-prev').onclick = () => { openMenu(false); switchSession(-1); term.focus(); };
document.getElementById('m-next').onclick = () => { openMenu(false); switchSession(1);  term.focus(); };
document.getElementById('m-new').onclick = () => { openMenu(false); newSession(); };
document.getElementById('m-kill').onclick = () => { openMenu(false); if(connected && sessions.length>1){ control({action:'kill',id:focusedID}); } term.focus(); };
document.getElementById('m-rename').onclick = () => { openMenu(false); openRename(); };
document.getElementById('m-save').onclick = () => { openMenu(false); if(connected){ control({action:'save',id:0}); } term.focus(); };
document.getElementById('m-mouse').onclick = () => { openMenu(false); toggleMouseMode(); term.focus(); };
document.getElementById('m-settings').onclick = () => { openMenu(false); openSettings(); };
buildKeysPanel();
document.getElementById('btn-keys').onclick = (e) => { e.stopPropagation(); openKeysPanel(); };
document.addEventListener('click', e => { if(!e.target.closest('#keys-wrap')) openKeysPanel(false); });
document.getElementById('btn-newtab').onclick = () => { newSession(); };

// Intercept browser shortcuts before they're swallowed.
function handleAppShortcut(e){
  // Tab nav uses Ctrl+Alt+Arrow to match the local TUI. Note Ctrl+Alt+M is
  // dev-tools in Edge but Ctrl+Alt+Arrow is free across browsers/OSes.
  // Letter shortcuts use plain Alt+letter to avoid Edge's Ctrl+Alt+T/W/R/M
  // claims (reopen-tab, close-window, reload-bypass, dev-tools).
  const arrowL = (e.key==='ArrowLeft'  || e.code==='ArrowLeft');
  const arrowR = (e.key==='ArrowRight' || e.code==='ArrowRight');
  const onlyCtrlAlt = e.ctrlKey && e.altKey && !e.shiftKey && !e.metaKey;
  const bracketL = (e.key==='[' || e.code==='BracketLeft');
  const bracketR = (e.key===']' || e.code==='BracketRight');
  if(onlyCtrlAlt && arrowL){ e.preventDefault(); e.stopPropagation(); switchSession(-1); return true; }
  if(onlyCtrlAlt && arrowR){ e.preventDefault(); e.stopPropagation(); switchSession(1); return true; }
  if(onlyCtrlAlt && bracketL){ e.preventDefault(); e.stopPropagation(); control({action:'prevConnection'}); return true; }
  if(onlyCtrlAlt && bracketR){ e.preventDefault(); e.stopPropagation(); control({action:'nextConnection'}); return true; }
  if(onlyCtrlAlt && (e.code==='KeyO' || (e.key||'').toLowerCase()==='o')){ e.preventDefault(); e.stopPropagation(); openConnections(); return true; }
  if(onlyCtrlAlt && (e.code==='KeyE' || (e.key||'').toLowerCase()==='e')){ e.preventDefault(); e.stopPropagation(); openRenameConnection(currentConnectionName()); return true; }
  if(onlyCtrlAlt && (e.code==='KeyC' || (e.key||'').toLowerCase()==='c')){ e.preventDefault(); e.stopPropagation(); newConnection(); return true; }
  if(e.altKey && !e.ctrlKey && !e.shiftKey && !e.metaKey){
    // Prefer e.code over e.key because some keyboard layouts (US-Intl,
    // macOS Option, AltGr layouts) compose Alt+letter into special chars,
    // which would make e.key something other than the letter.
    const c = e.code || '';
    const k = (e.key||'').toLowerCase();
    if(c==='KeyS' || k==='s'){ e.preventDefault(); e.stopPropagation(); openModal(); return true; }
    if(c==='KeyR' || k==='r'){ e.preventDefault(); e.stopPropagation(); openRename(); return true; }
    if(c==='KeyN' || k==='n'){ e.preventDefault(); e.stopPropagation(); newSession(); return true; }
    if(c==='KeyK' || k==='k'){ e.preventDefault(); e.stopPropagation(); if(sessions.length>1) control({action:'kill',id:focusedID}); return true; }
    if(c==='KeyP' || k==='p'){ e.preventDefault(); e.stopPropagation(); control({action:'save',id:0}); return true; }
    if(c==='KeyM' || k==='m'){ e.preventDefault(); e.stopPropagation(); toggleMouseMode(); return true; }
    if(c==='Comma' || k===','){ e.preventDefault(); e.stopPropagation(); openSettings(); return true; }
  }
  return false;
}

term.attachCustomKeyEventHandler(e=>{
  if(e.type!=='keydown') return true;
  if(handleAppShortcut(e)) return false;
  return true;
});

// Mouse mode toggle plumbing. xterm.js forwards wheel + click events to the
// PTY whenever the child enables mouse tracking. Some apps (Edge browser key
// combos aside) also intercept the bottom-pixel area for tooltips, making
// regular text selection awkward. 'select' mode swallows the mouse-event
// forwarding so wheel always scrolls the local scrollback and clicks select.
term.attachCustomWheelEventHandler(e=>{
  if(mouseMode==='select'){
    // Scroll local scrollback ourselves; xterm aborts its own handling
    // (which would otherwise forward as a mouse event to the child).
    const lines = e.deltaMode===1 ? e.deltaY : Math.sign(e.deltaY) * 3;
    if(lines) term.scrollLines(lines);
    e.preventDefault();
    return false;
  }
  return true;
});

// In 'select' mode, force xterm's selection service to engage even when the
// child PTY has captured mouse events. xterm's SelectionService only sees a
// mousedown when shouldForceSelection() is true; on non-Mac that means
// shiftKey is held. So we intercept the original event in capture phase and
// re-dispatch a Shift-modified clone at the same point. A WeakSet flag tags
// the synthesized event so we don't recurse on it.
const _synthMouse = new WeakSet();
function forceSelectMouse(e){
  if(mouseMode!=='select') return;
  if(_synthMouse.has(e)) return;
  if(e.button!==0) return;
  e.stopImmediatePropagation();
  e.preventDefault();
  const clone = new MouseEvent(e.type, {
    bubbles:true, cancelable:true, composed:true,
    view:window, detail:e.detail,
    screenX:e.screenX, screenY:e.screenY,
    clientX:e.clientX, clientY:e.clientY,
    ctrlKey:e.ctrlKey, altKey:e.altKey, metaKey:e.metaKey,
    shiftKey:true, button:e.button, buttons:e.buttons,
    relatedTarget:e.relatedTarget,
  });
  _synthMouse.add(clone);
  e.target.dispatchEvent(clone);
}
document.getElementById('terminal').addEventListener('mousedown', forceSelectMouse, {capture:true});

function updateMouseModeUI(){
  const label = document.getElementById('m-mouse-mode');
  if(label) label.textContent = mouseMode;
  const root = document.getElementById('terminal');
  if(root) root.classList.toggle('mouse-select', mouseMode==='select');
  updateStatusBar();
}

function setMouseMode(mode){
  mouseMode = (mode==='select') ? 'select' : 'app';
  localStorage.setItem('multicrum-mouse-mode', mouseMode);
  updateMouseModeUI();
}

function toggleMouseMode(){ setMouseMode(mouseMode==='select' ? 'app' : 'select'); }

updateMouseModeUI();

term.onData(d=>keystroke(d));
window.addEventListener('keydown',e=>{ handleAppShortcut(e); },{capture:true});
window.addEventListener('resize',()=>{fitAndResize();});

function fitAndResize(){
  fitAddon.fit();
  // FitAddon reserves space for xterm's scrollbar, but we hide it with
  // overflow-y:hidden, so an extra column often fits. Bump cols when the
  // leftover width can hold another full cell.
  try {
    const core = term._core;
    const cell = core && core._renderService && core._renderService.dimensions && core._renderService.dimensions.css && core._renderService.dimensions.css.cell;
    const host = document.getElementById('terminal');
    if (cell && cell.width > 0 && host) {
      const style = getComputedStyle(host);
      const padL = parseFloat(style.paddingLeft)||0, padR = parseFloat(style.paddingRight)||0;
      const avail = host.clientWidth - padL - padR;
      const extra = Math.floor((avail - term.cols * cell.width) / cell.width);
      if (extra > 0) term.resize(term.cols + extra, term.rows);
    }
  } catch(e) {}
  updateStatusBar();
  sendResize();
}

function sendResize(){
  const b=new TextEncoder().encode(JSON.stringify({id:focusedID,cols:term.cols,rows:term.rows}));
  const m=new Uint8Array(1+b.length);
  m[0]=0x02; m.set(b,1); send(m);
}

requestAnimationFrame(() => {
  fitAndResize();
  startWebSocket();
  applySettingsToTerminal(initialSettings);
  term.focus();
  // Initial cell metrics may be off before web fonts finish loading; refit
  // once they're ready, and one more frame later for layout to settle.
  if(document.fonts && document.fonts.ready){
    document.fonts.ready.then(() => {
      fitAndResize();
      requestAnimationFrame(() => fitAndResize());
    });
  }
});

</script>
</body>
</html>`, "__WS_QUERY__", wsQuery, 1)
}
