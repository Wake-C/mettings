const $ = selector => document.querySelector(selector);
const call = (method, ...args) => window.mygo.call(`Desktop.${method}`, ...args);
const ui = window.MettingsUI;
const node = (tag, className, text) => {
  const element = document.createElement(tag);
  if (className) element.className = className;
  if (text !== undefined) element.textContent = text;
  return element;
};
const isSettings = new URLSearchParams(location.search).get('view') === 'settings';
let setup = null, snapshot = null, settings = null, choices = [];
let requestedMode = 'meeting', capturePending = false, optionPending = false, transcribePending = false;
let titlePending = false;
let selected = '', selectedVersion = '', detail = null, versions = [];
let pendingOnly = false, viewMode = 'archive', folderLoaded = false, folderPath = '', archiveRoot = '';
let detailFingerprint = '', listFingerprint = '', versionFingerprint = '', choiceFingerprint = '', request = 0, folderRequest = 0;
const speakerDrafts = new Map();
const speakerOpen = new Set(), speakerLimits = new Map(), speakerBusy = new Set();

function failure(error) {
  $('#error span').textContent = String(error?.message || error);
  $('#error').hidden = false;
}
async function act(fn) { try { return await fn(); } catch (error) { failure(error); } }
$('#error button').onclick = () => $('#error').hidden = true;
function dateLabel(value) {
  if (!value) return '';
  const date = new Date(value);
  return Number.isNaN(date.valueOf()) ? value : date.toLocaleString('zh-CN', {month: 'long', day: 'numeric', hour: '2-digit', minute: '2-digit'});
}
function selectedAuto(mode) { return !!settings?.[mode === 'desk' ? 'deskAutoASR' : 'meetingAutoASR']; }
function localModelLabel() { return choices.find(model => model.id === settings?.localModel)?.label || settings?.localModel || ''; }
function phaseLabel(desk) {
  const phases = {listening: '正在守候，等待说话', speech: '检测到语音，正在保存', recording: '检测到语音，正在保存', paused: '守候已暂停', stopping: '正在保存最后一段', error: '守候已停止'};
  return phases[desk?.phase] || desk?.phase || (desk?.active ? '正在守候，等待说话' : '语音出现时，自动留下一段。');
}
function renderCapture() {
  const state = ui.capture(snapshot, requestedMode, setup);
  const desk = snapshot?.desk || {};
  if (state.active) requestedMode = state.mode;
  $('#mode-meeting').setAttribute('aria-pressed', String(state.mode === 'meeting'));
  $('#mode-desk').setAttribute('aria-pressed', String(state.mode === 'desk'));
  $('#mode-meeting').disabled = $('#mode-desk').disabled = state.modeLocked || capturePending;
  $('#capture-note').textContent = ui.autoASRDescription(state.mode,selectedAuto(state.mode),localModelLabel());
  $('#auto-asr-label').textContent = state.mode === 'desk' ? '自动追加本地转写' : '结束后自动转写';
  if (!optionPending) $('#auto-asr').checked = selectedAuto(state.mode);
  $('#auto-asr').disabled = optionPending || capturePending;
  $('#record').textContent = capturePending ? (state.active ? '正在保存…' : '正在准备…') : state.action;
  $('#record').disabled = capturePending || state.disabled;
  $('#phase').textContent = state.mode === 'desk' ? (desk.error || phaseLabel(desk)) : snapshot?.recording ? '正在录音' : '就绪';
  $('#phase').classList.toggle('error', state.mode === 'desk' && !!desk.error);
  $('#listening-dot').hidden = !state.active;
  $('#listening-dot').classList.toggle('desk-dot', state.mode === 'desk');
  $('#timer').hidden = !state.active;
  $('#timer').dataset.kind = state.mode;
  $('#timer').textContent = ui.duration(state.mode === 'desk' ? state.speechMS : state.elapsedMS);
  $('#timer').setAttribute('aria-label', `${state.mode === 'desk' ? '已保留音频' : '录音时长'} ${$('#timer').textContent}`);
  $('#desk-metrics').hidden = state.mode !== 'desk' || (!state.active && !state.clips);
  $('#desk-metrics').textContent = state.active ? `已守候 ${ui.duration(state.elapsedMS)} · ${state.clips} 段声音` : `本轮保留 ${ui.duration(state.speechMS)} · ${state.clips} 段声音`;
  $('#capture-tools').hidden = state.toolsReady || state.active;
  $('#capture-tools-note').textContent = state.mode === 'desk' ? '守候需要录音组件和本地语音检测器。' : '仅需录音组件，无需先安装转写模型。';
  $('#install-capture').textContent = setup?.installing ? '正在准备组件…' : state.mode === 'desk' ? '准备守候组件' : '准备录音组件';
  $('#install-capture').disabled = !!setup?.installing;
  const running = !!snapshot?.jobsRunning;
  const queued = Number(snapshot?.queued || 0);
  $('#queue-status').hidden = !running && !queued;
  $('#queue-status').textContent = `${running ? '后台转写中' : ''}${running && queued ? ' · ' : ''}${queued ? `${queued} 项等待转写` : ''}`;
  $('#import').disabled = $('#show-url').disabled = $('#download-audio').disabled = capturePending || !!setup?.installing;
}
function renderList() {
  const query = $('#search').value.toLowerCase();
  const items = (snapshot?.sessions || []).filter(item => (!pendingOnly || item.pendingSpeakers > 0) && `${item.title} ${item.id}`.toLowerCase().includes(query));
  $('#sessions').replaceChildren(...items.map(item => {
    const button = node('button', `session${item.id === selected && viewMode === 'session' ? ' selected' : ''}`);
    button.setAttribute('aria-current', String(item.id === selected && viewMode === 'session'));
    const fallback = item.mode === 'desk' ? '桌边讨论' : '未命名录音';
    button.append(node('strong', '', item.title || fallback), node('small', '', `${dateLabel(item.started_at)} · ${ui.sessionState(item)}`));
    if (item.mode === 'desk' && item.clipCount) button.append(node('small', 'session-duration', `保留 ${ui.duration(item.speechMS)} · ${item.clipCount} 段`));
    button.onclick = () => act(() => showSession(item.id));
    const row = node('div', 'session-row');
    const remove = node('button', 'session-delete', '删除');
    remove.setAttribute('aria-label', `删除录音：${item.title || fallback}`);
    remove.disabled = !!item.active;
    remove.onclick = () => act(async () => {
      remove.disabled = true;
      try {
        if (!await call('DeleteSession', item.id)) return;
        if (selected === item.id) {
          selected = selectedVersion = ''; detail = null; versions = []; ++request;
          detailFingerprint = versionFingerprint = '';
        }
        listFingerprint = ''; folderLoaded = false;
        await browseFolder(''); await refresh();
      } finally { remove.disabled = !!item.active; }
    });
    row.append(button, remove); return row;
  }));
  if (!items.length) $('#sessions').append(node('p', 'list-empty', pendingOnly ? '没有待确认的说话人。' : query ? '没有匹配的录音。' : '音频保存在这里，\n需要时再转写。'));
}
function renderTranscript(text, target = $('#transcript')) {
  target.replaceChildren();
  const lines = (text || '').split('\n').filter(line => line.trim() && !line.startsWith('#'));
  if (!lines.length) { target.append(node('p', 'muted', '暂无转写稿。')); return; }
  for (const line of lines) {
    const match = ui.transcriptLine(line);
    if (match) {
      const block = node('section', 'utterance'), header = node('header');
      header.append(node('span', '', match.speaker), node('time', '', match.time));
      block.append(header, node('p', '', match.text)); target.append(block);
    } else target.append(node('p', 'text-transcript', line));
  }
}
function renderModelChoices() {
  const fingerprint = JSON.stringify(choices);
  if (fingerprint !== choiceFingerprint) {
    choiceFingerprint = fingerprint;
    const current = $('#transcribe-model').value;
    $('#transcribe-model').replaceChildren(...choices.map(choice => {
      const option = node('option', '', choice.label + (choice.installed ? '' : ' · 未就绪'));
      option.value = choice.id; option.disabled = !choice.installed; return option;
    }));
    const preferred = current || settings?.localModel;
    const available = choices.find(choice => choice.id === preferred && choice.installed) || choices.find(choice => choice.installed);
    if (available) $('#transcribe-model').value = available.id;
  }
  const choice = choices.find(item => item.id === $('#transcribe-model').value);
  const liveMeeting = detail?.session?.active && detail.session.mode !== 'desk';
  const hasAudio = !!detail?.session?.audio && (!detail?.session?.active || detail.session.mode !== 'desk' || detail.session.clipCount > 0);
  $('#transcribe').disabled = transcribePending || !choice?.installed || !hasAudio || liveMeeting;
  $('#transcribe').textContent = transcribePending ? '正在加入队列…' : versions.length || detail?.transcript ? '生成新版本' : '生成转写稿';
  $('#transcribe-model').disabled = transcribePending || !choices.some(item => item.installed);
  $('#transcribe-setup').hidden = choices.every(item => item.installed) && choices.length > 0;
  $('#model-note').textContent = liveMeeting ? '先结束录音并保存，再选择模型转写。' : choice?.id === 'configured' && settings?.asr?.kind !== 'local'
    ? '音频会发送到已配置的在线服务。新结果会另存为一个版本。'
    : '生成独立版本，保留已有结果。';
}
function renderVersions() {
  const fingerprint = JSON.stringify(versions.map(run => [run.id, run.model, run.state, run.createdAt, run.error]));
  if (fingerprint !== versionFingerprint) {
    versionFingerprint = fingerprint;
    const options = [node('option', '', '当前转写稿 · 自动更新')]; options[0].value = '';
    for (const [index, run] of versions.entries()) { const option = node('option', '', `版本 ${versions.length - index} · ${run.model} · ${dateLabel(run.createdAt)} · ${ui.runLabel(run)}`); option.value = run.id; options.push(option); }
    if (selectedVersion && !versions.some(run => run.id === selectedVersion)) selectedVersion = '';
    $('#transcript-version').replaceChildren(...options); $('#transcript-version').value = selectedVersion;
  }
  $('#version-row').hidden = !versions.length;
  const viewing = versions.find(run => run.id === selectedVersion);
  $('#version-status').hidden = !viewing;
  $('#version-status').textContent = viewing ? viewing.automatic ? `自动追加 · ${(viewing.clips || []).filter(clip=>['complete','empty'].includes(clip.state)).length}/${(viewing.clips || []).length} 段已处理` : '独立转写版本' : '';
  const failed = ui.latestFailed(versions);
  $('#retry').hidden = !failed; $('#retry').disabled = transcribePending;
  const notice=ui.attemptStatus(versions,selectedVersion,!!detail?.transcript);
  $('#attempt-status').hidden=notice.kind==='none';$('#attempt-status').classList.toggle('error',notice.kind==='error');$('#attempt-status').textContent=notice.text;
}
function renderSpeakers(value, runID) {
  const entries = value.unidentified || [], groups = ui.speakerGroups(value);
  const id = value.session.id, scope = `${id}:${runID}`;
  const focused = document.activeElement?.dataset?.speakerMember;
  const focusedKind = document.activeElement?.dataset?.speakerKind;
  const selectionStart = document.activeElement?.selectionStart, selectionEnd = document.activeElement?.selectionEnd;
  const entryByID = new Map(entries.map(entry => [entry.spk,entry]));
  const pending = groups.filter(group => !group.name), recognized = groups.filter(group => group.name);
  $('#speakers').hidden = !groups.length && !value.hasSpeakers;
  $('#voice-library-status').textContent = `共享声纹 · ${value.voiceProfileCount || 0} 人 · ${value.voiceSampleCount || 0} 段声音`;
  $('#match-voices').onclick = () => act(async () => {
    const button = $('#match-voices'), status = $('#voice-match-status');
    button.disabled = true; status.hidden = false; status.textContent = '正在匹配声音…';
    try {
      await call('ReidentifySpeakers', id, runID);
      detailFingerprint = ''; await refreshDetail();
      status.textContent = '匹配完成。';
    } catch (error) { status.hidden = true; throw error; }
    finally { button.disabled = false; }
  });
  $('#names').replaceChildren(...(value.names || []).map(name => { const option = node('option'); option.value = name; return option; }));
  const refreshSpeakers = () => renderSpeakers(value,runID);
  function disclosure(key, label) {
    const details = node('details', 'speaker-disclosure'), summary = node('summary','',label);
    details.open = speakerOpen.has(key); details.append(summary);
    details.ontoggle = () => { if (details.open) speakerOpen.add(key); else speakerOpen.delete(key); };
    return details;
  }
  function editor(memberIDs, name, representative, label, groupAction) {
    const draftKey = member => ui.speakerDraftKey(scope,member,groupAction ? 'group' : 'clip');
    const key = `${scope}:${groupAction ? 'group' : 'clip'}:${memberIDs[0]}`;
    const row = node('div','speaker-editor');
    const play = node('button','','试听'); play.setAttribute('aria-label',`试听${label}`);
    play.onclick = () => act(() => call('PlayVersion',id,runID,representative));
    const input = node('input'); input.placeholder = '姓名'; input.setAttribute('list','names');
    const drafted = memberIDs.find(member => speakerDrafts.has(draftKey(member)));
    input.value = drafted ? speakerDrafts.get(draftKey(drafted)) : name || '';
    input.dataset.speakerMember = memberIDs.includes(focused) ? focused : memberIDs[0];
    input.dataset.speakerMembers = JSON.stringify(memberIDs); input.dataset.speakerKind = groupAction ? 'group' : 'clip';
    input.setAttribute('aria-label',`${label}的姓名`);
    input.oninput = () => memberIDs.forEach(member => speakerDrafts.set(draftKey(member),input.value));
    const save = node('button','',name ? '更新' : '确认');
    save.disabled = speakerBusy.has(key); save.textContent = save.disabled ? '保存中…' : name ? '更新' : '确认';
    save.onclick = () => act(async () => {
      if (!input.value.trim()) { input.focus(); return; }
      if (speakerBusy.has(key)) return;
      speakerBusy.add(key); save.disabled = true; save.textContent = '保存中…';
      try {
        if (groupAction) await call('LabelSpeakerGroup',id,runID,memberIDs,input.value);
        else await call('LabelVersion',id,runID,memberIDs[0],input.value);
        memberIDs.forEach(member => speakerDrafts.delete(draftKey(member)));
      } finally { speakerBusy.delete(key); detailFingerprint = ''; await refreshDetail(); }
    });
    row.append(play,input,save); return row;
  }
  function groupRow(group, index) {
    const block = node('section','speaker-group'), label = group.name || `声音组 ${index+1}`;
    const header = node('div','speaker-group-heading');
    header.append(node('strong','',label),node('span','muted',`${group.count} 段${group.name ? group.confirmed ? ' · 已确认' : ' · 已匹配' : ''}`));
    block.append(header,editor(group.memberIDs,group.name,group.representative || group.memberIDs[0],label,true));
    const key = `${scope}:${group.id}:members`;
    const review = disclosure(key,group.count > 1 ? `检查 ${group.count} 段声音` : '查看片段');
    if (group.reason) review.append(node('p','field-note',group.reason));
    const list = node('div','speaker-members');
    function fillMembers() {
      const count = speakerLimits.get(key) || 5;
      list.replaceChildren(...group.memberIDs.slice(0,count).map((member,index) => {
        const entry = entryByID.get(member) || {spk:member};
        const part = node('div','speaker-member');
        part.append(node('p','field-note',`片段 ${index+1} · ${ui.duration(entry.start_ms || 0)}`));
        part.append(editor([member],entry.name,member,`片段 ${index+1}`,false));
        if (entry.reason && !entry.name && (group.count > 1 || entry.reason !== group.reason)) part.append(node('p','field-note',entry.reason));
        return part;
      }));
      if (group.count > count) {
        const more = node('button','quiet speaker-more',`再显示 ${Math.min(5,group.count-count)} 段`);
        more.onclick = () => {speakerLimits.set(key,count+5);fillMembers();}; list.append(more);
      }
    }
    if (review.open) fillMembers();
    review.addEventListener('toggle',()=>{if(review.open && !list.childElementCount)fillMembers();});
    review.append(list); block.append(review); return block;
  }
  const contents = [];
  if (pending.length) {
    contents.push(node('h4','speaker-subheading',`待确认声音 · ${pending.length} 组`));
    const limit = speakerLimits.get(`${scope}:pending`) || 3;
    contents.push(...pending.slice(0,limit).map(groupRow));
    if (pending.length > limit) {
      const more = node('button','quiet speaker-more',`再显示 ${Math.min(3,pending.length-limit)} 组（剩余 ${pending.length-limit} 组）`);
      more.onclick = () => {speakerLimits.set(`${scope}:pending`,limit+3);refreshSpeakers();}; contents.push(more);
    }
  } else contents.push(node('p','field-note',groups.length ? '所有声音已匹配。' : '可重新匹配已保存的声音。'));
  if (recognized.length) {
    const known = disclosure(`${scope}:recognized`,`已识别 · ${recognized.length} 人`);
    const list = node('div','recognized-speakers');
    const fill = () => {
      const limit = speakerLimits.get(`${scope}:recognized`) || 5;
      list.replaceChildren(...recognized.slice(0,limit).map(groupRow));
      if(recognized.length>limit){const more=node('button','quiet speaker-more',`再显示 ${Math.min(5,recognized.length-limit)} 人`);more.onclick=()=>{speakerLimits.set(`${scope}:recognized`,limit+5);fill();};list.append(more);}
    };
    if(known.open)fill();known.addEventListener('toggle',()=>{if(known.open&&!list.childElementCount)fill();});
    known.append(list);contents.push(known);
  }
  $('#speaker-list').replaceChildren(...contents);
  if (focused) {
    const input = [...$('#speaker-list').querySelectorAll('input')].find(input => input.dataset.speakerKind===focusedKind && JSON.parse(input.dataset.speakerMembers).includes(focused));
    if(input){input.focus({preventScroll:true});if(selectionStart!==null)input.setSelectionRange(selectionStart,selectionEnd);}
  }
}
async function refreshDetail() {
  if (!selected || viewMode !== 'session') return;
  const id = selected, runID = selectedVersion, version = ++request;
  const [value, runs] = await Promise.all([call('TranscriptDetail', id, runID), call('TranscriptVersions', id)]);
  if (selected !== id || version !== request || viewMode !== 'session' || selectedVersion !== runID) return;
  detail = value; versions = ui.orderedRuns(runs);
  renderVersions(); renderModelChoices();
  const fingerprint = JSON.stringify([value, runID, versions.map(run=>[run.id,run.state]), !!settings?.titleEndpoint && !!settings?.titleModel]);
  if (fingerprint === detailFingerprint) return;
  detailFingerprint = fingerprint;
  $('#empty').hidden = true; $('#detail').hidden = false; $('#archive-browser').hidden = $('#onboarding').hidden = true;
  $('#rename').disabled = !!value.session.active;
  $('#regenerate-title').hidden = !settings?.titleEndpoint || !settings?.titleModel;
  $('#regenerate-title').disabled = titlePending || !value.transcript;
  $('#title').textContent = value.session.title || (value.session.mode === 'desk' ? '桌边讨论' : '未命名录音');
  $('#date').textContent = `${dateLabel(value.session.started_at)}${value.session.mode === 'desk' ? ' · 桌边守候' : ' · 会议录音'}`;
  const transcriptState=ui.transcriptState(value,versions,runID);
  $('#stages').replaceChildren(node('span', value.session.captureError ? 'error' : 'done', value.session.active ? '录音进行中' : value.session.captureError ? '录音有异常' : '音频已保存'), node('span', transcriptState.kind==='error'?'error':transcriptState.kind==='empty'?'muted':'done', transcriptState.label));
  $('#session-metrics').hidden = value.session.mode !== 'desk';
  $('#timestamp-note').hidden = !value.timestampEstimated;
  $('#session-metrics').textContent = `保留音频 ${ui.duration(value.session.speechMS)} · ${value.session.clipCount || 0} 段 · 讨论跨度 ${ui.duration(value.session.elapsedMS)}`;
  const sessionError = ui.sessionError(value,versions);
  $('#session-error').hidden = !sessionError; $('#session-error').textContent = sessionError;
  $('#warning').hidden = !value.session.warning; $('#warning').textContent = value.session.warning || '';
  $('#transcript-section').hidden=!value.transcript && ['error','empty'].includes(transcriptState.kind);
  renderTranscript(value.transcript);
  $('#copy').disabled = !value.transcript;
  $('#play').disabled = !value.session.audio || (value.session.active && value.session.mode !== 'desk');
  renderSpeakers(value, value.runId || runID || value.session.activeRun || '');
}
async function refresh() {
  const [nextSetup, value, nextSettings, nextChoices] = await Promise.all([call('SetupStatus'), call('Snapshot'), call('GetSettings'), call('ModelChoices')]);
  setup = nextSetup; snapshot = value; settings = nextSettings; choices = nextChoices || [];
  if (archiveRoot && archiveRoot !== value.root) {
    selected = selectedVersion = ''; detailFingerprint = versionFingerprint = ''; folderLoaded = false; viewMode = 'archive';
  }
  archiveRoot = value.root;
  renderSetup(); renderCapture();
  $('#stop-play').disabled = !value.playing;
  if (!value.playing) { $('#archive-play-status').textContent = ''; $('#archive-stop').hidden = true; }
  if (viewMode === 'archive' && !folderLoaded) await browseFolder('');
  $('#pending-filter').textContent = `待确认声音 · ${(value.sessions || []).reduce((count, item) => count + (item.pendingSpeakers || 0), 0)}`;
  const fingerprint = JSON.stringify(value.sessions);
  if (fingerprint !== listFingerprint) { listFingerprint = fingerprint; renderList(); }
  await refreshDetail();
}
async function afterImport(method, ...args) {
  await call(method, ...args); await refresh();
  if (snapshot.current) await showSession(snapshot.current);
}
async function captureAction() {
  const state = ui.capture(snapshot, requestedMode, setup);
  capturePending = true; renderCapture();
  try {
    await call(state.mode === 'desk' ? (state.active ? 'StopDesk' : 'StartDesk') : (state.active ? 'Finish' : 'Start'));
    await refresh();
    const current = state.mode === 'desk' ? snapshot.desk?.current : snapshot.current;
    if (current) await showSession(current);
  } finally { capturePending = false; renderCapture(); }
}
$('#record').onclick = () => act(captureAction);
$('#mode-meeting').onclick = () => { requestedMode = 'meeting'; renderCapture(); };
$('#mode-desk').onclick = () => { requestedMode = 'desk'; renderCapture(); };
$('#auto-asr').onchange = () => act(async () => {
  const mode = ui.capture(snapshot, requestedMode, setup).mode, auto = $('#auto-asr').checked;
  optionPending = true; renderCapture();
  try { await call('SetRecordingOptions', mode, auto); settings = await call('GetSettings'); }
  finally { optionPending = false; renderCapture(); }
});
$('#install-capture').onclick = () => act(async () => { await call(requestedMode === 'desk' ? 'InstallDeskTools' : 'InstallRecordingTools'); await refresh(); });
$('#import').onclick = () => act(() => afterImport('ImportAudio'));
$('#show-url').onclick = () => { $('#url-form').hidden = !$('#url-form').hidden; if (!$('#url-form').hidden) $('#audio-url').focus(); };
$('#url-form').onsubmit = event => { event.preventDefault(); act(async () => { $('#download-audio').disabled = true; try { await afterImport('ImportURL', $('#audio-url').value); $('#url-form').hidden = true; $('#audio-url').value = ''; } finally { renderCapture(); } }); };
$('#settings').onclick = () => act(() => call('OpenSettings'));
$('#setup').onclick = $('#transcribe-setup').onclick = () => { viewMode = 'setup'; renderSetup(); };
$('#setup-browse').onclick = $('#archive').onclick = () => act(() => browseFolder(''));
$('#folder').onclick = () => act(() => browseFolder(detail.session.directory));
$('#play').onclick = () => act(() => call('Play', selected, ''));
$('#stop-play').onclick = () => act(() => call('StopPlayback'));
$('#transcribe-model').onchange = renderModelChoices;
$('#transcribe').onclick = () => act(async () => {
  transcribePending = true; renderModelChoices();
  try { await call('Transcribe', selected, $('#transcribe-model').value); await refreshDetail(); }
  finally { transcribePending = false; renderModelChoices(); }
});
$('#retry').onclick = () => act(async () => { transcribePending = true; renderVersions(); try { await call('Retry', selected); await refreshDetail(); } finally { transcribePending = false; renderVersions(); } });
$('#transcript-version').onchange = () => act(async () => { selectedVersion = $('#transcript-version').value; detailFingerprint = ''; await refreshDetail(); });
$('#copy').onclick = () => act(async () => { await navigator.clipboard.writeText(detail?.transcript || ''); $('#copy').textContent = '已复制'; setTimeout(() => $('#copy').textContent = '复制', 1500); });
$('#search').oninput = renderList;
$('#pending-filter').onclick = () => { pendingOnly = !pendingOnly; $('#pending-filter').setAttribute('aria-pressed', String(pendingOnly)); renderList(); };

