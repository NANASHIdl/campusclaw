'use strict';

const el = (id) => document.getElementById(id);

const state = {
  token: sessionStorage.getItem('cc_token') || '',
  user: null,
  tab: 'chat',
  conversationId: null,
  agents: [],
  skills: [],
  materials: [],
};

// --- 基础设施 ---------------------------------------------------------------

function showMessage(text, kind) {
  const box = el('message');
  if (!text) {
    box.hidden = true;
    return;
  }
  box.textContent = text;
  box.className = 'message ' + (kind || 'info');
  box.hidden = false;
}

function roleLabel(role) {
  return role === 'teacher' ? '教师' : '学生';
}

function authHeaders(extra) {
  const h = Object.assign({}, extra || {});
  if (state.token) h.Authorization = 'Bearer ' + state.token;
  return h;
}

async function api(path, options) {
  const opts = Object.assign({ credentials: 'same-origin' }, options || {});
  opts.headers = authHeaders(opts.headers);
  const res = await fetch(path, opts);
  let body = null;
  const ct = res.headers.get('content-type') || '';
  if (ct.includes('application/json')) body = await res.json().catch(() => null);
  if (res.status === 401) {
    clearSession();
    renderLogin();
    showMessage('登录已过期，请重新登录。', 'error');
  }
  return { status: res.status, ok: res.ok, body };
}

function postJSON(path, payload) {
  return api(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  });
}

function putJSON(path, payload) {
  return api(path, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  });
}

function el2(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined && text !== null) node.textContent = text;
  return node;
}

function fmt(n, d) {
  if (n === null || n === undefined) return '—';
  return Number(n).toFixed(d === undefined ? 4 : d);
}

function clearSession() {
  state.token = '';
  state.user = null;
  sessionStorage.removeItem('cc_token');
}

// --- 视图切换 ---------------------------------------------------------------

function switchTab(name) {
  state.tab = name;
  for (const btn of document.querySelectorAll('#tabs .tab')) {
    btn.classList.toggle('is-active', btn.dataset.tab === name);
  }
  for (const t of ['chat', 'agents', 'skills', 'mcp', 'materials', 'search']) {
    el('tab-' + t).hidden = t !== name;
  }
  if (name === 'agents') loadAgents();
  if (name === 'skills') loadSkills();
  if (name === 'mcp') loadMCP();
  if (name === 'chat') loadChatSelectors();
}

function renderLogin() {
  el('view-login').hidden = false;
  el('view-app').hidden = true;
  el('who').hidden = true;
  el('login-form').reset();
}

function renderApp(user) {
  state.user = user;
  el('view-login').hidden = true;
  el('view-app').hidden = false;
  el('who').hidden = false;
  el('who-text').textContent = `${user.username} · ${roleLabel(user.role)} · 班级 ${user.class_id}`;
  el('scope-note').textContent = `列表仅展示你所属班级（班级 ${user.class_id}）的材料；检索与问答同样只在会话班级内进行。`;

  const isTeacher = user.role === 'teacher';
  el('upload-block').hidden = !isTeacher;
  el('tab-btn-mcp').hidden = !isTeacher;
  if (!isTeacher && state.tab === 'mcp') state.tab = 'chat';
  el('readonly-note').hidden = isTeacher;
  el('agent-form-card').hidden = true;
  el('skill-form-card').hidden = true;
  switchTab(state.tab);
}

// --- 登录 -------------------------------------------------------------------

el('login-form').addEventListener('submit', async (e) => {
  e.preventDefault();
  showMessage('');
  const { status, body } = await api('/api/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      username: el('username').value.trim(),
      password: el('password').value,
    }),
  });
  if (status === 200 && body && body.token) {
    state.token = body.token;
    sessionStorage.setItem('cc_token', body.token);
    showMessage('');
    renderApp(body.user);
    return;
  }
  // 账号不存在 / 口令错误 / 限流对外同构，前端也只给一句提示。
  showMessage('登录失败：用户名或密码错误。', 'error');
});

el('logout').addEventListener('click', async () => {
  await api('/api/logout', { method: 'POST' });
  clearSession();
  renderLogin();
  showMessage('已退出登录。', 'info');
});

