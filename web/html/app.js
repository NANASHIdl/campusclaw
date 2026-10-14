'use strict';

const el = (id) => document.getElementById(id);

const state = {
  user: null,
  tab: 'materials',
};

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

async function api(path, options) {
  const res = await fetch(path, Object.assign({ credentials: 'same-origin' }, options));
  let body = null;
  const ct = res.headers.get('content-type') || '';
  if (ct.includes('application/json')) {
    body = await res.json().catch(() => null);
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

function el2(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined && text !== null) node.textContent = text;
  return node;
}

function fmt(n, digits) {
  if (n === null || n === undefined) return '—';
  return Number(n).toFixed(digits === undefined ? 4 : digits);
}

// --- 视图切换 ---------------------------------------------------------------

function switchTab(name) {
  state.tab = name;
  for (const btn of document.querySelectorAll('#tabs .tab')) {
    btn.classList.toggle('is-active', btn.dataset.tab === name);
  }
  el('tab-materials').hidden = name !== 'materials';
  el('tab-search').hidden = name !== 'search';
  el('tab-ask').hidden = name !== 'ask';
}

function renderLogin() {
  state.user = null;
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
  el('scope-note').textContent = `列表仅展示你所属班级（班级 ${user.class_id}）的材料；检索与问答同样只在会话班级内进行，请求中自带的班级编号一律无效。`;

  const isTeacher = user.role === 'teacher';
  // 是否渲染上传入口只影响可发现性；真正的授权在服务端角色校验。
  el('upload-block').hidden = !isTeacher;
  el('readonly-note').hidden = isTeacher;
  switchTab(state.tab);
}

// --- 材料列表 ---------------------------------------------------------------

async function loadMaterials() {
  const { status, body } = await api('/api/materials');
  if (status === 401) {
    renderLogin();
    showMessage('会话已失效，请重新登录。', 'error');
    return;
  }
  if (status !== 200) {
    showMessage('加载材料列表失败。', 'error');
    return;
  }
  const items = (body && body.materials) || [];
  const tbody = el('materials').querySelector('tbody');
  tbody.innerHTML = '';
  el('list-title').textContent = `材料列表（${items.length}）`;
  el('empty').hidden = items.length > 0;

  for (const m of items) {
    const tr = document.createElement('tr');
    tr.appendChild(cell(String(m.id)));
    tr.appendChild(cell(m.title));
    tr.appendChild(cell(m.uploader || '—'));
    tr.appendChild(cell(m.created_at || ''));

    const actions = document.createElement('td');
    const download = document.createElement('a');
    download.href = `/api/materials/${m.id}/file`;
    download.textContent = '下载';
    actions.appendChild(download);

    const vectors = document.createElement('button');
    vectors.type = 'button';
    vectors.className = 'link';
    vectors.textContent = '切片与向量';
    vectors.addEventListener('click', () => showVectors(m.id, m.title));
    actions.appendChild(vectors);
    tr.appendChild(actions);
    tbody.appendChild(tr);
  }
}

function cell(text) {
  const td = document.createElement('td');
  td.textContent = text;
  return td;
}

async function showVectors(materialId, title) {
  const { status, body } = await api(`/api/materials/${materialId}/vectors`);
  if (status === 401) {
    renderLogin();
    showMessage('会话已失效，请重新登录。', 'error');
    return;
  }
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
    `集合 ${body.collection || '—'} · 距离 ${body.distance || '—'} · 维度 ${body.dim} · ` +
    `material_id=${body.material_id} · knowledge_entry_id=${body.knowledge_entry_id} · class_id=${body.class_id}`;

  const box = el('vectors-body');
  box.innerHTML = '';
  const chunks = body.chunks || [];
  if (chunks.length === 0) {
    box.appendChild(el2('p', 'empty', '这份材料还没有切片。教师上传或重建索引后，这里会出现每条向量。'));
    return;
  }

  for (const c of chunks) {
    const item = el2('div', 'chunk');
    const head = el2('div', 'chunk-head');
    head.appendChild(el2('span', 'badge', `切片 #${c.chunk_index}`));
    head.appendChild(el2('span', 'mono', `字符 ${c.start_offset}–${c.end_offset}`));
    head.appendChild(el2('span', 'badge ' + (c.index_status === 'ready' ? 'ok' : 'warn'),
      c.index_status === 'ready' ? '已入库' : '向量失败'));
    head.appendChild(el2('span', 'mono', c.linked ? `向量 ${c.dim} 维 · L2 ${fmt(c.norm, 3)}` : '向量库中暂无记录'));
    item.appendChild(head);
    item.appendChild(el2('p', 'excerpt', c.excerpt));

    const link = el2('p', 'hint');
    link.textContent = c.linked
      ? `编号一致：${c.mysql_chunk_id} 同时是 MySQL 主键、向量主键与 payload.chunk_id；` +
        `payload 里的 class_id=${c.payload_class_id}、material_id=${c.payload_material_id}、` +
        `chunk_index=${c.payload_chunk_index}，不含正文。`
      : '该切片在向量库中还没有对应记录，无法用于向量检索。';
    item.appendChild(link);
    box.appendChild(item);
  }
}

// --- 检索 -------------------------------------------------------------------

function hitCard(h, index) {
  const item = el2('div', 'hit');
  const head = el2('div', 'hit-head');
  head.appendChild(el2('span', 'badge', `#${index + 1}`));
  head.appendChild(el2('span', 'hit-title', h.material_title));
  head.appendChild(el2('span', 'mono', `切片 #${h.chunk_index} · 字符 ${h.start_offset}–${h.end_offset}`));
  item.appendChild(head);

  item.appendChild(el2('p', 'excerpt', h.snippet));

  const parts = [];
  parts.push(`合成分 ${fmt(h.score_final, 6)}`);
  if (h.rank_keyword !== null && h.rank_keyword !== undefined) {
    parts.push(`关键字第 ${h.rank_keyword} 名（全文分 ${fmt(h.score_keyword, 2)}）→ 1/(60+${h.rank_keyword})`);
  }
  if (h.rank_vector !== null && h.rank_vector !== undefined) {
    parts.push(`向量第 ${h.rank_vector} 名（余弦 ${fmt(h.score_vector, 3)}）→ 1/(60+${h.rank_vector})`);
  }
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
  if (status === 401) {
    renderLogin();
    showMessage('会话已失效，请重新登录。', 'error');
    return;
  }
  if (status === 400) {
    el('search-result-title').textContent = '检索结果';
    showMessage((body && body.error) || '请求不合法。', 'error');
    return;
  }
  if (status === 503) {
    el('search-result-title').textContent = '检索结果';
    showMessage('向量库不可用：向量与混合模式返回 503，此时可改用「仅关键字」检索。', 'error');
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
    const qv = body.query_vector;
    const line = el2('p', 'hint');
    line.textContent =
      `问句「${q}」先被嵌入成 ${qv.dim} 维向量（L2 范数 ${fmt(qv.norm, 3)}），再拿去和切片向量算余弦：` +
      `[${qv.preview.map((v) => fmt(v, 4)).join(', ')}, …]`;
    vecBox.appendChild(line);
  } else {
    vecBox.appendChild(el2('p', 'hint', '本次没有计算向量：关键字路径把问句原文交给全文索引，不经过嵌入。'));
  }

  const hitsBox = el('search-hits');
  hitsBox.innerHTML = '';
  if (hits.length === 0) {
    hitsBox.appendChild(el2('p', 'empty', '资料中未找到相关内容。'));
    return;
  }
  hits.forEach((h, i) => hitsBox.appendChild(hitCard(h, i)));
}

// --- 问答 -------------------------------------------------------------------

async function runAsk() {
  const q = el('ask-q').value.trim();
  if (q.length < 2) {
    showMessage('问题至少需要 2 个字符。', 'error');
    return;
  }
  showMessage('');
  el('ask-result-card').hidden = false;
  el('ask-answer').textContent = '检索并生成中…';
  el('ask-status').textContent = '';
  el('ask-citations').innerHTML = '';

  const { status, body } = await postJSON('/api/ask', { q });
  if (status === 401) {
    renderLogin();
    showMessage('会话已失效，请重新登录。', 'error');
    return;
  }
  if (status === 400) {
    el('ask-answer').textContent = '';
    showMessage((body && body.error) || '请求不合法。', 'error');
    return;
  }
  if (status === 503) {
    el('ask-answer').textContent = '';
    showMessage('向量库不可用：问答依赖混合检索，当前无法作答。', 'error');
    return;
  }
  if (status !== 200 || !body) {
    el('ask-answer').textContent = '';
    showMessage('问答失败。', 'error');
    return;
  }

  el('ask-answer').textContent = body.answer || '';
  el('ask-status').textContent = body.status === 'no_evidence'
    ? '本班材料中没有依据，因此没有调用生成模型。'
    : `依据 ${(body.citations || []).length} 条切片生成；回答中的 [1]、[2] 与下方出处顺序一致。`;

  const box = el('ask-citations');
  box.innerHTML = '';
  (body.citations || []).forEach((c, i) => box.appendChild(hitCard(c, i)));
}

// --- 分段策略卡片 -----------------------------------------------------------

function selectedStrategy() {
  const checked = document.querySelector('input[name=chunk_strategy]:checked');
  return checked ? checked.value : 'auto';
}

function syncStrategyUI() {
  const current = selectedStrategy();
  for (const card of document.querySelectorAll('.strategy-card')) {
    card.classList.toggle('is-active', card.dataset.strategy === current);
  }
  for (const box of document.querySelectorAll('.strategy-fields')) {
    box.hidden = box.dataset.fields !== current;
  }
}

function initStrategyCards() {
  for (const input of document.querySelectorAll('input[name=chunk_strategy]')) {
    input.addEventListener('change', syncStrategyUI);
  }
  syncStrategyUI();
}

// 把表单里的分段参数按当前策略装进 FormData。
// auto 策略不提交任何参数——服务端会忽略它们。
function appendChunkParams(data) {
  const strategy = selectedStrategy();
  data.append('chunk_strategy', strategy);

  if (strategy === 'custom') {
    data.append('separator', el('custom-separator').value);
    data.append('max_chars', el('custom-max').value);
    data.append('overlap_percent', el('custom-overlap').value);
    if (el('custom-collapse').checked) data.append('collapse_whitespace', '1');
    if (el('custom-removeurls').checked) data.append('remove_urls', '1');
  } else if (strategy === 'hierarchy') {
    data.append('max_chars', el('hierarchy-max').value);
    data.append('overlap_percent', el('hierarchy-overlap').value);
  }
}

// --- 启动 -------------------------------------------------------------------

el('tabs').addEventListener('click', (e) => {
  const btn = e.target.closest('.tab');
  if (btn) switchTab(btn.dataset.tab);
});

el('login-form').addEventListener('submit', async (e) => {
  e.preventDefault();
  showMessage('');
  const payload = {
    username: el('username').value.trim(),
    password: el('password').value,
  };
  const { status, body } = await postJSON('/api/login', payload);
  if (status === 200 && body) {
    showMessage('');
    renderApp(body);
    await loadMaterials();
    return;
  }
  // 服务端对账号不存在 / 口令错误 / 限流返回同一响应，前端也使用同一提示。
  showMessage('登录失败：用户名或密码错误。', 'error');
});

el('logout').addEventListener('click', async () => {
  await api('/api/logout', { method: 'POST' });
  renderLogin();
  showMessage('已退出登录。', 'info');
});

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
  // 注意：不提交 class 字段——班级由服务端从会话读取。

  const submit = form.querySelector('button[type=submit]');
  submit.disabled = true;
  submit.textContent = '上传中…';
  showMessage('');

  let res;
  try {
    res = await fetch('/api/materials/upload', {
      method: 'POST',
      body: data,
      credentials: 'same-origin',
    });
  } catch (err) {
    submit.disabled = false;
    submit.textContent = '上传并入库';
    showMessage('网络错误，上传未完成。', 'error');
    return;
  }
  submit.disabled = false;
  submit.textContent = '上传并入库';

  if (res.status === 201) {
    const body = await res.json().catch(() => null);
    form.reset();
    syncStrategyUI();
    const names = { auto: '自动分段', custom: '自定义', hierarchy: '按层级分段' };
    const n = body ? body.chunk_count : 0;
    showMessage(`上传成功，按「${names[strategy]}」建立 ${n} 条切片并写入向量库。`, 'info');
    await loadMaterials();
    return;
  }

  let msg = '上传失败。';
  if (res.status === 403) {
    msg = '上传被拒绝（403）：仅教师可上传。';
  } else if (res.status === 400) {
    const body = await res.json().catch(() => null);
    msg = (body && body.error)
      ? `上传失败（400）：${body.error}`
      : '上传失败（400）：仅接受 .txt / .md，且不能为空。';
  } else if (res.status === 401) {
    msg = '会话已失效，请重新登录。';
  }
  showMessage(msg, 'error');
});

el('search-form').addEventListener('submit', (e) => {
  e.preventDefault();
  runSearch();
});

el('ask-form').addEventListener('submit', (e) => {
  e.preventDefault();
  runAsk();
});

document.addEventListener('click', (e) => {
  const link = e.target.closest('.example');
  if (!link) return;
  e.preventDefault();
  el('search-q').value = link.dataset.q;
  switchTab('search');
  runSearch();
});

async function bootstrap() {
  const { status, body } = await api('/api/me');
  if (status === 200 && body) {
    renderApp(body);
    await loadMaterials();
  } else {
    renderLogin();
  }
}

initStrategyCards();
bootstrap();
