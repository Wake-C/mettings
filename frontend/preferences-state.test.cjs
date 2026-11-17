const test = require('node:test');
const assert = require('node:assert/strict');
const state = require('./preferences-state.js');
test('recommended preset preserves user data and stored service profiles', () => {
 const original = {root:'/archive',device:'2',localModel:'sensevoice',meetingAutoASR:true,deskAutoASR:true,voiceThreshold:.8,hotwords:'私有词',corrections:'错=对',aliases:'{"昵称":"姓名"}',titleEndpoint:'https://title.example/v1',titleModel:'title-model',asr:{kind:'compatible',endpoint:'https://asr.example/v1',model:'custom',responseFormat:'json'}};
 const result=state.recommended(original);
 assert.equal(result.localModel,'qwen3');assert.equal(result.device,'default');assert.equal(result.asr.kind,'local');assert.equal(result.meetingAutoASR,false);assert.equal(result.deskAutoASR,false);assert.equal(result.voiceThreshold,.6);
 for(const key of ['root','hotwords','corrections','aliases','titleEndpoint','titleModel'])assert.equal(result[key],original[key]);
 assert.equal(result.asr.endpoint,original.asr.endpoint);assert.equal(result.asr.model,'custom');assert.equal(original.asr.kind,'compatible');
});
test('plain-language aliases round-trip without JSON editing', () => {
 const aliases={'小王':'王明','PM':'产品经理'};
 assert.deepEqual(JSON.parse(state.aliasesFromText(state.aliasesToText(JSON.stringify(aliases)))),aliases);
 assert.equal(state.aliasesFromText(''),'{}');
 assert.deepEqual(JSON.parse(state.aliasesFromText('# names\n小王=王明\nPM=产品经理')),aliases);
});
test('existing uncommon alias keys and whitespace remain intact', () => {
 const aliases={'a=b':' A ','':'empty key',' line\n':'name'};
 assert.deepEqual(JSON.parse(state.aliasesFromText(state.aliasesToText(JSON.stringify(aliases)))),aliases);
});
test('invalid aliases give an actionable line number', () => {
 assert.throws(()=>state.aliasesFromText('小王=王明\n未完成'),/第 2 行/);
 assert.throws(()=>state.aliasesFromText('{"nickname":42}'),/文字/);
});
test('saving Basic changes preserves newer choices from the recording window',()=>{
 const before={root:'/old',meetingAutoASR:false,deskAutoASR:false,aliases:'{}',asr:{kind:'local',endpoint:'https://old.example',model:'old'}};
 const edited={...before,root:'/new'};
 const latest={...before,meetingAutoASR:true,deskAutoASR:true,aliases:'{"new":"name"}',asr:{...before.asr,endpoint:'https://new.example',model:'new'}};
 const result=state.mergeChanges(before,edited,latest);
 assert.equal(result.root,'/new');assert.equal(result.meetingAutoASR,true);assert.equal(result.deskAutoASR,true);assert.equal(result.aliases,latest.aliases);assert.deepEqual(result.asr,latest.asr);
});
test('edited Advanced field wins without resetting other provider fields',()=>{
 const before={asr:{kind:'compatible',model:'a',endpoint:'https://service.example'}};
 const edited={asr:{...before.asr,model:'b'}};
 const latest={asr:{...before.asr,endpoint:'https://new.example'}};
 assert.deepEqual(state.mergeChanges(before,edited,latest).asr,{kind:'compatible',model:'b',endpoint:'https://new.example'});
});