el('tabs').addEventListener('click', (e) => {
  const btn = e.target.closest('.tab');
  if (btn) switchTab(btn.dataset.tab);
});

// --- 材料 -------------------------------------------------------------------

async function loadMaterials() {
  const { status, body } = await api('/api/materials');
  if (status !== 200) {
    if (status !== 401) showMessage('加载材料列表失败。', 'error');
    return;
  }
  const items = (body && body.materials) || [];
  state.materials = items;
  const tbody = el('materials').querySelector('tbody');
  tbody.innerHTML = '';
  el('list-title').textContent = `材料列表（${items.length}）`;
  el('empty').hidden = items.length > 0;

  for (const m of items) {
    const tr = document.createElement('tr');
    tr.appendChild(el2('td', '', String(m.id)));
    tr.appendChild(el2('td', '', m.title));
    tr.appendChild(el2('td', '', m.uploader || '—'));
    tr.appendChild(el2('td', '', m.created_at || ''));

    const actions = document.createElement('td');
    const a = document.createElement('a');
    a.href = '#';
    a.textContent = '下载';
    a.addEventListener('click', (ev) => {
      ev.preventDefault();
      downloadMaterial(m.id, m.title);
    });
    actions.appendChild(a);

    const btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'link';
    btn.textContent = '切片与向量';
    btn.addEventListener('click', () => showVectors(m.id, m.title));
    actions.appendChild(btn);
    tr.appendChild(actions);
    tbody.appendChild(tr);
  }
}

// 下载也要带令牌，因此不能直接用 <a href>：先取 blob 再触发保存。
async function downloadMaterial(id, title) {
  const res = await fetch(`/api/materials/${id}/file`, { headers: authHeaders() });
  if (!res.ok) {
    showMessage(res.status === 404 ? '材料不存在或不属于本班。' : '下载失败。', 'error');
    return;
  }
  const blob = await res.blob();
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = title || 'material';
  document.body.appendChild(a);
  a.click();
  a.remove();
  URL.revokeObjectURL(url);
}

async function showVectors(materialId, title) {
  const { status, body } = await api(`/api/materials/${materialId}/vectors`);
  if (status === 404) {
    showMessage('材料不存在，或不属于本班（对外统一为 404）。', 'error');
    return;
  }
  if (status !== 200 || !body) {
    showMessage('读取切片与向量失败。', 'error');
    return;
  }
  el('vectors-card').hidden = false;
  el('vectors-title').textContent = `切片与向量 · ${title}`;
  el('vectors-meta').textContent =
    `集合 ${body.collection || '—'} · 距离 ${body.distance || '—'} · 维度 ${body.dim} · material_id=${body.material_id}`;

  const box = el('vectors-body');
  box.innerHTML = '';
  const chunks = body.chunks || [];
  if (chunks.length === 0) {
    box.appendChild(el2('p', 'empty', '这份材料还没有切片。'));
    return;
  }
  for (const c of chunks) {
    const item = el2('div', 'chunk');
    const head = el2('div', 'chunk-head');
    head.appendChild(el2('span', 'badge', `切片 #${c.chunk_index}`));
    head.appendChild(el2('span', 'mono', `字符 ${c.start_offset}–${c.end_offset}`));
    head.appendChild(el2('span', 'badge ' + (c.index_status === 'ready' ? 'ok' : 'warn'),
      c.index_status === 'ready' ? '已入库' : '向量失败'));
    head.appendChild(el2('span', 'mono', c.linked ? `${c.dim} 维 · L2 ${fmt(c.norm, 3)}` : '向量库中暂无记录'));
    item.appendChild(head);
    item.appendChild(el2('p', 'excerpt', c.excerpt));
    box.appendChild(item);
  }
}

// 分段策略卡片
function selectedStrategy() {
  const checked = document.querySelector('input[name=chunk_strategy]:checked');
  return checked ? checked.value : 'auto';
}

function syncStrategyUI() {
  const cur = selectedStrategy();
  for (const card of document.querySelectorAll('.strategy-card')) {
    card.classList.toggle('is-active', card.dataset.strategy === cur);
  }
  for (const box of document.querySelectorAll('.strategy-fields')) {
    box.hidden = box.dataset.fields !== cur;
  }
}

