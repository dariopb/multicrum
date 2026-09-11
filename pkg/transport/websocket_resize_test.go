package transport

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func webFunctionSection(t *testing.T, start, end string) string {
	t.Helper()
	html := indexHTML("")
	_, rest, ok := strings.Cut(html, start)
	if !ok {
		t.Fatalf("missing web function %q", start)
	}
	body, _, ok := strings.Cut(rest, end)
	if !ok {
		t.Fatalf("missing web function boundary %q", end)
	}
	return start + body
}

func runWebScript(t *testing.T, script string) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "-")
	cmd.Stdin = strings.NewReader(script)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("web regression failed: %v\n%s", err, output)
	}
}

func TestWebFitDoesNotTemporarilyShrinkTerminal(t *testing.T) {
	fit := webFunctionSection(t, "function fitAndResize(){", "\nfunction sendResize(){")
	runWebScript(t, `
const assert = require('node:assert/strict');
const {Terminal} = require('./static/xterm/xterm.js');
const actual = new Terminal({cols:81,rows:6});
const resizes = [];
const term = {
  cols:81, rows:6,
  _core:{_renderService:{dimensions:{css:{cell:{width:10}}}}},
  resize(cols, rows){
    resizes.push([cols, rows]);
    actual.resize(cols, rows);
    this.cols = cols; this.rows = rows;
  }
};
let proposed = {cols:80,rows:6};
const fitAddon = {
  proposeDimensions(){ return proposed; },
  fit(){ if(term.cols!==proposed.cols || term.rows!==proposed.rows) term.resize(proposed.cols, proposed.rows); }
};
const host = {clientWidth:816};
const document = {getElementById(){ return host; }};
function getComputedStyle(){ return {paddingLeft:'6px',paddingRight:'0px'}; }
const sent = [];
function sendResize(){ sent.push([term.cols,term.rows]); }
`+fit+`
actual.write('\x1b[6;1H'+'x'.repeat(81), () => {
  const before = actual.buffer.active.getLine(5).translateToString(true);
  fitAndResize();
  fitAndResize();
  assert.deepEqual(resizes, [], 'unchanged fit must not narrow and then widen the terminal');
  assert.equal(actual.buffer.active.getLine(5).translateToString(true), before, 'fit damaged the footer');
  assert.deepEqual(sent, [[81,6],[81,6]], 'explicit refit must still send the authoritative size');
  host.clientWidth = 716;
  proposed = {cols:70,rows:6};
  fitAndResize();
  assert.deepEqual(resizes, [[71,6]], 'a real fit should resize directly to its final size once');
  actual.dispose();
});
`)
}

func TestWebConnectionSizesActualFocusedSession(t *testing.T) {
	socket := webFunctionSection(t, "function startWebSocket(){", "\nfunction updateLabel(){")
	resize := webFunctionSection(t, "function sendResize(){", "\nrequestAnimationFrame(")
	focus := webFunctionSection(t, "function focusSession(id){", "\nfunction switchSession(")
	runWebScript(t, `
const assert = require('node:assert/strict');
class WebSocket {
  static OPEN = 1;
  static CONNECTING = 0;
  constructor(){ this.readyState = 0; }
}
let ws, reconnectTimer = null, connected = false, awaitingInitialMeta = true;
let sessions = [], connections = [], activeConnection = '', serverName = '', focusedID = 0;
let weInitiatedSwitch = false, modalOpen = false, modalMode = '', serverSettings = {};
const location = {protocol:'http:',host:'test'};
let resets = 0, writerResets = 0;
const term = {cols:81,rows:24,reset(){ resets++; },focus(){}};
const terminalWriter = {reset(){ writerResets++; },write(){}};
const resizes = [], controls = [];
function send(data){ resizes.push(JSON.parse(new TextDecoder().decode(data.slice(1)))); }
function control(data){ controls.push(data); }
function setConnectionState(state){ connected = state === 'connected'; }
function scheduleReconnect(){}
function updateLabel(){}
function syncAppSettingsForm(){}
`+socket+resize+focus+`
function open(){
  startWebSocket();
  ws.readyState = WebSocket.OPEN;
  ws.onopen();
}
function metadata(value){
  const json = new TextEncoder().encode(JSON.stringify(value));
  const data = new Uint8Array(json.length+1);
  data[0] = 2; data.set(json,1);
  ws.onmessage({data:data.buffer});
}
open();
sendResize();
assert.deepEqual(resizes, [], 'must not resize stale session 0 before metadata');
metadata({activeConnection:'first',focusedId:0,sessions:[]});
assert.deepEqual(resizes, [], 'must wait until the focused session exists');
metadata({activeConnection:'first',focusedId:3,sessions:[{id:3},{id:4}]});
assert.deepEqual(resizes, [{id:3,cols:81,rows:24}]);
assert.equal(resets, 1);
metadata({activeConnection:'first',focusedId:3,sessions:[{id:3},{id:4}]});
metadata({activeConnection:'first',focusedId:4,sessions:[{id:3},{id:4}]});
assert.equal(resizes.length, 1, 'passive metadata adoption must not override the initiating viewer');
weInitiatedSwitch = true;
metadata({activeConnection:'second',focusedId:2,sessions:[{id:2}]});
assert.equal(resizes[1].id, 2, 'connection switch initiator must resize its target');
ws.readyState = 3;
ws.onclose();
open();
sendResize();
assert.equal(resizes.length, 2);
metadata({activeConnection:'second',focusedId:2,sessions:[{id:2}]});
assert.equal(resizes[2].id, 2, 'reconnect to the same session must still claim its size');
const previousResets = resets;
focusSession(3);
assert.equal(resizes[3].id, 3);
assert.equal(resets, previousResets+1, 'explicit focus must reset old terminal modes, not just clear rows');
assert.equal(writerResets, resets);
ws.readyState = 3;
ws.onclose();
open();
metadata([{id:7}]);
assert.equal(resizes[4].id, 7, 'legacy array metadata must also resolve a valid initial session');
`)
}

func TestWebWriterResetDiscardsPartialUTF8(t *testing.T) {
	writer := webFunctionSection(t, "const terminalWriter = (() => {", "\nfunction scheduleReconnect(){")
	runWebScript(t, `
const assert = require('node:assert/strict');
const written = [], frames = [];
const term = {write(text){ written.push(text); }};
function requestAnimationFrame(callback){ frames.push(callback); }
`+writer+`
terminalWriter.write(new TextEncoder().encode('old session'));
terminalWriter.write(new Uint8Array([0xe2,0x97]));
terminalWriter.reset();
terminalWriter.write(new TextEncoder().encode('new session'));
for(const flush of frames) flush();
assert.equal(written.join(''), 'new session', 'old queued bytes or partial UTF-8 leaked across session reset');
`)
}
