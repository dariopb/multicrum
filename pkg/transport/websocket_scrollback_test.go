package transport

import (
	"encoding/json"
	"testing"
)

func TestWebScrollbackSearchAndTopbar(t *testing.T) {
	search := webFunctionSection(t, "const scrollSearch = ", "\n// Mouse mode toggle plumbing.")
	runWebScript(t, `
const assert = require('node:assert/strict');
const {Terminal} = require('./static/xterm/xterm.js');
const actual = new Terminal({cols:20,rows:4,scrollback:100});
const nodes = new Map();
const document = {getElementById(id){
  if(!nodes.has(id)) nodes.set(id,{
    value:'',hidden:false,textContent:'',validity:'',
    setCustomValidity(value){this.validity=value;},reportValidity(){},
    setAttribute(){},focus(){},addEventListener(){}
  });
  return nodes.get(id);
}};
let modalOpen = false, selected = null;
const terminalTarget = {};
const term = {
  get buffer(){return actual.buffer;},get cols(){return actual.cols;},get rows(){return actual.rows;},
  element:{contains(target){return target===terminalTarget;}},
  select(col,row,length){selected={col,row,length};},clearSelection(){selected=null;},
  scrollToLine(row){actual.scrollToLine(row);},scrollToTop(){actual.scrollToTop();},
  scrollToBottom(){actual.scrollToBottom();},scrollLines(delta){actual.scrollLines(delta);},
  scrollPages(delta){actual.scrollPages(delta);},focus(){},
  onScroll(){},onWriteParsed(){},onResize(){}
};
`+search+`
function key(key,options={}){
  const event = {key,target:terminalTarget,preventDefault(){this.prevented=true;},stopPropagation(){this.stopped=true;},...options};
  const handled = handleScrollbackKey(event);
  if(handled) assert.ok(event.stopped, 'handled navigation leaked to the child terminal');
  return {handled,event};
}
actual.write('zero\r\none\r\n  Needle here\r\nthree\r\nwide \u754c needle\r\nfive\r\nsix\r\nseven\r\neight\r\nnine',()=>{
  assert.equal(key('/').handled,false,'live shell slash must reach PTY');
  assert.equal(key('Home',{ctrlKey:true}).handled,true);
  updateScrollbackStatus();
  assert.equal(document.getElementById('scroll-status').hidden,false);
  assert.equal(document.getElementById('scroll-position').textContent,'4/10');
  assert.equal(key('/').handled,true);
  const input = document.getElementById('scroll-query');
  assert.equal(document.getElementById('scroll-search').hidden,false);
  input.value='needle';
  assert.equal(key('Enter',{target:input}).handled,true);
  assert.deepEqual(selected,{row:2,col:2,length:6});
  assert.equal(document.getElementById('scroll-search').hidden,true,'commit must restore normal topbar');
  assert.equal(scrollSearch.hits.length,2);
  key('n');
  assert.deepEqual(selected,{row:4,col:8,length:6},'wide characters must count terminal cells, not JS string offsets');
  key('N');
  assert.equal(selected.row,2);
  key('/');
  input.value='missing';
  key('Enter',{target:input});
  assert.equal(input.validity,'No matches');
  assert.equal(scrollSearch.open,true,'failed search should keep the query editable');
  key('u',{ctrlKey:true,target:input});
  assert.equal(input.value,'');
  key('Escape',{target:input});
  assert.equal(scrollSearch.open,false);
  key(':');
  assert.equal(document.getElementById('scroll-prefix').textContent,':');
  input.value='bad';
  key('Enter',{target:input});
  assert.equal(input.validity,'Enter a line number');
  input.value='6';
  key('Enter',{target:input});
  assert.equal(actual.buffer.active.viewportY,3);
  assert.equal(key('/',{target:{}}).handled,false,'shortcuts must not hijack other inputs');
  modalOpen=true;
  assert.equal(key('/').handled,false);
  modalOpen=false;
  key('End',{ctrlKey:true});
  assert.equal(document.getElementById('scroll-status').hidden,true);
  resetScrollbackUI();
  assert.equal(scrollSearch.query,'');
  assert.deepEqual(scrollSearch.hits,[]);
  actual.dispose();
});
`)
}

