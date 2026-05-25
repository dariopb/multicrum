package transport

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// SessionInfo is sent to browsers to render the tab bar.
type SessionInfo struct {
	ID     int    `json:"id"`
	Title  string `json:"title"`
	Exited bool   `json:"exited"`
}

type MetaMsg struct {
	FocusedID int           `json:"focusedId"`
	Sessions  []SessionInfo `json:"sessions"`
}

// ControlMsg is received from the browser for session management.
type ControlMsg struct {
	Action   string `json:"action"` // "focus" | "new" | "kill" | "rename" | "exit"
	ID       int    `json:"id"`
	Title    string `json:"title,omitempty"`
	Mode     string `json:"mode,omitempty"`     // new: "same" | "local" | "remote"
	Cmd      string `json:"cmd,omitempty"`      // new local command or remote command
	Target   string `json:"target,omitempty"`   // new remote SSH target
	Password string `json:"password,omitempty"` // new remote SSH password
	Key      string `json:"key,omitempty"`      // new remote SSH identity file
	Choice   string `json:"choice,omitempty"`   // exit: "respawn" | "remove"
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
	OnInput   func(sessionID int, data []byte) // keystroke from browser
	OnControl func(msg ControlMsg)             // session management from browser
	OnResize  func(msg ResizeMsg)              // terminal resize from browser
	SnapOf    func(sessionID int) []byte       // raw PTY snapshot for new clients
	Sessions  func() []SessionInfo             // current session list
	FocusedID func() int                       // currently focused session
}