function renderSetup() {
  if (!setup) return;
  window.MettingsPreferencesState.fillModels($('#setup-model'),choices);
  if(document.activeElement !== $('#setup-model')) $('#setup-model').value = setup.model; $('#setup-model').disabled = setup.installing;
  $('#setup-model-label').textContent = choices.find(model=>model.id===setup.model)?.label || setup.model;
  $('#setup-download-size').textContent = `需要下载约 ${Math.ceil((setup.installBytes || 0) / 1000000)} MB`;
  $('#onboarding').hidden = viewMode !== 'setup' || isSettings;
  $('#archive-browser').hidden = viewMode !== 'archive' || isSettings;
  if (viewMode === 'setup') $('#empty').hidden = $('#detail').hidden = true;
  $('#install').disabled = setup.installing || setup.ready;
  $('#install').textContent = setup.ready ? '所选转写组件已就绪' : setup.error ? '重新安装转写组件' : setup.installing ? '正在安装…' : '安装转写组件';
  $('#cancel-install').hidden = !setup.installing;
  $('#setup-name').textContent = setup.installing ? `${setup.name} · ${Math.round(setup.downloaded / 1000000)} / ${Math.round(setup.total / 1000000)} MB` : '';
  $('#setup-progress').hidden = !setup.installing; $('#setup-progress').value = setup.total ? Math.min(100, setup.downloaded / setup.total * 100) : 0;
  $('#setup-error').hidden = !setup.error; $('#setup-error').textContent = setup.error || '';

}
async function refreshSetup() { setup = await call('SetupStatus'); renderSetup(); }
$('#install').onclick = () => act(async () => { await call('InstallRuntime'); await refreshSetup(); });
$('#cancel-install').onclick = () => act(() => call('CancelSetup'));
$('#setup-model').onchange = () => act(async () => { await call('SelectLocalModel', $('#setup-model').value); await refreshSetup(); });

