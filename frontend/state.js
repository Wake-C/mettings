(function (root) {
  'use strict';
  const runStates = { queued: '排队中', running: '转写中', partial: '持续追加', complete: '已完成', empty: '无文本', failed: '失败' };
  function duration(ms) {
    const seconds = Math.max(0, Math.floor(Number(ms || 0) / 1000));
    const hours = Math.floor(seconds / 3600);
    const rest = `${String(Math.floor(seconds % 3600 / 60)).padStart(2, '0')}:${String(seconds % 60).padStart(2, '0')}`;
    return hours ? `${hours}:${rest}` : rest;
  }
  function transcriptLine(line) {
    const match = line.match(/^\*\*\[((?:\d{4}-\d{2}-\d{2} )?\d+:\d{2}:\d{2})\]\s*(.*?)\*\*[:：](.*)$/);
    return match ? {time:match[1],speaker:match[2],text:match[3]} : null;
  }
  function capture(snapshot, requestedMode, setup) {
    snapshot = snapshot || {};
    setup = setup || {};
    const desk = snapshot.desk || {};
    const mode = desk.active ? 'desk' : snapshot.recording ? 'meeting' : requestedMode;
    const active = mode === 'desk' ? !!desk.active : !!snapshot.recording;
    const toolsReady = mode === 'desk' ? (setup.deskReady ?? setup.ready) : (setup.captureReady ?? setup.ready);
    return {
      mode, active, toolsReady: !!toolsReady,
      disabled: !active && (!toolsReady || !!setup.installing),
      modeLocked: !!desk.active || !!snapshot.recording,
      action: active ? (mode === 'desk' ? '暂停守候' : '结束并保存') : (mode === 'desk' ? '开始守候' : '开始录音'),
      elapsedMS: mode === 'desk' ? (desk.elapsedMS || 0) : (snapshot.seconds || 0) * 1000,
      speechMS: desk.speechMS || 0,
      clips: desk.clipCount || 0,
    };
  }
  function orderedRuns(runs) {
    return [...(runs || [])].sort((a, b) => String(b.createdAt || b.id).localeCompare(String(a.createdAt || a.id)));
  }
  function latestFailed(runs) { return orderedRuns(runs).find(run => run.state === 'failed') || null; }
  function runLabel(run) { return runStates[run?.state] || run?.state || '待转写'; }
  function attemptStatus(runs, selectedID, hasTranscript) {
    const ordered=orderedRuns(runs), selected=ordered.find(run=>run.id===selectedID);
    const run=selected || ordered[0];
    if (!run) return {kind:'none',text:''};
    if (run.state==='failed') return {kind:'error',text:`${run.model}：${run.error || '转写失败'}`};
    if (run.state==='empty') return {kind:'empty',text:`${run.model}：未生成转写文本`};
    const pending=selected ? [selected] : ordered.filter(item=>['queued','running','partial'].includes(item.state));
    if (pending.some(item=>['queued','running','partial'].includes(item.state))) return {kind:'progress',text:pending.map(item=>`${item.model}：${runLabel(item)}`).join(' · ')};
    return {kind:'none',text:''};
  }
  function transcriptState(detail,runs,selectedID) {
    const run=selectedID ? runs.find(item=>item.id===selectedID) : (!detail.transcript ? orderedRuns(runs)[0] : null);
    if (run?.state==='failed') return {kind:'error',label:'转写失败'};
    if (run?.state==='empty') return {kind:'empty',label:'无转写文本'};
    if (detail.transcript) return {kind:'ready',label:'转写稿可用'};
    if (run && ['queued','running','partial'].includes(run.state)) return {kind:'progress',label:runLabel(run)};
    return {kind:'none',label:'待转写'};
  }
  function speakerDraftKey(scope, member, kind) {
    return JSON.stringify([scope,kind,member]);
  }
  function speakerGroups(detail) {
    const entries = detail.unidentified || [];
    if (Array.isArray(detail.speakerGroups)) return detail.speakerGroups.map(group => ({...group, memberIDs:[...(group.memberIDs || [])], count:(group.memberIDs || []).length || group.count || 0}));
    // Older detail responses have no acoustic grouping. Only names can be
    // grouped safely in the browser; unknown clips stay separate.
    const groups = [], named = new Map();
    for (const entry of entries) {
      let group = entry.name ? named.get(entry.name) : null;
      if (!group) {
        group = {id:entry.spk, name:entry.name || '', confirmed:!!entry.labeled, memberIDs:[], representative:entry.spk, count:0, reason:entry.reason || ''};
        groups.push(group); if (entry.name) named.set(entry.name,group);
      }
      group.memberIDs.push(entry.spk); group.count++; group.confirmed = group.confirmed && !!entry.labeled;
    }
    return groups;
  }
  function autoASRDescription(mode,enabled,model) {
    return mode==='desk' ? enabled ? `本地 ${model} · 自动追加` : '仅保存片段。' : enabled ? '结束后自动转写。' : '仅保存音频。';
  }
  function sessionError(detail, runs) {
    const session = detail.session || {};
    if (session.captureError) return session.captureError;
    const error = session.error || '';
    return error && !runs.some(run => run.error === error) ? error : '';
  }
  function sessionState(item) {
    if (item.active) return item.mode === 'desk' ? '讨论进行中' : '录音中';
    if (item.captureError) return '录音有异常';
    if (item.error) return '有失败记录';
    if (item.pendingSpeakers) return `${item.pendingSpeakers} 段声音待确认`;
    return item.transcribed ? '已有转写稿' : '音频已保存';
  }
  const api = { speakerDraftKey, speakerGroups, transcriptLine, duration, capture, orderedRuns, latestFailed, runLabel, sessionState, attemptStatus, transcriptState, autoASRDescription, sessionError };
  root.MettingsUI = api;
  if (typeof module !== 'undefined' && module.exports) module.exports = api;
})(typeof globalThis !== 'undefined' ? globalThis : this);