function initStrategyCards() {
  for (const input of document.querySelectorAll('input[name=chunk_strategy]')) {
    input.addEventListener('change', syncStrategyUI);
  }
  syncStrategyUI();
}

function appendChunkParams(data) {
  const s = selectedStrategy();
  data.append('chunk_strategy', s);
  if (s === 'custom') {
    data.append('separator', el('custom-separator').value);
    data.append('max_chars', el('custom-max').value);
    data.append('overlap_percent', el('custom-overlap').value);
    if (el('custom-collapse').checked) data.append('collapse_whitespace', '1');
    if (el('custom-removeurls').checked) data.append('remove_urls', '1');
  } else if (s === 'hierarchy') {
    data.append('max_chars', el('hierarchy-max').value);
    data.append('overlap_percent', el('hierarchy-overlap').value);
  }
}

el('upload-form').addEventListener('submit', async (e) => {
  e.preventDefault();
  const form = e.target;
  const file = el('upload-file').files[0];
  if (!file) {
    showMessage('请选择一个 .txt 或 .md 文件。', 'error');
    return;
  }
  const strategy = selectedStrategy();
  const data = new FormData();
  data.append('file', file);
  appendChunkParams(data);

  const submit = form.querySelector('button[type=submit]');
  submit.disabled = true;
  submit.textContent = '上传中…';

  const res = await fetch('/api/materials/upload', {
    method: 'POST',
    body: data,
    headers: authHeaders(),
  });
  submit.disabled = false;
  submit.textContent = '上传并入库';

  if (res.status === 201) {
    const body = await res.json().catch(() => null);
    form.reset();
    syncStrategyUI();
    showMessage(`上传成功，建立 ${body ? body.chunk_count : 0} 条切片。`, 'info');
    await loadMaterials();
    return;
  }
  let msg = '上传失败。';
  if (res.status === 403) msg = '上传被拒绝（403）：仅教师可上传。';
  else if (res.status === 400) {
    const b = await res.json().catch(() => null);
    msg = b && b.error ? `上传失败（400）：${b.error}` : '上传失败（400）。';
  }
  showMessage(msg, 'error');
});

// --- 检索 -------------------------------------------------------------------

function hitCard(h, index) {
  const item = el2('div', 'hit');
  const head = el2('div', 'hit-head');
  head.appendChild(el2('span', 'badge', `#${index + 1}`));
  head.appendChild(el2('span', 'hit-title', h.material_title));
  head.appendChild(el2('span', 'mono', `切片 #${h.chunk_index} · 字符 ${h.start_offset}–${h.end_offset}`));
  item.appendChild(head);
  item.appendChild(el2('p', 'excerpt', h.snippet));

  const parts = [`合成分 ${fmt(h.score_final, 6)}`];
  if (h.rank_keyword != null) parts.push(`关键字第 ${h.rank_keyword} 名（全文分 ${fmt(h.score_keyword, 2)}）`);
  if (h.rank_vector != null) parts.push(`向量第 ${h.rank_vector} 名（余弦 ${fmt(h.score_vector, 3)}）`);
  item.appendChild(el2('p', 'hint', parts.join(' ｜ ')));
  return item;
}

async function runSearch() {
  const q = el('search-q').value.trim();
  const mode = el('search-mode').value;
  if (q.length < 2) {
    showMessage('查询至少需要 2 个字符。', 'error');
    return;
  }
  showMessage('');
  el('search-result-card').hidden = false;
  el('search-result-title').textContent = '检索中…';

  const { status, body } = await postJSON('/api/search', { q, mode });
  if (status === 400) {
    el('search-result-title').textContent = '检索结果';
    showMessage((body && body.error) || '请求不合法。', 'error');
    return;
  }
  if (status === 503) {
    el('search-result-title').textContent = '检索结果';
    showMessage('向量库不可用：向量与混合模式返回 503，可改用「仅关键字」。', 'error');
    return;
  }
  if (status !== 200 || !body) {
    el('search-result-title').textContent = '检索结果';
    showMessage('检索失败。', 'error');
    return;
  }

  const hits = body.hits || [];
  el('search-result-title').textContent = `检索结果（${hits.length}）`;
  el('search-note').textContent = body.note || '';

  const vecBox = el('search-vector');
  vecBox.innerHTML = '';
  if (body.query_vector) {
    vecBox.appendChild(el2('p', 'hint',
      `问句「${q}」先被嵌入成 ${body.query_vector.dim} 维向量（L2 范数 ${fmt(body.query_vector.norm, 3)}），再与切片向量算余弦。`));
  } else {
    vecBox.appendChild(el2('p', 'hint', '本次没有计算向量：关键字路径把问句原文交给全文索引。'));
  }

  const hitsBox = el('search-hits');
  hitsBox.innerHTML = '';
  if (hits.length === 0) {
    hitsBox.appendChild(el2('p', 'empty', '资料中未找到相关内容。'));
    return;
  }
  hits.forEach((h, i) => hitsBox.appendChild(hitCard(h, i)));
}