async function showSession(id) {
  $('#title-status').hidden = true;
  viewMode = 'session'; selected = id; selectedVersion = ''; detailFingerprint = versionFingerprint = ''; versions = [];
  $('#archive-browser').hidden = $('#onboarding').hidden = true; renderList(); await refreshDetail();
}
function sizeLabel(size) { return size < 1024 ? `${size} B` : size < 1048576 ? `${Math.round(size / 1024)} KB` : `${(size / 1000000).toFixed(1)} MB`; }
async function browseFolder(path) {
  const version = ++folderRequest; viewMode = 'archive';
  const folder = await call('BrowseArchive', path); if (version !== folderRequest || viewMode !== 'archive') return;
  folderPath = folder.path; folderLoaded = true;
  $('#archive-browser').hidden = false; $('#detail').hidden = $('#empty').hidden = $('#file-preview').hidden = $('#onboarding').hidden = true;
  $('#archive-location').textContent = folder.root; $('#archive-up').disabled = !folderPath; renderList();
  $('#breadcrumbs').replaceChildren();
  const root = node('button', 'quiet', '全部会议'); root.onclick = () => act(() => browseFolder('')); $('#breadcrumbs').append(root);
  const parts = folderPath.split('/').filter(Boolean); parts.forEach((part, index) => { $('#breadcrumbs').append(node('span', 'muted', '/')); const button = node('button', 'quiet', part); button.onclick = () => act(() => browseFolder(parts.slice(0, index + 1).join('/'))); $('#breadcrumbs').append(button); });
  const session = snapshot?.sessions?.find(item => item.directory === folderPath);
  $('#archive-session').hidden = !session; $('#archive-session').onclick = () => act(() => showSession(session.id));
  $('#archive-empty').hidden = !!folder.entries.length;
  $('#archive-entries').replaceChildren(...folder.entries.map(entry => {
    const button = node('button', 'file-row');
    button.append(node('span', 'file-kind', entry.directory ? '目录' : entry.kind === 'audio' ? '音频' : entry.kind === 'text' ? '文本' : '文件'), node('span', 'file-title', entry.name), node('span', 'file-size', entry.directory ? '打开 ›' : sizeLabel(entry.size)));
    button.disabled = entry.kind === 'file';
    button.onclick = () => act(async () => {
      if (entry.directory) return browseFolder(entry.path);
      if (entry.kind === 'audio') { await call('PlayArchiveFile', entry.path); $('#archive-play-status').textContent = `播放：${entry.name}`; $('#archive-stop').hidden = false; return; }
      const text = await call('ReadArchiveFile', entry.path); if (viewMode !== 'archive' || folderPath !== folder.path) return;
      $('#file-preview').hidden = false; $('#file-name').textContent = entry.name; renderTranscript(text, $('#file-content')); $('#file-preview').scrollIntoView({block: 'start'});
    }); return button;
  }));
}
$('#archive-up').onclick = () => act(() => browseFolder(folderPath.split('/').slice(0, -1).join('/')));
$('#archive-refresh').onclick = () => act(() => browseFolder(folderPath));
$('#archive-settings').onclick = () => act(() => call('OpenSettings'));
$('#file-close').onclick = () => $('#file-preview').hidden = true;
$('#archive-stop').onclick = () => act(async () => { await call('StopPlayback'); $('#archive-play-status').textContent = ''; $('#archive-stop').hidden = true; });