// NewWSTransport starts listening on addr.
func NewWSTransport(addr, token string) (*WSTransport, error) {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	t := &WSTransport{token: token, echo: e}
	e.GET("/ws", t.handleWS)
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
	payload, _ := json.Marshal(MetaMsg{FocusedID: focusedID, Sessions: sessions})
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
<title>multicrum</title>
<link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/xterm@5/css/xterm.css"/>
<script src="https://cdn.jsdelivr.net/npm/xterm@5/lib/xterm.js"></script>
<script src="https://cdn.jsdelivr.net/npm/xterm-addon-fit@0.8/lib/xterm-addon-fit.js"></script>
<style>
*{box-sizing:border-box;margin:0;padding:0}
body{display:flex;flex-direction:column;height:100vh;background:#17121f;font-family:monospace;color:#e8dff5}
#tabbar{display:flex;align-items:center;background:linear-gradient(90deg,#2a1042,#4b176a 45%,#7b1f72);border-bottom:1px solid #d946ef55;box-shadow:0 4px 18px #0008;height:38px;padding:0 10px;gap:7px;flex-shrink:0}
#session-label{display:inline-flex;align-items:center;height:27px;font-size:12px;color:#f5d0fe;background:#1f102fcc;border:1px solid #e879f955;border-radius:7px;padding:0 11px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;max-width:260px;box-shadow:inset 0 0 12px #a855f733}
#connection-state{display:inline-flex;align-items:center;height:27px;font-size:12px;border-radius:7px;padding:0 9px;border:1px solid #facc1555;background:#422006aa;color:#fde68a;white-space:nowrap}
#connection-state.connected{border-color:#86efac66;background:#14532daa;color:#bbf7d0}
#connection-state.disconnected{border-color:#fb718566;background:#7f1d1daa;color:#fecaca}
.btn{display:inline-flex;align-items:center;height:27px;padding:0 12px;border-radius:7px;cursor:pointer;font-size:12px;border:1px solid #f0abfc55;background:#3b1b52cc;color:#f5d0fe;box-shadow:0 1px 8px #0004;transition:background .12s,border-color .12s,transform .12s}
.btn:hover{background:#5b247acc;border-color:#f0abfcaa;transform:translateY(-1px)}
.btn:disabled{opacity:.45;cursor:not-allowed;transform:none}
.btn-green{background:#4a1f5fcc;border-color:#f0abfc66;color:#f5d0fe}.btn-green:hover{background:#6d2c82cc}
.btn-red{background:#5b1f46cc;border-color:#fb718566;color:#fecdd3}.btn-red:hover{background:#7f1d5fcc}
.btn-blue{background:#3b1f6bcc;border-color:#c084fc77;color:#e9d5ff}.btn-blue:hover{background:#5b21b6cc}
#hint{margin-left:auto;font-size:11px;color:#f5d0fe99}
#terminal{flex:1;overflow:hidden;padding:2px}
/* Modal */
#modal-overlay{display:none;position:fixed;inset:0;background:rgba(0,0,0,.6);z-index:100;align-items:center;justify-content:center}
#modal-overlay.open{display:flex}
#modal{background:#252525;border:1px solid #555;border-radius:8px;padding:16px;min-width:320px;max-width:500px;width:90%;box-shadow:0 8px 32px #000a}
#modal h2{font-size:13px;color:#aaa;margin-bottom:10px;font-weight:normal;text-transform:uppercase;letter-spacing:.08em}
#session-filter,#rename-input,.new-input{width:100%;background:#1b1b1b;border:1px solid #444;border-radius:5px;color:#ddd;padding:7px 9px;margin-bottom:10px;font-family:inherit;font-size:13px}
.choice-row{display:block;padding:7px 9px;margin:4px 0;border:1px solid transparent;border-radius:5px;color:#ddd;cursor:pointer}
.choice-row.selected{background:#0d3a6a;border-color:#2a6aaa;color:#fff}
.choice-row:focus-within{outline:1px dashed #6af;outline-offset:2px}
.choice-row input{margin-right:8px}
#exit-form{display:flex;gap:10px;justify-content:center}
#exit-form .btn.selected{outline:2px solid #6af;border-color:#9cf}
#exit-form .btn:focus:not(.selected){outline:1px dashed #6af;outline-offset:2px}
#session-list{display:flex;flex-direction:column;gap:4px;max-height:320px;overflow-y:auto}
.sess-item{display:flex;align-items:center;gap:8px;padding:7px 10px;border-radius:5px;cursor:pointer;border:1px solid transparent}
.sess-item:hover{background:#333;border-color:#555}
.sess-item.active{background:#0d3a6a;border-color:#2a6aaa}
.sess-item.exited{opacity:.5}
.sess-num{font-size:11px;color:#666;min-width:18px;text-align:right}
.sess-title{flex:1;font-size:13px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.sess-badge{font-size:10px;padding:1px 5px;border-radius:3px;background:#333;color:#888}
.sess-badge.running{background:#1a3a1a;color:#6f6}
.sess-badge.exited{background:#3a1a1a;color:#f66}
#modal-footer{margin-top:12px;font-size:11px;color:#555;text-align:center}
</style>
</head>
<body>
<div id="tabbar">
  <button id="btn-sessions" class="btn btn-blue" title="Switch session (Ctrl+Alt+S)">☰ Sessions</button>
  <span id="session-label">—</span>
  <span id="connection-state">connecting</span>
  <button id="btn-new" class="btn btn-green" title="New session (Ctrl+Alt+T)">+ New</button>
  <button id="btn-kill" class="btn btn-red" title="Kill session (Ctrl+Alt+W)">✕ Kill</button>
  <span id="hint">Ctrl+←/→ switch &nbsp; Ctrl+Alt+S sessions &nbsp; Ctrl+Alt+R rename &nbsp; Ctrl+Alt+T new &nbsp; Ctrl+Alt+W kill</span>
</div>
<div id="terminal"></div>

<div id="modal-overlay">
  <div id="modal">
    <h2 id="modal-title">Sessions</h2>
    <input id="session-filter" placeholder="Filter sessions..." autocomplete="off"/>
    <input id="rename-input" placeholder="Session name" autocomplete="off" style="display:none"/>
    <div id="new-session-form" style="display:none">
      <label class="choice-row selected"><input type="radio" name="new-mode" value="same" checked> Same as current/default</label>
      <label class="choice-row"><input type="radio" name="new-mode" value="local"> Local command</label>
      <input id="new-local-cmd" class="new-input" placeholder="Local command (optional)" autocomplete="off"/>
      <label class="choice-row"><input type="radio" name="new-mode" value="remote"> Remote SSH</label>
      <input id="new-ssh-target" class="new-input" placeholder="user@host[:port]" autocomplete="off"/>
      <input id="new-ssh-passwd" class="new-input" placeholder="Password (optional)" autocomplete="off" type="password"/>
      <input id="new-ssh-key" class="new-input" placeholder="Key file (optional)" autocomplete="off"/>
      <input id="new-remote-cmd" class="new-input" placeholder="Remote command (optional)" autocomplete="off"/>
    </div>
    <div id="exit-form" style="display:none">
      <button id="exit-respawn" class="btn btn-green selected">Respawn</button>
      <button id="exit-remove" class="btn btn-red">Remove</button>
    </div>
    <div id="session-list"></div>
    <div id="modal-footer">Type to filter &nbsp; ↑↓ navigate &nbsp; Enter select &nbsp; Esc close</div>
  </div>
</div>

<script>
const term = new Terminal({cursorBlink:true,fontSize:14,fontFamily:'Cascadia Code,Consolas,monospace'});
const fitAddon = new FitAddon.FitAddon();
term.loadAddon(fitAddon);
term.open(document.getElementById('terminal'));

// Size xterm before opening the WebSocket so incoming snapshot bytes are
// written to a properly-dimensioned terminal, not a 0×0 one.
requestAnimationFrame(() => {
fitAddon.fit();

const ws = new WebSocket('ws://'+location.host+'/ws__WS_QUERY__');
ws.binaryType = 'arraybuffer';

let connected = false;
let sessions = [];
let filteredSessions = [];
let focusedID = 0;
let modalOpen = false;
let modalMode = 'sessions';
let modalCursor = 0;
let newChoice = 0;
let exitChoice = 0;
const newModes = ['same','local','remote'];

function setConnectionState(state){
  const el = document.getElementById('connection-state');
  el.className = state;
  el.textContent = state;
  connected = state === 'connected';
  document.querySelectorAll('.btn').forEach(b=>b.disabled=!connected);
}

function send(data){ if(ws.readyState===1) ws.send(data); }
function control(obj){ const b=new TextEncoder().encode(JSON.stringify(obj)); const m=new Uint8Array(1+b.length); m[0]=0x01; m.set(b,1); send(m); }
function keystroke(str){ const b=new TextEncoder().encode(str); const m=new Uint8Array(1+b.length); m[0]=0x00; m.set(b,1); send(m); }

ws.onopen = () => { setConnectionState('connected'); sendResize(); };
ws.onclose = () => { setConnectionState('disconnected'); };
ws.onerror = () => { setConnectionState('disconnected'); };
setConnectionState('connecting');

ws.onmessage = e => {
  const buf = new Uint8Array(e.data);
  if(buf[0]===0x01){ term.write(buf.slice(1)); }
  else if(buf[0]===0x02){
    const meta = JSON.parse(new TextDecoder().decode(buf.slice(1)));
    if(Array.isArray(meta)){
      sessions = meta;
    } else {
      sessions = meta.sessions || [];
      if(typeof meta.focusedId === 'number' && focusedID !== meta.focusedId){
        focusedID = meta.focusedId;
        term.clear();
        control({action:'focus',id:focusedID});
        sendResize();
      }
    }
    updateLabel();
    if(modalOpen && modalMode === 'sessions') renderModal();
  }
};

function updateLabel(){
  const s = sessions.find(s=>s.id===focusedID);
  document.getElementById('session-label').textContent = s ? '['+(s.id+1)+'] '+s.title : '—';
  if(s && s.exited && !modalOpen) openExitModal(s.id);
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

function newSession(){ openNewSession(); }

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
  control({
    action:'new',
    id:0,
    mode,
    cmd: mode === 'local' ? document.getElementById('new-local-cmd').value : document.getElementById('new-remote-cmd').value,
    target: document.getElementById('new-ssh-target').value,
    password: document.getElementById('new-ssh-passwd').value,
    key: document.getElementById('new-ssh-key').value,
  });
  term.clear();
  closeModal();
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
  document.getElementById('modal-footer').textContent = 'Enter start   Esc cancel   ↑/↓ choose   Tab fields';
  setNewChoice(0);
  document.getElementById('modal-overlay').classList.add('open');
  document.querySelector('input[name="new-mode"][value="same"]').focus();
}

// ── Modal ────────────────────────────────────────────────────────────────────

function openModal(){
  modalOpen = true;
  modalMode = 'sessions';
  document.getElementById('modal-title').textContent = 'Sessions';
  document.getElementById('session-filter').style.display = '';
  document.getElementById('rename-input').style.display = 'none';
  document.getElementById('new-session-form').style.display = 'none';
  document.getElementById('exit-form').style.display = 'none';
  document.getElementById('session-list').style.display = '';
  document.getElementById('session-filter').value = '';
  filteredSessions = sessions;
  modalCursor = Math.max(0, sessions.findIndex(s=>s.id===focusedID));
  renderModal();
  document.getElementById('modal-overlay').classList.add('open');
  document.getElementById('session-filter').focus();
}

function openRename(){
  const s = sessions.find(s=>s.id===focusedID);
  modalOpen = true;
  modalMode = 'rename';
  document.getElementById('modal-title').textContent = 'Rename Session';
  document.getElementById('session-filter').style.display = 'none';
  document.getElementById('rename-input').style.display = '';
  document.getElementById('new-session-form').style.display = 'none';
  document.getElementById('exit-form').style.display = 'none';
  document.getElementById('session-list').style.display = '';
  document.getElementById('session-list').innerHTML = '';
  document.getElementById('modal-footer').textContent = 'Enter save   Esc cancel';
  document.getElementById('rename-input').value = s ? s.title : '';
  document.getElementById('modal-overlay').classList.add('open');
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
  document.getElementById('exit-form').style.display = '';
  document.getElementById('modal-footer').textContent = 'Enter confirm   ←/→ choose   Esc close';
  setExitChoice(0);
  document.getElementById('modal-overlay').classList.add('open');
  document.getElementById('exit-respawn').focus();
}

function closeModal(){
  modalOpen = false;
  document.getElementById('modal-overlay').classList.remove('open');
  document.getElementById('modal-footer').textContent = 'Type to filter   ↑↓ navigate   Enter select   Esc close';
  term.focus();
}

function renderModal(){
  if(modalMode !== 'sessions') return;
  const el = document.getElementById('session-list');
  el.innerHTML = '';
  const q = document.getElementById('session-filter').value.trim().toLowerCase();
  filteredSessions = sessions.filter(s => !q || ((s.title||'')+' '+(s.id+1)).toLowerCase().includes(q));
  if(modalCursor < 0) modalCursor = 0;
  if(modalCursor >= filteredSessions.length) modalCursor = Math.max(0, filteredSessions.length-1);
  filteredSessions.forEach((s, i) => {
    const row = document.createElement('div');
    row.className = 'sess-item' + (s.id===focusedID?' active':'') + (s.exited?' exited':'');
    if(i === modalCursor) row.style.outline = '1px solid #6af';
    row.innerHTML =
      '<span class="sess-num">'+(s.id+1)+'</span>'+
      '<span class="sess-title">'+escHtml(s.title||('Session '+(s.id+1)))+'</span>'+
      '<span class="sess-badge '+(s.exited?'exited':'running')+'">'+(s.exited?'exited':'running')+'</span>';
    row.onclick = () => { focusSession(s.id); closeModal(); };
    el.appendChild(row);
  });
  // Scroll cursor into view
  const rows = el.querySelectorAll('.sess-item');
  if(rows[modalCursor]) rows[modalCursor].scrollIntoView({block:'nearest'});
}

function escHtml(s){ return s.replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;'); }

// Keyboard nav inside modal
document.getElementById('modal-overlay').addEventListener('keydown', e => {
  if(!modalOpen) return;
  if(e.key==='Escape'){ e.preventDefault(); closeModal(); return; }
  if(modalMode === 'rename'){
    if(e.key==='Enter'){
      e.preventDefault();
      control({action:'rename',id:focusedID,title:document.getElementById('rename-input').value});
      closeModal();
    }
    return;
  }
  if(modalMode === 'new'){
    if(e.key==='ArrowDown' || e.key==='ArrowRight'){ e.preventDefault(); setNewChoice(newChoice+1); return; }
    if(e.key==='ArrowUp' || e.key==='ArrowLeft'){ e.preventDefault(); setNewChoice(newChoice-1); return; }
    if(e.key==='Tab'){ return; }
    if(e.key==='Enter'){ e.preventDefault(); submitNewSession(); return; }
    return;
  }
  if(modalMode === 'exit'){
    if(e.key==='ArrowRight' || e.key==='Tab'){ e.preventDefault(); setExitChoice(1-exitChoice); return; }
    if(e.key==='ArrowLeft'){ e.preventDefault(); setExitChoice(1-exitChoice); return; }
    if(e.key==='Enter'){ e.preventDefault(); control({action:'exit',id:focusedID,choice:exitChoice===0?'respawn':'remove'}); closeModal(); return; }
    return;
  }
  if(e.key==='ArrowDown' && filteredSessions.length){ e.preventDefault(); modalCursor=(modalCursor+1)%filteredSessions.length; renderModal(); return; }
  if(e.key==='ArrowUp' && filteredSessions.length){ e.preventDefault(); modalCursor=(modalCursor-1+filteredSessions.length)%filteredSessions.length; renderModal(); return; }
  if(e.key==='Enter'){ e.preventDefault(); if(filteredSessions[modalCursor]){ focusSession(filteredSessions[modalCursor].id); closeModal(); } return; }
});

document.getElementById('session-filter').addEventListener('input', () => { modalCursor = 0; renderModal(); });

// Close on backdrop click
document.getElementById('modal-overlay').addEventListener('click', e => {
  if(e.target === document.getElementById('modal-overlay')) closeModal();
});

document.querySelectorAll('input[name="new-mode"]').forEach((el,i)=>el.onchange=()=>setNewChoice(i));
document.getElementById('exit-respawn').onclick = () => { setExitChoice(0); control({action:'exit',id:focusedID,choice:'respawn'}); closeModal(); };
document.getElementById('exit-remove').onclick = () => { setExitChoice(1); control({action:'exit',id:focusedID,choice:'remove'}); closeModal(); };
document.getElementById('btn-sessions').onclick = () => { openModal(); };
document.getElementById('btn-new').onclick = ()=>{ newSession(); };
document.getElementById('btn-kill').onclick = ()=>{ if(connected && sessions.length>1){ control({action:'kill',id:focusedID}); } term.focus(); };
const renameBtn = document.createElement('button');
renameBtn.className = 'btn';
renameBtn.textContent = 'Rename';
renameBtn.onclick = () => { openRename(); };
document.getElementById('btn-kill').after(renameBtn);

// Intercept browser shortcuts before they're swallowed.
function handleAppShortcut(e){
  if(e.ctrlKey && !e.shiftKey && e.key==='ArrowLeft'){ e.preventDefault(); e.stopPropagation(); switchSession(-1); return true; }
  if(e.ctrlKey && !e.shiftKey && e.key==='ArrowRight'){ e.preventDefault(); e.stopPropagation(); switchSession(1); return true; }
  if(e.ctrlKey && e.altKey && !e.shiftKey){
    const k = e.key.toLowerCase();
    if(k==='s'){ e.preventDefault(); e.stopPropagation(); openModal(); return true; }
    if(k==='r'){ e.preventDefault(); e.stopPropagation(); openRename(); return true; }
    if(k==='t'){ e.preventDefault(); e.stopPropagation(); newSession(); return true; }
    if(k==='w'){ e.preventDefault(); e.stopPropagation(); if(sessions.length>1) control({action:'kill',id:focusedID}); return true; }
  }
  return false;
}

term.attachCustomKeyEventHandler(e=>{
  if(e.type!=='keydown') return true;
  if(handleAppShortcut(e)) return false;
  return true;
});

term.onData(d=>keystroke(d));
window.addEventListener('keydown',e=>{ handleAppShortcut(e); },{capture:true});
window.addEventListener('resize',()=>{fitAddon.fit(); sendResize();});

function sendResize(){
  const b=new TextEncoder().encode(JSON.stringify({id:focusedID,cols:term.cols,rows:term.rows}));
  const m=new Uint8Array(1+b.length);
  m[0]=0x02; m.set(b,1); send(m);
}

term.focus();
}); // end requestAnimationFrame
</script>
</body>
</html>`, "__WS_QUERY__", wsQuery, 1)
}
