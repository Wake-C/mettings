(function(root) {
  'use strict';
  const $ = selector => document.querySelector(selector);
  const state = root.MettingsPreferencesState;
  const call = (method, ...args) => root.mygo.call(`Desktop.${method}`, ...args);
  let titleTesting = false, titleTestVersion = 0;
  let saved = null, baseline = null, setup = null, models = [], saving = false, pending = false, devicesLoaded = false, dirty = false;
  function error(message) { $('#settings-error').textContent = String(message?.message || message); $('#settings-error').hidden = false; }
  function clearError() { $('#settings-error').hidden = true; }
  function note(message) { $('#save-status').textContent = message; }
  function selectTab(name, focus = false) {
    for (const tab of ['basic','advanced']) {
      const active = tab === name, button = $(`#settings-tab-${tab}`);
      button.setAttribute('aria-selected', String(active)); button.tabIndex = active ? 0 : -1;
      $(`#settings-${tab}`).hidden = !active;
    }
    $('.settings-panels').scrollTop = 0;
    if (focus) $(`#settings-tab-${name}`).focus();
  }
  function reveal(field) {
    selectTab(field.closest('#settings-advanced') ? 'advanced' : 'basic');
    let parent = field.parentElement;
    while (parent && parent.id !== 'settings-form') { if (parent.tagName === 'DETAILS') parent.open = true; parent = parent.parentElement; }
    field.focus(); field.scrollIntoView({block:'nearest'});
  }
  function option(value, label) { const node=document.createElement('option');node.value=value;node.textContent=label;return node; }
  function providerFields() {
    const kind = $('#kind').value, local = kind === 'local', volc = kind === 'volc', fireRed = kind === 'firered2-llm';
    $('#firered-service-note').hidden = !fireRed;
    $('#cloud-fields').hidden = local; $('#compatible-fields').hidden = volc; $('#volc-fields').hidden = !volc;
    $('#cloud-fields').querySelectorAll('input,select').forEach(field => field.disabled = local);
    $('#compatible-fields').querySelectorAll('input,select').forEach(field => field.disabled = local || volc);
    $('#volc-fields').querySelectorAll('input,select').forEach(field => field.disabled = local || !volc);
    $('#format').disabled = local || volc || fireRed;
    $('#endpoint').required = !local; $('#model').required = kind === 'compatible' || fireRed;
    $('#provider-summary').textContent = local ? '默认关闭' : volc ? 'Volcengine' : fireRed ? 'FireRedASR2-LLM' : '兼容服务';
    $('#online-route-note').hidden = $('#manage-provider').hidden = local;
    $('#online-route-note').textContent = `默认转写已选择${volc ? ' Volcengine' : '在线兼容服务'}。会议自动转写会发送音频；桌边守候仍使用本地模型。`;
    $('#title-summary').textContent = $('#title-endpoint').value.trim() && $('#title-model').value.trim() ? '已配置在线服务' : '使用转写稿首句';
    $('#threshold-summary').textContent = Number($('#voice-threshold').value) === .6 ? '推荐 0.6' : `当前 ${$('#voice-threshold').value || '未填写'}`;
  }
  function fill(settings, resetSecrets = false) {
    $('#local-model').value = settings.localModel || 'qwen3';
    $('#voice-threshold').value = settings.voiceThreshold || .6;
    $('#settings-meeting-auto').checked = !!settings.meetingAutoASR; $('#settings-desk-auto').checked = !!settings.deskAutoASR;
    for (const [id,key] of Object.entries({root:'root',hotwords:'hotwords',corrections:'corrections','title-endpoint':'titleEndpoint','title-model':'titleModel'})) $(`#${id}`).value = settings[key] || '';
    $('#aliases').value = state.aliasesToText(settings.aliases || '{}');
    for (const [id,key] of Object.entries({kind:'kind',endpoint:'endpoint',model:'model',format:'responseFormat',resource:'resourceId'})) $(`#${id}`).value = settings.asr?.[key] || (id === 'format' ? 'json' : id === 'kind' ? 'local' : '');
    const device = settings.device || 'default';
    if (![...$('#device').options].some(item => item.value === device)) $('#device').append(option(device, `已保存的麦克风 ${device}`));
    $('#device').value = device;
    if (resetSecrets) $('#key').value = $('#app-key').value = $('#title-key').value = '';
    providerFields(); renderStatus();
  }
  function draft() {
    let aliases;
    try { aliases=state.aliasesFromText($('#aliases').value); }
    catch (cause) { reveal($('#aliases')); throw cause; }
    return {...saved, device:$('#device').value, root:$('#root').value,
      localModel:$('#local-model').value, meetingAutoASR:$('#settings-meeting-auto').checked, deskAutoASR:$('#settings-desk-auto').checked,
      voiceThreshold:Number($('#voice-threshold').value), hotwords:$('#hotwords').value, corrections:$('#corrections').value, aliases,
      titleEndpoint:$('#title-endpoint').value.trim(), titleModel:$('#title-model').value.trim(),
      asr:{...saved.asr,kind:$('#kind').value,endpoint:$('#endpoint').value.trim(),model:$('#model').value.trim(),responseFormat:$('#format').value || 'json',resourceId:$('#resource').value.trim()}};
  }
  function renderStatus() {
    const model=state.modelInfo($('#local-model').value,models), ready=models.find(item => item.id === $('#local-model').value)?.installed;
    const installing=!!setup?.installing;
    $('#model-description').textContent=model.note;
    $('#local-model-status').textContent=!setup ? '正在检查模型…' : ready ? '模型已就绪' : '未安装';
    $('#prepare-model').hidden=!!ready; $('#prepare-model').disabled=!saved || pending || installing;
    $('#local-model').disabled=installing || pending;
    $('#save-settings').disabled=!saved || saving || pending || installing;
    const progress=installing ? `${setup.name} · ${Math.round(setup.downloaded / 1000000)} / ${Math.round(setup.total / 1000000)} MB` : '';
    $('#recording-readiness').textContent=!setup ? '正在检查录音组件…' : installing ? progress : setup?.deskReady ? '录音与守候组件已就绪' : setup?.captureReady ? '录音组件就绪 · 守候组件未安装' : '录音组件未安装';
    $('#prepare-recording').hidden=!setup || !!setup?.deskReady;
    $('#prepare-recording').textContent=setup?.captureReady ? '准备守候' : '准备录音与守候';
    $('#prepare-recording').disabled=!saved || installing || pending;
    $('#advanced-capture-install').hidden=!!setup?.deskReady; $('#advanced-capture-install').disabled=installing || pending;
    $('#repair').hidden=!!ready; $('#repair').disabled=!saved || installing || pending;
    $('#components-summary').textContent=installing ? '安装中' : setup?.deskReady && ready ? '已就绪' : '可按需准备';
    $('#component-status').textContent=installing ? progress : `录音：${setup?.captureReady ? '已就绪' : '未准备'} · 桌边守候：${setup?.deskReady ? '已就绪' : '未准备'} · ${model.name}：${ready ? '已就绪' : '未安装'}`;
    $('#folder-note').classList.toggle('visually-hidden', !saved || $('#root').value === saved.root);
    $('#device-note').classList.toggle('visually-hidden', $('#device').value === 'default' || devicesLoaded);
    $('#device-note').textContent=$('#device').value === 'default' ? '跟随 macOS 当前选择的输入设备。' : devicesLoaded ? '使用所选麦克风。更换设备后可刷新列表。' : setup?.captureReady ? '设备名称暂不可用，可刷新或使用系统默认。' : '保留上次的设备选择，准备组件后可刷新名称。';
  }
  async function refreshDevices(silent = false) {
    $('#devices').disabled=true;
    try {
      const devices=await call('Devices');
      const value=$('#device').value;
      const options=[option('default','跟随系统默认麦克风 · 推荐'),...(devices || []).map(device => option(device.id,device.name))];
      if (!options.some(item => item.value === value)) options.push(option(value,`上次选择的麦克风 ${value} · 当前不可用`));
      $('#device').replaceChildren(...options); $('#device').value=value; devicesLoaded=true; renderStatus();
    } catch (cause) { if (!silent) error('未能读取麦克风列表，请先准备录音组件。'); }
    finally { $('#devices').disabled=false; }
  }
  async function installModel() {
    clearError();pending=true;renderStatus();
    const model=$('#local-model').value;
    try { await call('InstallLocalModel',model);saved={...saved,localModel:model};if(baseline)baseline.localModel=model;note('正在安装模型。'); await refresh(); }
    catch (cause) { error(cause); }
    finally {pending=false;renderStatus();}
  }
  async function installCapture() {
    clearError();pending=true;renderStatus();
    try {await call('InstallDeskTools');note('正在准备录音与守候组件。');await refresh();}
    catch(cause){error(cause);}
    finally{pending=false;renderStatus();}
  }
  async function refresh() {
    const wasInstalling=!!setup?.installing;
    [setup,models]=await Promise.all([call('SetupStatus'),call('ModelChoices')]);
    state.fillModels($('#local-model'),models);
    renderStatus();
    if (wasInstalling && !setup.installing) {
      if (setup.error) error(setup.error); else note('组件已就绪。');
      if (setup.captureReady && !devicesLoaded) await refreshDevices(true);
    }
  }
  async function start() {
    $('.settings-panels').inert=true;
    for (const name of ['basic','advanced']) $(`#settings-tab-${name}`).onclick=()=>selectTab(name);
    $('.settings-tabs').onkeydown=event=>{
      if (!['ArrowLeft','ArrowRight','Home','End'].includes(event.key)) return;
      event.preventDefault();
      const next=event.key === 'Home' ? 'basic' : event.key === 'End' ? 'advanced' : $('#settings-basic').hidden ? 'basic' : 'advanced';selectTab(next,true);
    };
    $('#kind').onchange=()=>{
      if ($('#kind').value === 'firered2-llm') {$('#endpoint').value='http://localhost:8000/v1';$('#model').value='allendou/FireRedASR2-LLM-vllm';$('#format').value='json';}
      else if ($('#kind').value === 'volc') {$('#endpoint').value='https://openspeech.bytedance.com';$('#resource').value='volc.seedasr.auc';}
      else if ($('#kind').value === 'compatible' && $('#endpoint').value === 'https://openspeech.bytedance.com') $('#endpoint').value='';
      $('#key').value=$('#app-key').value='';providerFields();
    };
    $('#devices').onclick=()=>refreshDevices();
    $('#choose-root').onclick=async()=>{try{const path=await call('ChooseFolder');if(path){$('#root').value=path;dirty=true;note('未保存');}}catch(cause){error(cause);}};
    $('#manage-provider').onclick=()=>{selectTab('advanced');$('#provider-section').open=true;$('#kind').focus();};
    $('#prepare-model').onclick=$('#repair').onclick=installModel;
    $('#prepare-recording').onclick=$('#advanced-capture-install').onclick=installCapture;
    $('#local-model').onchange=renderStatus;
    $('#reset-threshold').onclick=()=>{$('#voice-threshold').value=.6;dirty=true;providerFields();note('阈值已恢复，待保存。');};
    $('#test-title').onclick=async()=>{
      if(titleTesting)return;
      titleTesting=true;const version=++titleTestVersion;
      $('#test-title').disabled=true;$('#title-test-status').textContent='正在测试…';
      try {await call('TestTitleService',$('#title-endpoint').value.trim(),$('#title-model').value.trim(),$('#title-key').value);if(version===titleTestVersion)$('#title-test-status').textContent='连接成功，模型已返回文本。';}
      catch(cause){if(version===titleTestVersion)$('#title-test-status').textContent=String(cause?.message || cause);}
      finally{titleTesting=false;$('#test-title').disabled=false;}
    };
    for(const id of ['title-endpoint','title-model','title-key']) $(`#${id}`).addEventListener('input',()=>{titleTestVersion++;$('#title-test-status').textContent='';});
    $('#local-titles').onclick=()=>{$('#title-endpoint').value=$('#title-model').value=$('#title-key').value='';titleTestVersion++;$('#title-test-status').textContent='';dirty=true;providerFields();note('命名方式待保存。');};
    $('#recommended-settings').onclick=()=>{
      try {fill(state.recommended(draft()));dirty=true;$('#key').value=$('#app-key').value='';clearError();note('推荐设置待保存。');}
      catch(cause){error(cause);}
    };
    $('#settings-form').addEventListener('input',()=>{dirty=true;clearError();providerFields();note('未保存');});
    $('#settings-form').addEventListener('change',()=>{dirty=true;clearError();providerFields();renderStatus();note('未保存');});
    $('#settings-form').onsubmit=async event=>{
      event.preventDefault();clearError();
      const invalid=[...$('#settings-form').elements].find(field=>field.willValidate && !field.validity.valid);
      if(invalid){reveal(invalid);invalid.reportValidity();return;}
      let edited;try{edited=draft();}catch(cause){error(cause);return;}
      saving=true;$('.settings-panels').inert=true;renderStatus();
      try {const latest=await call('GetSettings');const value=state.mergeChanges(baseline,edited,latest);if($('#key').value || $('#app-key').value)value.asr={...edited.asr};if($('#title-key').value){value.titleEndpoint=edited.titleEndpoint;value.titleModel=edited.titleModel;}await call('SaveSettings',value,$('#key').value,$('#app-key').value,$('#title-key').value);saved=value;fill(saved,true);baseline=draft();dirty=false;note('设置已保存。');}
      catch(cause){error(cause);}
      finally{saving=false;$('.settings-panels').inert=false;renderStatus();}
    };
    try {[saved,models]=await Promise.all([call('GetSettings'),call('ModelChoices')]);state.fillModels($('#local-model'),models);fill(saved,true);baseline=draft();await refresh();$('.settings-panels').inert=false;note('');if(setup.captureReady) await refreshDevices(true);}
    catch(cause){error(cause);note('无法读取设置，请重新打开应用。');}
    root.addEventListener('focus',async()=>{if(!saved||dirty||saving||pending)return;try{const latest=await call('GetSettings');if(dirty||saving||pending)return;saved=latest;fill(saved,true);baseline=draft();}catch{}});
    const poll=async()=>{try{await refresh();}catch{}setTimeout(poll,1500);};setTimeout(poll,1500);
  }
  root.MettingsPreferences={start};
})(window);