el('search-form').addEventListener('submit', (e) => {
  e.preventDefault();
  runSearch();
});

document.addEventListener('click', (e) => {
  const link = e.target.closest('.example');
  if (!link) return;
  e.preventDefault();
  el('search-q').value = link.dataset.q;
  switchTab('search');
  runSearch();
});

// --- 智能体 -----------------------------------------------------------------

async function loadAgents() {
  const [{ status, body }, sres, mres] = await Promise.all([
    api('/api/agents'), api('/api/skills'), api('/api/materials'),
  ]);
  if (status !== 200) return;
  state.agents = (body && body.agents) || [];
  state.skills = (sres.body && sres.body.skills) || [];
  state.materials = (mres.body && mres.body.materials) || [];

  const tbody = el('agents-table').querySelector('tbody');
  tbody.innerHTML = '';
  el('agents-empty').hidden = state.agents.length > 0;

  for (const a of state.agents) {
    const tr = document.createElement('tr');
    tr.appendChild(el2('td', '', String(a.id)));
    tr.appendChild(el2('td', '', a.name));
    tr.appendChild(el2('td', 'mono', (a.prompt || '（空，回退默认角色）').slice(0, 24)));
    tr.appendChild(el2('td', 'mono', String((a.skill_ids || []).length)));
    tr.appendChild(el2('td', 'mono', String((a.material_ids || []).length)));
    tr.appendChild(el2('td', '', a.enabled ? '已启用' : '已停用'));

    const actions = document.createElement('td');
    if (state.user && state.user.role === 'teacher') {
      const btn = document.createElement('button');
      btn.type = 'button';
      btn.className = 'link';
      btn.textContent = '编辑';
      btn.addEventListener('click', () => openAgentForm(a));
      actions.appendChild(btn);
    }
    tr.appendChild(actions);
    tbody.appendChild(tr);
  }

  if (state.user && state.user.role === 'teacher') {
    el('agent-form-card').hidden = false;
    if (!el('agent-id').value) openAgentForm(null);
  }
}

function fillMulti(select, items, selected, label) {
  select.innerHTML = '';
  for (const it of items) {
    const opt = document.createElement('option');
    opt.value = String(it.id);
    opt.textContent = label(it);
    opt.selected = (selected || []).includes(it.id);
    select.appendChild(opt);
  }
}

function openAgentForm(agent) {
  el('agent-id').value = agent ? String(agent.id) : '';
  el('agent-form-title').textContent = agent ? `编辑智能体 #${agent.id}` : '新建智能体';
  el('agent-name').value = agent ? agent.name : '';
  el('agent-prompt').value = agent ? agent.prompt : '';
  el('agent-enabled').checked = agent ? agent.enabled : true;
  fillMulti(el('agent-skills'), state.skills, agent ? agent.skill_ids : [], (s) => `${s.name}（${s.trigger}）`);
  fillMulti(el('agent-materials'), state.materials, agent ? agent.material_ids : [], (m) => m.title);
  el('agent-form-card').hidden = false;
}

function selectedValues(select) {
  return Array.from(select.selectedOptions).map((o) => Number(o.value));
}

el('agent-cancel').addEventListener('click', () => {
  el('agent-form-card').hidden = true;
});

