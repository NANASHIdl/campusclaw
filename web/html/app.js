'use strict';

const el = (id) => document.getElementById(id);

const state = {
  user: null,
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
  el('scope-note').textContent = `列表仅展示你所属班级（班级 ${user.class_id}）的材料，数据来自服务端按会话班级过滤的查询。`;

  const isTeacher = user.role === 'teacher';
  // 是否渲染上传入口只影响可发现性；真正的授权在服务端角色校验。
  el('upload-block').hidden = !isTeacher;
  el('readonly-note').hidden = isTeacher;
}

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
    const a = document.createElement('a');
    a.href = `/api/materials/${m.id}/file`;
    a.textContent = '下载';
    actions.appendChild(a);
    tr.appendChild(actions);
    tbody.appendChild(tr);
  }
}

function cell(text) {
  const td = document.createElement('td');
  td.textContent = text;
  return td;
}

async function bootstrap() {
  const { status, body } = await api('/api/me');
  if (status === 200 && body) {
    renderApp(body);
    await loadMaterials();
  } else {
    renderLogin();
  }
}

el('login-form').addEventListener('submit', async (e) => {
  e.preventDefault();
  showMessage('');
  const payload = {
    username: el('username').value.trim(),
    password: el('password').value,
  };
  const { status, body } = await api('/api/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  });
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
  const data = new FormData();
  const file = form.querySelector('input[type=file]').files[0];
  if (!file) return;
  data.append('file', file);
  // 注意：不提交 class 字段——班级由服务端从会话读取。
  const res = await fetch('/api/materials/upload', {
    method: 'POST',
    body: data,
    credentials: 'same-origin',
  });
  if (res.status === 201) {
    form.reset();
    showMessage('上传成功。', 'info');
    await loadMaterials();
    return;
  }
  let msg = '上传失败。';
  if (res.status === 403) msg = '上传被拒绝（403）：仅教师可上传。';
  else if (res.status === 400) msg = '上传失败（400）：仅接受 .txt / .md，且不能为空。';
  else if (res.status === 401) msg = '会话已失效，请重新登录。';
  showMessage(msg, 'error');
});

bootstrap();
