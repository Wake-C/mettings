(function(root) {
  'use strict';
  function aliasesToText(raw) {
    const value = JSON.parse(raw || '{}');
    if (value === null) return '';
    if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('姓名别名格式不正确');
    const entries = Object.entries(value);
    if (entries.some(([a, b]) => typeof b !== 'string' || !a || !b || /[=\r\n]/.test(a) || /[\r\n]/.test(b) || a.trim() !== a || b.trim() !== b)) return JSON.stringify(value, null, 2);
    return entries.map(([a, b]) => `${a}=${b}`).join('\n');
  }
  function aliasesFromText(text) {
    const input = text.trim();
    if (!input) return '{}';
    if (input.startsWith('{')) {
      let value;
      try { value = JSON.parse(input); } catch { throw new Error('姓名别名格式不正确，请使用“别名=姓名”，每行一组。'); }
      if (!value || typeof value !== 'object' || Array.isArray(value) || Object.values(value).some(name => typeof name !== 'string')) throw new Error('姓名别名中的姓名必须是文字。');
      return JSON.stringify(value);
    }
    const pairs = [];
    for (const [index, raw] of text.split('\n').entries()) {
      const line = raw.trim(); if (!line || line.startsWith('#')) continue;
      const split = line.indexOf('=');
      if (split <= 0 || !line.slice(split + 1).trim()) throw new Error(`姓名别名第 ${index + 1} 行需要使用“别名=姓名”。`);
      pairs.push([line.slice(0, split).trim(), line.slice(split + 1).trim()]);
    }
    return JSON.stringify(Object.fromEntries(pairs));
  }
  // Apply the recording preset without erasing locations, vocabulary or service profiles.
  function recommended(settings) {
    return {...settings, device: 'default', localModel: 'qwen3', meetingAutoASR: false, deskAutoASR: false, voiceThreshold: 0.6,
      asr: {...settings.asr, kind: 'local'}};
  }
  function modelInfo(id, catalog = []) {
    const model = catalog.find(item => item.id === id);
    const bytes = model?.downloadBytes || 0;
    const size = bytes >= 1e9 ? `${(bytes / 1e9).toFixed(2)} GB` : bytes ? `${Math.ceil(bytes / 1e6)} MB` : '';
    return {name:model?.label || id, note:[model?.description, size].filter(Boolean).join(' · ')};
  }
  function fillModels(select, catalog) {
    const models = catalog.filter(item => item.id !== 'configured');
    const signature = JSON.stringify(models.map(item => [item.id,item.label,item.tier]));
    if (select.dataset.models === signature || !models.length) return;
    const value = select.value, groups = new Map();
    for (const model of models) {
      const tier = model.tier || '本地模型';
      if (!groups.has(tier)) { const group = select.ownerDocument.createElement('optgroup'); group.label = tier; groups.set(tier,group); }
      const option=select.ownerDocument.createElement('option');option.value=model.id;option.textContent=model.label;groups.get(tier).append(option);
    }
    select.replaceChildren(...groups.values()); if (models.some(item => item.id === value)) select.value=value;
    select.dataset.models = signature;
  }
  function mergeChanges(baseline, edited, latest) {
    const result = {...latest};
    for (const key of Object.keys(edited)) {
      if (key === 'asr') result.asr = mergeChanges(baseline.asr || {}, edited.asr || {}, latest.asr || {});
      else if (JSON.stringify(edited[key]) !== JSON.stringify(baseline[key])) result[key] = edited[key];
    }
    return result;
  }
  const api = {aliasesToText, aliasesFromText, recommended, modelInfo, fillModels, mergeChanges};
  root.MettingsPreferencesState = api;
  if (typeof module !== 'undefined' && module.exports) module.exports = api;
})(typeof globalThis !== 'undefined' ? globalThis : this);