el('agent-form').addEventListener('submit', async (e) => {
  e.preventDefault();
  const id = el('agent-id').value;
  const payload = {
    name: el('agent-name').value.trim(),
    prompt: el('agent-prompt').value,
    enabled: el('agent-enabled').checked,
    skill_ids: selectedValues(el('agent-skills')),
    material_ids: selectedValues(el('agent-materials')),
  };
  const res = id ? await putJSON(`/api/agents/${id}`, payload) : await postJSON('/api/agents', payload);
  if (res.status === 200 || res.status === 201) {
    showMessage('智能体已保存。', 'info');
    await loadAgents();
    return;
  }
  const msg = (res.body && res.body.error) || (res.status === 404 ? '绑定的技能或材料不属于本班。' : '保存失败。');
  showMessage(msg, 'error');
});

// --- 技能 -------------------------------------------------------------------

async function loadSkills() {
  const { status, body } = await api('/api/skills');
  if (status !== 200) return;
  state.skills = (body && body.skills) || [];
  const tbody = el('skills-table').querySelector('tbody');
  tbody.innerHTML = '';
  el('skills-empty').hidden = state.skills.length > 0;

  for (const s of state.skills) {
    const tr = document.createElement('tr');
    tr.appendChild(el2('td', '', String(s.id)));
    tr.appendChild(el2('td', '', s.name));
    tr.appendChild(el2('td', 'mono', s.trigger));
    tr.appendChild(el2('td', '', s.subject || '—'));
    tr.appendChild(el2('td', '', s.enabled ? '已启用' : '已停用'));

    const actions = document.createElement('td');
    if (state.user && state.user.role === 'teacher') {
      const edit = document.createElement('button');
      edit.type = 'button';
      edit.className = 'link';
      edit.textContent = '编辑';
      edit.addEventListener('click', () => openSkillForm(s));
      actions.appendChild(edit);

      const del = document.createElement('button');
      del.type = 'button';
      del.className = 'link';
      del.textContent = '删除';
      del.addEventListener('click', () => deleteSkill(s));
      actions.appendChild(del);
    }
    tr.appendChild(actions);
    tbody.appendChild(tr);
  }

  if (state.user && state.user.role === 'teacher') {
    el('skill-form-card').hidden = false;
    if (!el('skill-id').value) openSkillForm(null);
  }
}

function openSkillForm(skill) {
  el('skill-id').value = skill ? String(skill.id) : '';
  el('skill-form-title').textContent = skill ? `编辑技能 #${skill.id}` : '新建技能';
  el('skill-source').value = skill ? skill.source : '名称：\n适用条件：\n资料：\n工具：knowledge_search\n启用：是\n\n';
  el('skill-form-card').hidden = false;
}

async function deleteSkill(skill) {
  if (!confirm(`确定删除技能「${skill.name}」？绑定它的智能体会自动解绑。`)) return;
  const res = await api(`/api/skills/${skill.id}`, { method: 'DELETE' });
  if (res.status === 204) {
    showMessage('技能已删除。', 'info');
    await loadSkills();
    return;
  }
  showMessage((res.body && res.body.error) || '删除失败。', 'error');
}

el('skill-cancel').addEventListener('click', () => {
  el('skill-form-card').hidden = true;
});

el('skill-form').addEventListener('submit', async (e) => {
  e.preventDefault();
  const id = el('skill-id').value;
  const payload = { source: el('skill-source').value };
  const res = id ? await putJSON(`/api/skills/${id}`, payload) : await postJSON('/api/skills', payload);
  if (res.status === 200 || res.status === 201) {
    showMessage('技能已保存。', 'info');
    await loadSkills();
    return;
  }
  showMessage((res.body && res.body.error) || '保存失败。', 'error');
});

// --- 工具服务（第 6 课） ------------------------------------------------------