func TestWebSelectionUsesNativeDragAndColumnGates(t *testing.T) {
	gates := webFunctionSection(t, "const selectionService = ", "\nlet pinchStartDistance")
	press := webFunctionSection(t, "function forceSelectMouse(e){", "\nfunction fallbackCopyText(")
	wheel := webFunctionSection(t, "term.attachCustomWheelEventHandler(e=>{", "\n// The pinned xterm selection service")
	runWebScript(t, `
const assert = require('node:assert/strict');
let mouseMode='select',modalOpen=false,rectangularSelection=false,selectionDrag=null;
const events=[], listeners={};
const document={
  getElementById(){return {addEventListener(type,fn){listeners[type]=fn;}};},
  dispatchEvent(event){events.push(event);}
};
class MouseEvent {constructor(type,options){this.type=type;Object.assign(this,options);}}
const term={
  options:{altClickMovesCursor:true},
  _core:{_selectionService:{
    shouldForceSelection(e){return !!e.shiftKey;},
    shouldColumnSelect(e){return !!e.altKey;}
  }},
  scrollLines(lines){events.push({type:'scroll',lines});},
  attachCustomWheelEventHandler(fn){this.wheel=fn;}
};
`+gates+press+wheel+`
assert.equal(selectionService.shouldForceSelection({}),true,'selection must work while child mouse reporting is enabled');
assert.equal(selectionService.shouldColumnSelect({ctrlKey:true,altKey:true}),true);
assert.equal(!!selectionService.shouldColumnSelect({altKey:true}),false,'only Ctrl+Alt starts a block in select mode');
forceSelectMouse({button:0,ctrlKey:true,altKey:true,clientX:12,clientY:18});
assert.equal(rectangularSelection,true);
assert.ok(selectionDrag);
const wheel={deltaMode:0,deltaY:-50,clientX:14,clientY:20,preventDefault(){this.prevented=true;}};
assert.equal(term.wheel(wheel),false);
assert.deepEqual(events.map(e=>e.type),['scroll','mousemove'],'wheel must extend the existing native drag after scrolling');
assert.equal(events[0].lines,-3);
assert.equal(events[1].clientY,20);
assert.equal(events[1].buttons,1);
mouseMode='app';
assert.equal(selectionService.shouldForceSelection({}),false);
assert.equal(selectionService.shouldForceSelection({shiftKey:true}),true);
assert.equal(selectionService.shouldColumnSelect({altKey:true}),true,'app-mode native gestures must stay intact');
assert.equal(term.wheel(wheel),true,'app-mode wheel must be forwarded by xterm');
`)
}

func TestWebBlockCopyPreservesBreaksAndLinearWhitespace(t *testing.T) {
	copy := webFunctionSection(t, "function terminalSelectionText(){", "\nfunction updateMouseModeUI(){")
	runWebScript(t, `
const assert = require('node:assert/strict');
let rectangularSelection=true,selectionDrag={},modalOpen=false,mouseMode='select';
let serverSettings={copyOnRelease:true};
let selected='  ab   \r\n     \r\n c d  \r\n';
let bottom=0;
const copied=[],microtasks=[],events={};
const term={
  getSelection(){return selected;},clearSelection(){selected='';},
  hasSelection(){return selected!=='';},scrollToBottom(){bottom++;}
};
const navigator={clipboard:{writeText(text){copied.push(text);return Promise.resolve();}}};
function fallbackCopyText(){throw new Error('unexpected fallback');}
function queueMicrotask(fn){microtasks.push(fn);}
const window={addEventListener(type,fn){events[type]=fn;}};
const document={getElementById(){return {addEventListener(type,fn){events[type]=fn;}};}};
`+copy+`
const block='  ab\r\n\r\n c d\r\n';
assert.equal(terminalSelectionText(),block);
rectangularSelection=false;
assert.equal(terminalSelectionText(),selected,'normal selection must not be trimmed');
rectangularSelection=true;
copyTerminalSelectionOnRelease({button:0});
assert.equal(copied.length,0,'release must wait for xterm to finish the drag');
microtasks.shift()();
assert.deepEqual(copied,[block]);
assert.equal(bottom,1);
selected='retained   \n';
selectionDrag={};
serverSettings.copyOnRelease=false;
copyTerminalSelectionOnRelease({button:0});
assert.equal(selected,'retained   \n');
assert.equal(microtasks.length,0);
events.contextmenu({preventDefault(){},stopPropagation(){}});
assert.equal(copied[1],'retained\n');
selected='keyboard copy   \r\n';
let clipboard='';
events.copy({preventDefault(){},stopImmediatePropagation(){},clipboardData:{setData(type,text){clipboard=text;}}});
assert.equal(clipboard,'keyboard copy\r\n');
serverSettings.copyOnRelease=true;
selectionDrag=null;
copyTerminalSelectionOnRelease({button:0});
assert.equal(microtasks.length,0,'clicking the search input must not copy a highlighted search match');
`)
}

func TestWebInlineScriptsParse(t *testing.T) {
	html, err := json.Marshal(indexHTML(""))
	if err != nil {
		t.Fatal(err)
	}
	runWebScript(t, `
const vm=require('node:vm');
const html=`+string(html)+`;
for(const match of html.matchAll(/<script>([\s\S]*?)<\/script>/g)) new vm.Script(match[1]);
`)
}