if (isSettings) {
  document.title = 'Mettings 设置'; $('#workspace').hidden = true; $('#preferences').hidden = false;
  window.MettingsPreferences.start();
} else {
  const poll = async () => { await act(refresh); setTimeout(poll, 1200); }; poll();
}

$('#rename').onclick = () => { $('#rename-form').hidden = false; $('#rename-title').value = detail?.session.title || ''; $('#rename-title').focus(); };
$('#regenerate-title').onclick = () => act(async () => {
  if (titlePending || !selected) return;
  const id = selected;
  titlePending = true; $('#regenerate-title').disabled = true;
  $('#title-status').hidden = false; $('#title-status').textContent = '正在生成标题…';
  try {
    await call('RegenerateTitle', id);
    if (selected === id) { detailFingerprint = ''; await refresh(); $('#title-status').textContent = '标题已更新。'; }
  } catch (error) {
    if (selected === id) $('#title-status').hidden = true;
    throw error;
  } finally { titlePending = false; $('#regenerate-title').disabled = !detail?.transcript; }
});
$('#rename-cancel').onclick = () => $('#rename-form').hidden = true;
$('#rename-form').onsubmit = event => { event.preventDefault(); act(async () => { await call('RenameSession', selected, $('#rename-title').value); $('#rename-form').hidden = true; detailFingerprint = ''; await refresh(); }); };