function renderMCPServers(servers) {
  const tbody = el('mcp-table').querySelector('tbody');
  tbody.innerHTML = '';
  el('mcp-empty').hidden = servers.length > 0;

  for (const s of servers) {
    const tr = document.createElement('tr');
    tr.appendChild(el2('td', '', String(s.id)));
    tr.appendChild(el2('td', '', s.name));
    tr.appendChild(el2('td', 'mono', s.base_url));
    tr.appendChild(el2('td', '', s.has_auth ? '已配置' : '未配置'));
    tr.appendChild(el2('td', '', s.connected ? '已连接' : '未连接'));
    tr.appendChild(el2('td', 'mono', String(s.tool_count)));
    tr.appendChild(el2('td', 'mono', s.last_error || '—'));

    const actions = document.createElement('td');
    const add = (label, handler) => {
      const b = document.createElement('button');
      b.type = 'button';
      b.className = 'link';
      b.textContent = label;
      b.addEventListener('click', handler);
      actions.appendChild(b);
    };
    add(s.connected ? '重新连接' : '连接并发现', () => connectMCP(s));
    if (s.connected) add('断开', () => disconnectMCP(s));
    add('查看工具', () => viewMCPTools(s));
    add('编辑', () => openMCPForm(s));
    add('删除', () => deleteMCP(s));
    tr.appendChild(actions);
    tbody.appendChild(tr);
  }
}

function renderMCPCalls(calls) {
  const tbody = el('mcp-calls-table').querySelector('tbody');
  tbody.innerHTML = '';
  el('mcp-calls-empty').hidden = calls.length > 0;
  for (const c of calls) {
    const tr = document.createElement('tr');
    tr.appendChild(el2('td', 'mono', c.created_at));
    tr.appendChild(el2('td', 'mono', c.tool_ref));
    const statusLabel = c.status === 'ok' ? '成功' : (c.status === 'denied' ? '被拒' : '出错');
    tr.appendChild(el2('td', '', statusLabel));
    tr.appendChild(el2('td', 'mono', String(c.user_id)));
    tr.appendChild(el2('td', 'mono', c.conversation_id ? String(c.conversation_id) : '—'));
    tr.appendChild(el2('td', 'mono', String(c.round_no)));
    tr.appendChild(el2('td', 'mono', c.detail || ''));
    tbody.appendChild(tr);
  }
}

async function loadMCP() {
  const [sres, cres] = await Promise.all([api('/api/mcp/servers'), api('/api/mcp/tool-calls')]);
  if (sres.status !== 200) return;
  renderMCPServers((sres.body && sres.body.servers) || []);
  renderMCPCalls((cres.body && cres.body.calls) || []);
}

function openMCPForm(server) {
  el('mcp-id').value = server ? String(server.id) : '';
  el('mcp-form-title').textContent = server ? `编辑工具服务 #${server.id}` : '登记工具服务';
  el('mcp-name').value = server ? server.name : '';
  el('mcp-base').value = server ? server.base_url : '';
  el('mcp-token').value = '';
  el('mcp-form-card').hidden = false;
}

async function connectMCP(server) {
  const res = await postJSON(`/api/mcp/servers/${server.id}/connect`, {});
  if (res.status === 200) {
    const n = ((res.body && res.body.tools) || []).length;
    showMessage(`已连接「${server.name}」，发现 ${n} 个工具。`, 'info');
    await loadMCP();
    return;
  }
  showMessage((res.body && res.body.error) || '连接失败。', 'error');
  await loadMCP();
}

async function disconnectMCP(server) {
  const res = await postJSON(`/api/mcp/servers/${server.id}/disconnect`, {});
  if (res.status === 200) {
    showMessage(`已断开「${server.name}」。`, 'info');
    await loadMCP();
    return;
  }
  showMessage((res.body && res.body.error) || '断开失败。', 'error');
}

async function viewMCPTools(server) {
  const res = await api(`/api/mcp/servers/${server.id}/tools`);
  if (res.status !== 200) {
    showMessage((res.body && res.body.error) || '读取工具清单失败。', 'error');
    return;
  }
  const tools = (res.body && res.body.tools) || [];
  el('mcp-tools-title').textContent = `已发现的工具 · ${server.name}（${tools.length}）`;
  const tbody = el('mcp-tools-table').querySelector('tbody');
  tbody.innerHTML = '';
  for (const t of tools) {
    const tr = document.createElement('tr');
    tr.appendChild(el2('td', 'mono', t.name));
    tr.appendChild(el2('td', '', t.description || ''));
    tr.appendChild(el2('td', 'mono', JSON.stringify(t.input_schema || {})));
    tbody.appendChild(tr);
  }
  el('mcp-tools-card').hidden = false;
}

