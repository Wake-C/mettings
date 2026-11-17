const test = require('node:test');
const assert = require('node:assert/strict');
const ui = require('./state.js');

test('finishing a recording stays available while ASR is busy or models are missing', () => {
  const state = ui.capture({recording:true,busy:true,seconds:73},'meeting',{ready:false,captureReady:true});
  assert.equal(state.action,'结束并保存');
  assert.equal(state.disabled,false);
  assert.equal(state.elapsedMS,73000);
});
test('desk pause is independent of the transcription queue', () => {
  const state=ui.capture({busy:true,queued:5,desk:{active:true,elapsedMS:5400000,speechMS:600000,clipCount:19}},'meeting',{ready:false});
  assert.equal(state.mode,'desk');assert.equal(state.action,'暂停守候');assert.equal(state.disabled,false);
  assert.equal(state.speechMS,600000);assert.equal(state.clips,19);
});
test('audio-only recording needs capture tools, not an installed ASR model', () => {
  assert.equal(ui.capture({},'meeting',{ready:false,captureReady:true}).disabled,false);
  assert.equal(ui.capture({},'desk',{ready:true,deskReady:false}).disabled,true);
});
test('failed historical attempt remains independently retryable', () => {
  const runs=[{id:'old',createdAt:'2026-10-29T10:00:00Z',state:'failed'},{id:'new',createdAt:'2026-10-29T11:00:00Z',state:'complete'}];
  assert.equal(ui.latestFailed(runs).id,'old');
  assert.equal(ui.orderedRuns(runs)[0].id,'new');
  assert.equal(runs[0].id,'old');
});
test('saved audio is distinct from a failed or pending transcript', () => {
  assert.equal(ui.sessionState({transcribed:false}),'音频已保存');
  assert.equal(ui.sessionState({transcribed:true,captureError:'设备断开'}),'录音有异常');
  assert.equal(ui.sessionState({mode:'desk',active:true}),'讨论进行中');
  assert.equal(ui.duration(3*3600000+9*60000+42000),'3:09:42');
});
test('failed attempt has one selected error surface and a truthful status',()=>{
 const runs=[{id:'bad',model:'FireRed',state:'failed',error:'No speech',createdAt:'2026-10-30T10:00:00Z'}];
 const view=ui.attemptStatus(runs,'bad',false);assert.equal(view.kind,'error');assert.equal(view.text,'FireRed：No speech');assert.equal(ui.transcriptState({transcript:''},runs,'bad').label,'转写失败');
});
test('empty result is neutral and never replaces the visible successful status',()=>{
 const runs=[{id:'empty',model:'FireRed',state:'empty',createdAt:'2026-10-30T10:00:00Z'}];
 assert.equal(ui.attemptStatus(runs,'empty',false).kind,'empty');assert.equal(ui.runLabel(runs[0]),'无文本');
 assert.equal(ui.transcriptState({transcript:'previous result'},runs,'').label,'转写稿可用');
});
test('recording note follows the actual automatic-ASR switch',()=>{
 assert.match(ui.autoASRDescription('meeting',true,''),/自动转写/);
 assert.match(ui.autoASRDescription('meeting',false,''),/仅保存/);
 assert.match(ui.autoASRDescription('desk',true,'FireRed'),/FireRed/);
});
test('deduplication keeps capture and unversioned legacy errors visible',()=>{
 assert.equal(ui.sessionError({session:{error:'same'}},[{error:'same'}]),'');
 assert.equal(ui.sessionError({session:{captureError:'microphone stopped',error:'same'}},[{error:'same'}]),'microphone stopped');
 assert.equal(ui.sessionError({session:{error:'legacy failure'}},[{id:'legacy',state:'complete'}]),'legacy failure');
});

test('Desk transcript renders real dates while meeting timestamps remain supported', () => {
  assert.deepEqual(ui.transcriptLine('**[2026-11-02 11:15:09] Alice**：hello'),{time:'2026-11-02 11:15:09',speaker:'Alice',text:'hello'});
  assert.deepEqual(ui.transcriptLine('**[00:03:15] Alice**：hello'),{time:'00:03:15',speaker:'Alice',text:'hello'});
  assert.equal(ui.transcriptLine('plain text'),null);
  assert.equal(ui.sessionState({pendingSpeakers:33}),'33 段声音待确认');
});

test('speaker groups use backend acoustic membership without merging unknown names', () => {
  const source={speakerGroups:[{id:'a',name:'',memberIDs:['a','b'],representative:'b',count:2},{id:'c',name:'',memberIDs:['c'],representative:'c',count:1}]};
  const groups=ui.speakerGroups(source);
  assert.equal(groups.length,2);assert.deepEqual(groups[0].memberIDs,['a','b']);assert.equal(groups[0].representative,'b');
  groups[0].memberIDs.push('d');assert.deepEqual(source.speakerGroups[0].memberIDs,['a','b']);
});

test('legacy detail collapses recognized names but keeps unknown clips independent', () => {
  const groups=ui.speakerGroups({unidentified:[{spk:'1',name:'Alice',labeled:true},{spk:'2',name:'Alice',labeled:false},{spk:'3'},{spk:'4'}]});
  assert.equal(groups.length,3);assert.deepEqual(groups[0].memberIDs,['1','2']);assert.equal(groups[0].count,2);assert.equal(groups[0].confirmed,false);
  assert.deepEqual(groups[1].memberIDs,['3']);assert.deepEqual(groups[2].memberIDs,['4']);
  assert.deepEqual(ui.speakerGroups({speakerGroups:[],unidentified:[{spk:'old'}]}),[]);
});

test('one-clip name drafts cannot replace a group draft during a refresh', () => {
  const drafts=new Map(), scope='session:run';
  const groupKey=member=>ui.speakerDraftKey(scope,member,'group');
  const clipKey=member=>ui.speakerDraftKey(scope,member,'clip');
  for(const member of ['a','b']) drafts.set(groupKey(member),'Group name');
  drafts.set(clipKey('a'),'Different person');
  assert.equal(drafts.get(groupKey('a')),'Group name');
  assert.equal(drafts.get(clipKey('a')),'Different person');
  // An appended group member does not discard the existing group draft.
  const member=['a','b','c'].find(member=>drafts.has(groupKey(member)));
  assert.equal(drafts.get(groupKey(member)),'Group name');
  drafts.delete(clipKey('a'));
  assert.equal(drafts.get(groupKey('a')),'Group name');
  assert.equal(drafts.has(clipKey('a')),false);
  assert.equal(drafts.has(ui.speakerDraftKey('other-session:run','a','group')),false);
});