async function deleteMCP(server) {
  if (!confirm(`确定删除工具服务「${server.name}」？已发现的工具会一并清空，历史调用记录保留。`)) return;
  const res = await api(`/api/mcp/servers/${server.id}`, { method: 'DELETE' });
  if (res.status === 204) {
    showMessage('已删除。', 'info');
    await loadMCP();
    return;
  }
  showMessage((res.body && res.body.error) || '删除失败。', 'error');
}

el('mcp-cancel').addEventListener('click', () => {
  el('mcp-form-card').hidden = true;
});

el('mcp-form').addEventListener('submit', async (e) => {
  e.preventDefault();
  const id = el('mcp-id').value;
  const payload = {
    name: el('mcp-name').value.trim(),
    base_url: el('mcp-base').value.trim(),
    auth_token: el('mcp-token').value,
  };
  const res = id
    ? await putJSON(`/api/mcp/servers/${id}`, payload)
    : await postJSON('/api/mcp/servers', payload);
  // 令牌输入框提交后立刻清空，不回显。
  el('mcp-token').value = '';
  if (res.status === 200 || res.status === 201) {
    showMessage('已保存。', 'info');
    await loadMCP();
    return;
  }
  showMessage((res.body && res.body.error) || '保存失败。', 'error');
});

// --- 对话 -------------------------------------------------------------------

async function loadChatSelectors() {
  const [ares, cres] = await Promise.all([api('/api/agents'), api('/api/conversations')]);
  const agents = (ares.body && ares.body.agents) || [];
  const convs = (cres.body && cres.body.conversations) || [];

  const agentSel = el('conv-agent');
  agentSel.innerHTML = '';
  for (const a of agents) {
    const opt = document.createElement('option');
    opt.value = String(a.id);
    opt.textContent = a.name;
    agentSel.appendChild(opt);
  }
  if (agents.length === 0) {
    const opt = document.createElement('option');
    opt.value = '';
    opt.textContent = '本班暂无可用智能体';
    agentSel.appendChild(opt);
  }

  const convSel = el('conv-list');
  convSel.innerHTML = '';
  const placeholder = document.createElement('option');
  placeholder.value = '';
  placeholder.textContent = convs.length ? '选择已有会话…' : '还没有会话';
  convSel.appendChild(placeholder);
  for (const c of convs) {
    const opt = document.createElement('option');
    opt.value = String(c.id);
    opt.textContent = `${c.title}（${c.updated_at}）`;
    convSel.appendChild(opt);
  }
  convSel.value = state.conversationId ? String(state.conversationId) : '';
}

el('conv-new').addEventListener('click', async () => {
  const agentId = Number(el('conv-agent').value);
  if (!agentId) {
    showMessage('请先选择一个智能体。', 'error');
    return;
  }
  const res = await postJSON('/api/conversations', { agent_id: agentId });
  if (res.status === 201 && res.body) {
    state.conversationId = res.body.id;
    showMessage('会话已创建。', 'info');
    await loadChatSelectors();
    await openConversation(res.body.id);
    return;
  }
  showMessage((res.body && res.body.error) || '新建会话失败。', 'error');
});

el('conv-list').addEventListener('change', async (e) => {
  const id = Number(e.target.value);
  if (!id) return;
  state.conversationId = id;
  await openConversation(id);
});

function renderMessage(m) {
  const item = el2('div', 'msg ' + (m.role === 'user' ? 'msg-user' : 'msg-bot'));
  item.appendChild(el2('div', 'msg-role', m.role === 'user' ? '我' : '助手'));
  item.appendChild(el2('div', 'msg-body', m.content));
  if (m.citations && m.citations.length) {
    const box = el2('div', 'msg-cites');
    m.citations.forEach((c, i) => {
      box.appendChild(el2('span', 'badge', `[${i + 1}] ${c.material_title} · 切片 ${c.chunk_index}`));
    });
    item.appendChild(box);
  }
  return item;
}

async function openConversation(id) {
  const { status, body } = await api(`/api/conversations/${id}`);
  if (status !== 200 || !body) {
    showMessage('读取会话失败。', 'error');
    return;
  }
  el('conv-card').hidden = false;
  el('conv-title').textContent = body.conversation.title;
  const box = el('conv-messages');
  box.innerHTML = '';
  for (const m of body.messages || []) box.appendChild(renderMessage(m));
  box.scrollTop = box.scrollHeight;
}

el('conv-form').addEventListener('submit', async (e) => {
  e.preventDefault();
  const input = el('conv-input');
  const content = input.value.trim();
  if (!state.conversationId) {
    showMessage('请先新建或选择一个会话。', 'error');
    return;
  }
  if (content.length < 2) {
    showMessage('问题至少需要 2 个字符。', 'error');
    return;
  }
  input.value = '';
  showMessage('');

  const box = el('conv-messages');
  box.appendChild(renderMessage({ role: 'user', content }));

  const bot = renderMessage({ role: 'assistant', content: '' });
  const bodyEl = bot.querySelector('.msg-body');
  const citesEl = el2('div', 'msg-cites');
  bot.appendChild(citesEl);
  box.appendChild(bot);
  box.scrollTop = box.scrollHeight;

  const res = await fetch(`/api/conversations/${state.conversationId}/messages`, {
    method: 'POST',
    headers: authHeaders({ 'Content-Type': 'application/json' }),
    body: JSON.stringify({ content }),
  });

  if (!res.ok) {
    let msg = '发送失败。';
    const ct = res.headers.get('content-type') || '';
    if (ct.includes('application/json')) {
      const b = await res.json().catch(() => null);
      if (b && b.error) msg = b.error;
    }
    if (res.status === 404) msg = '会话不存在。';
    if (res.status === 503) msg = '检索或模型依赖暂不可用。';
    bodyEl.textContent = '';
    showMessage(msg, 'error');
    return;
  }

  // 自行解帧：EventSource 只能发 GET，而提问要带 JSON 正文。
  const reader = res.body.getReader();
  const decoder = new TextDecoder();
  let buffer = '';

  const handleFrame = (frame) => {
    let event = 'message';
    let data = '';
    for (const line of frame.split('\n')) {
      if (line.startsWith('event:')) event = line.slice(6).trim();
      else if (line.startsWith('data:')) data += line.slice(5).trim();
    }
    if (!data) return;
    let payload;
    try {
      payload = JSON.parse(data);
    } catch (err) {
      return;
    }

    if (event === 'meta') {
      citesEl.innerHTML = '';
      (payload.citations || []).forEach((c, i) => {
        citesEl.appendChild(el2('span', 'badge', `[${i + 1}] ${c.material_title} · 切片 ${c.chunk_index}`));
      });
      if (payload.tool_calls && payload.tool_calls.length) {
        const toolsBox = el2('div', 'msg-tools');
        payload.tool_calls.forEach((t) => {
          const label = t.status === 'ok' ? '成功' : (t.status === 'denied' ? '被拒' : '出错');
          toolsBox.appendChild(el2('span', 'badge badge-tool',
            `[工具] ${t.ref} · ${label}`));
        });
        bot.insertBefore(toolsBox, citesEl);
      }
      const bits = [];
      bits.push(payload.status === 'ok' ? '已找到依据' : '本轮没有检索到资料');
      if (payload.tool_calls && payload.tool_calls.length) {
        bits.push(`工具调用 ${payload.tool_calls.length} 次`);
      }
      if (payload.skill) bits.push(`技能：${payload.skill.name}`);
      el('conv-meta').textContent = bits.join(' · ');
    } else if (event === 'delta') {
      bodyEl.textContent += payload.text || '';
      box.scrollTop = box.scrollHeight;
    } else if (event === 'error') {
      showMessage(payload.error || '生成失败，请稍后重试。', 'error');
    }
  };

  for (;;) {
    const { value, done } = await reader.read();
    if (done) break;
    buffer += decoder.decode(value, { stream: true });
    let idx;
    while ((idx = buffer.indexOf('\n\n')) >= 0) {
      const frame = buffer.slice(0, idx);
      buffer = buffer.slice(idx + 2);
      handleFrame(frame);
    }
  }
});

// --- 启动 -------------------------------------------------------------------

async function bootstrap() {
  if (!state.token) {
    renderLogin();
    return;
  }
  const { status, body } = await api('/api/me');
  if (status === 200 && body) {
    renderApp(body);
    await loadMaterials();
    await loadChatSelectors();
  } else {
    renderLogin();
  }
}

initStrategyCards();
bootstrap();
