// 第 5 课前端点验（tasks.md 11.1–11.6）+ Swagger 页面试调（10.4）+ 无外网依赖（10.6）。
//
// 用本机 Chrome 的真实交互逐条验证，不依赖任何 npm 包：
//   1. 先起浏览器：chrome --headless=new --remote-debugging-port=9222 about:blank
//   2. 再跑本脚本：node scripts/verify_frontend.mjs
// 截图落在 scripts/.frontend-report/（已忽略入库）。
// 注意：每次运行约登录 5 次，登录限流为 10 次/分钟，连续跑两次会触发限流。
import fs from 'node:fs/promises';
import path from 'node:path';
import { setTimeout as sleep } from 'node:timers/promises';
import { CDP, waitFor } from './cdp.mjs';

const BASE = process.env.BASE_URL || 'http://127.0.0.1:8081';
const SHOTS = path.join(import.meta.dirname, '.frontend-report');
const TEACHER = ['teacher_a', 'teach123'];
const STUDENT = ['student_a1', 'stu123'];
const STAMP = String(Date.now()).slice(-6);

let pass = 0;
let fail = 0;
const failed = [];

function check(name, ok, detail = '') {
  if (ok) { pass += 1; console.log(`  PASS  ${name}${detail ? `  (${detail})` : ''}`); }
  else { fail += 1; failed.push(name); console.log(`  FAIL  ${name}${detail ? `  ${detail}` : ''}`); }
}

const sel = (s) => JSON.stringify(s);

async function fill(cdp, selector, value) {
  return cdp.eval(`(() => {
    const e = document.querySelector(${sel(selector)});
    if (!e) return false;
    e.focus(); e.value = ${sel(value)};
    e.dispatchEvent(new Event('input', { bubbles: true }));
    e.dispatchEvent(new Event('change', { bubbles: true }));
    return true;
  })()`);
}

async function clickText(cdp, selector, text) {
  return cdp.eval(`(() => {
    const n = [...document.querySelectorAll(${sel(selector)})]
      .find((x) => (x.textContent || '').trim().includes(${sel(text)}));
    if (!n) return false;
    n.click(); return true;
  })()`);
}

async function clickRowButton(cdp, table, rowText, buttonText) {
  return cdp.eval(`(() => {
    const rows = [...document.querySelectorAll(${sel(table + ' tbody tr')})];
    const row = rows.find((r) => (r.textContent || '').includes(${sel(rowText)}));
    if (!row) return false;
    const btn = [...row.querySelectorAll('button')]
      .find((b) => (b.textContent || '').trim() === ${sel(buttonText)});
    if (!btn) return false;
    btn.click(); return true;
  })()`);
}

async function login(cdp, [user, pwd]) {
  await fill(cdp, '#username', user);
  await fill(cdp, '#password', pwd);
  await cdp.eval(`document.querySelector('#login-form button[type=submit]').click()`);
  await waitFor(cdp, `!document.querySelector('#view-app').hidden`, 10000, '登录后进入应用');
}

async function logout(cdp) {
  await cdp.eval(`document.querySelector('#logout').click()`);
  await waitFor(cdp, `!document.querySelector('#view-login').hidden`, 8000, '回到登录页');
}

// 提示条会保留上一次文案，提交前先清空，否则会误判「已完成」。
async function clearMessage(cdp) {
  await cdp.eval(`(() => {
    const m = document.querySelector('#message');
    m.hidden = true; m.textContent = '';
  })()`);
}

// 取表格某行的某一列文本
async function cellOf(cdp, table, rowText, index) {
  return cdp.eval(`(() => {
    const rows = [...document.querySelectorAll(${sel(table + ' tbody tr')})];
    const row = rows.find((r) => (r.textContent || '').includes(${sel(rowText)}));
    return row ? row.children[${index}].textContent.trim() : '';
  })()`);
}

async function main() {
  await fs.mkdir(SHOTS, { recursive: true });
  const cdp = await CDP.attach(9222);
  try {
    // ================= 11.1 登录态与 sessionStorage =================
    console.log('== 11.1 登录令牌存 sessionStorage、刷新仍在线 ==');
    await cdp.goto(BASE + '/');
    // 上一轮可能留下登录态，先清干净再从登录页开始。
    await cdp.eval(`sessionStorage.clear()`);
    await cdp.reload();
    await waitFor(cdp, `!document.querySelector('#view-login').hidden`, 8000, '登录页渲染');
    check('未登录时显示登录页', await cdp.eval(`!document.querySelector('#view-login').hidden`));
    await cdp.shot(`${SHOTS}/01-login.png`);

    await login(cdp, TEACHER);
    const token = await cdp.eval(`sessionStorage.getItem('cc_token') || ''`);
    check('登录后 sessionStorage 存有 cc_token', token.split('.').length === 3,
      `长度 ${token.length}`);
    check('页头显示用户名与角色',
      (await cdp.eval(`document.querySelector('#who-text').textContent`)).includes('teacher_a'));

    await cdp.reload();
    await waitFor(cdp, `!document.querySelector('#view-app').hidden`, 8000, '刷新后仍在线');
    check('刷新页面后仍在线（令牌来自 sessionStorage）',
      await cdp.eval(`!document.querySelector('#view-app').hidden`));

    await cdp.eval(`sessionStorage.removeItem('cc_token')`);
    await cdp.reload();
    await waitFor(cdp, `!document.querySelector('#view-login').hidden`, 8000, '清空后回登录页');
    check('清空 sessionStorage 后刷新回到登录页',
      await cdp.eval(`!document.querySelector('#view-login').hidden`));

    // ================= 11.6 主题色 =================
    console.log('== 11.6 北大红主题 ==');
    await login(cdp, TEACHER);
    const btnColor = await cdp.eval(
      `getComputedStyle(document.querySelector('#conv-new')).backgroundColor`);
    const redVar = await cdp.eval(
      `getComputedStyle(document.documentElement).getPropertyValue('--pku-red').trim()`);
    check('主按钮为北大红 #94070A', btnColor.replace(/\s/g, '') === 'rgb(148,7,10)', `实际 ${btnColor}`);
    check('主题变量 --pku-red 为 #94070A', redVar.toLowerCase() === '#94070a', `实际 ${redVar}`);

    // ================= 11.3 智能体页（教师可写） =================
    console.log('== 11.3 智能体页：列表、新建、编辑、启停 ==');
    await clickText(cdp, '.tab', '智能体');
    await waitFor(cdp, `document.querySelector('#agents-table tbody').children.length > 0`, 8000,
      '智能体列表');
    check('教师可见智能体列表', await cdp.eval(
      `document.querySelector('#agents-table tbody').children.length > 0`));
    check('教师可见新建/编辑表单', await cdp.eval(`!document.querySelector('#agent-form-card').hidden`));

    const agentName = `前端验收·${STAMP}`;
    await fill(cdp, '#agent-name', agentName);
    await fill(cdp, '#agent-prompt', '你是前端验收用的教研助手，回答简短。');
    await cdp.eval(`document.querySelector('#agent-form').requestSubmit()`);
    await waitFor(cdp, `document.querySelector('#message').textContent.includes('智能体已保存')`,
      10000, '新建智能体成功');
    check('新建智能体成功', true, agentName);
    check('新智能体出现在列表中', await cdp.eval(
      `[...document.querySelectorAll('#agents-table tbody tr')]
        .some((r) => r.textContent.includes(${sel(agentName)}))`));

    check('点击行内「编辑」打开该智能体',
      await clickRowButton(cdp, '#agents-table', agentName, '编辑'));
    await waitFor(cdp, `document.querySelector('#agent-form-title').textContent.startsWith('编辑智能体')`,
      5000, '进入编辑态');
    check('表单切到编辑态', await cdp.eval(
      `document.querySelector('#agent-form-title').textContent.startsWith('编辑智能体')`));

    // 11.2 教师改提示词
    console.log('== 11.2 提示词：教师可编辑 ==');
    await fill(cdp, '#agent-prompt', '改过的提示词：先看学生写到哪一步。');
    await clearMessage(cdp);
    await cdp.eval(`document.querySelector('#agent-form').requestSubmit()`);
    await waitFor(cdp, `document.querySelector('#message').textContent.includes('智能体已保存')`,
      10000, '保存提示词');
    await waitFor(cdp, `(() => {
      const rows = [...document.querySelectorAll('#agents-table tbody tr')];
      const row = rows.find((r) => r.textContent.includes(${sel(agentName)}));
      return row && row.children[2].textContent.includes('改过的提示词');
    })()`, 8000, '提示词列刷新');
    const promptCell = await cellOf(cdp, '#agents-table', agentName, 2);
    check('提示词列显示新内容', promptCell.includes('改过的提示词'), `实际 ${promptCell}`);

    // 启停
    await cdp.eval(`document.querySelector('#agent-enabled').checked = false`);
    await clearMessage(cdp);
    await cdp.eval(`document.querySelector('#agent-form').requestSubmit()`);
    await waitFor(cdp, `document.querySelector('#message').textContent.includes('智能体已保存')`,
      10000, '停用智能体');
    await waitFor(cdp, `(() => {
      const rows = [...document.querySelectorAll('#agents-table tbody tr')];
      const row = rows.find((r) => r.textContent.includes(${sel(agentName)}));
      return row && row.children[5].textContent.trim() === '已停用';
    })()`, 8000, '状态列刷新');
    const statusCell = await cellOf(cdp, '#agents-table', agentName, 5);
    check('取消勾选「启用」后状态列显示已停用', statusCell === '已停用', `实际 ${statusCell}`);
    await cdp.shot(`${SHOTS}/02-agents-teacher.png`);

    // ================= 11.2 学生只读 =================
    console.log('== 11.2 / 11.3 学生侧只读 ==');
    await logout(cdp);
    await login(cdp, STUDENT);
    await clickText(cdp, '.tab', '智能体');
    await sleep(800);
    const studentEdit = await cdp.eval(
      `[...document.querySelectorAll('#agents-table button')].filter((b) => b.textContent.trim() === '编辑').length`);
    check('学生看不到「编辑」按钮', studentEdit === 0, `实际 ${studentEdit} 个`);
    check('学生看不到智能体表单', await cdp.eval(`document.querySelector('#agent-form-card').hidden`));
    const studentListed = await cdp.eval(
      `[...document.querySelectorAll('#agents-table tbody tr')].map((r) => r.textContent).join('|')`);
    check('学生列表不含刚停用的智能体', !studentListed.includes(agentName));
    const forbidden = await cdp.eval(`(async () => {
      const r = await fetch('/api/agents', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json',
                   Authorization: 'Bearer ' + sessionStorage.getItem('cc_token') },
        body: JSON.stringify({ name: '学生绕过界面' + ${sel(STAMP)}, prompt: '', enabled: true,
                               skill_ids: [], material_ids: [] }),
      });
      return r.status;
    })()`);
    check('学生直接调接口仍被拒（403）', forbidden === 403, `实际 ${forbidden}`);
    await cdp.shot(`${SHOTS}/03-agents-student.png`);

    // 学生侧技能页也只读
    await clickText(cdp, '.tab', '技能');
    await sleep(800);
    const studentSkillBtns = await cdp.eval(
      `[...document.querySelectorAll('#skills-table button')].length`);
    check('学生看不到技能的编辑/删除按钮', studentSkillBtns === 0, `实际 ${studentSkillBtns} 个`);
    check('学生看不到技能表单', await cdp.eval(`document.querySelector('#skill-form-card').hidden`));

    // ================= 11.4 技能页 =================
    console.log('== 11.4 技能页：粘贴、校验错误、编辑、启停、删除 ==');
    await logout(cdp);
    await login(cdp, TEACHER);
    await clickText(cdp, '.tab', '技能');
    await waitFor(cdp, `document.querySelector('#skills-table tbody').children.length > 0`, 8000,
      '技能列表');

    const skillName = `前端验收技能${STAMP}`;
    await fill(cdp, '#skill-source', '名称：缺空行\n适用条件：x\n启用：是');
    await clearMessage(cdp);
    await cdp.eval(`document.querySelector('#skill-form').requestSubmit()`);
    await waitFor(cdp, `(() => {
      const m = document.querySelector('#message');
      return !m.hidden && m.textContent.includes('空行');
    })()`, 8000, '非法文件提示');
    const errText = await cdp.eval(`document.querySelector('#message').textContent`);
    check('非法技能文件显示可读错误', errText.includes('空行'), `实际「${errText}」`);

    await fill(cdp, '#skill-source',
      `名称：${skillName}\n适用条件：前端验收,去括号\n资料：数学\n工具：knowledge_search\n启用：是\n\n用于前端验收的技能说明。`);
    await clearMessage(cdp);
    await cdp.eval(`document.querySelector('#skill-form').requestSubmit()`);
    await waitFor(cdp, `document.querySelector('#message').textContent.includes('技能已保存')`,
      8000, '保存技能');
    check('合法技能文件保存成功', await cdp.eval(
      `[...document.querySelectorAll('#skills-table tbody tr')]
        .some((r) => r.textContent.includes(${sel(skillName)}))`));

    check('点击行内「编辑」打开技能',
      await clickRowButton(cdp, '#skills-table', skillName, '编辑'));
    await waitFor(cdp, `document.querySelector('#skill-form-title').textContent.startsWith('编辑技能')`,
      5000, '技能编辑态');
    await fill(cdp, '#skill-source',
      `名称：${skillName}\n适用条件：前端验收,去括号\n资料：数学\n工具：knowledge_search\n启用：否\n\n改成停用状态的说明。`);
    await clearMessage(cdp);
    await cdp.eval(`document.querySelector('#skill-form').requestSubmit()`);
    await waitFor(cdp, `document.querySelector('#message').textContent.includes('技能已保存')`,
      8000, '保存停用');
    await waitFor(cdp, `(() => {
      const rows = [...document.querySelectorAll('#skills-table tbody tr')];
      const row = rows.find((r) => r.textContent.includes(${sel(skillName)}));
      return row && row.children[4].textContent.trim() === '已停用';
    })()`, 8000, '技能状态列刷新');
    const skillStatus = await cellOf(cdp, '#skills-table', skillName, 4);
    check('把「启用」改成否后状态列显示已停用', skillStatus === '已停用', `实际 ${skillStatus}`);
    await cdp.shot(`${SHOTS}/04-skills.png`);

    await clearMessage(cdp);
    check('点击「删除」触发确认并删除成功',
      await clickRowButton(cdp, '#skills-table', skillName, '删除'));
    await waitFor(cdp, `document.querySelector('#message').textContent.includes('技能已删除')`,
      8000, '删除技能');
    check('删除后列表不再有该技能', !(await cdp.eval(
      `[...document.querySelectorAll('#skills-table tbody tr')]
        .some((r) => r.textContent.includes(${sel(skillName)}))`)));

    // ================= 11.5 对话页 =================
    console.log('== 11.5 对话页：新建会话 → 追问 → 逐段渲染 → 引用 ==');
    await clickText(cdp, '.tab', '解题对话');
    await sleep(500);
    const picked = await cdp.eval(`(() => {
      const s = document.querySelector('#conv-agent');
      const opt = [...s.options].find((o) => o.textContent.includes('解题引导助手'));
      if (!opt) return '';
      s.value = opt.value; return opt.textContent;
    })()`);
    check('对话页可选择智能体', picked !== '', `选中「${picked}」`);
    await cdp.eval(`document.querySelector('#conv-new').click()`);
    await waitFor(cdp, `!document.querySelector('#conv-card').hidden`, 10000, '新建会话');
    check('新建会话成功并打开会话卡片', await cdp.eval(`!document.querySelector('#conv-card').hidden`));
    check('会话标题为占位串或已改写', await cdp.eval(
      `document.querySelector('#conv-title').textContent.length > 0`));

    await fill(cdp, '#conv-input', '请用三句话说明去括号时要注意什么，每句一行');
    await cdp.eval(`document.querySelector('#conv-form').requestSubmit()`);

    const samples = [];
    let stable = 0;
    for (let i = 0; i < 500; i += 1) {
      const len = await cdp.eval(`(() => {
        const box = document.querySelector('#conv-messages');
        const bots = box.querySelectorAll('.msg-bot .msg-body');
        return bots.length ? bots[bots.length - 1].textContent.length : 0;
      })()`);
      if (samples[samples.length - 1] !== len) { samples.push(len); stable = 0; } else { stable += 1; }
      if (samples.filter((n) => n > 0).length >= 4) break;
      if (stable > 25 && (samples[samples.length - 1] || 0) > 0) break;
      await sleep(60);
    }
    const grew = samples.filter((n) => n > 0);
    check('正文逐段出现（多帧递增，不是一次性整段）', grew.length >= 3,
      `长度序列 ${JSON.stringify(samples.slice(0, 10))}`);
    await waitFor(cdp, `document.querySelector('#conv-meta').textContent.length > 0`, 15000,
      'meta 到达');
    const metaText = await cdp.eval(`document.querySelector('#conv-meta').textContent`);
    check('首个事件即显示依据状态与命中技能', metaText.includes('依据') || metaText.includes('技能'),
      `实际「${metaText}」`);

    const cites = await cdp.eval(`(() => {
      const box = document.querySelector('#conv-messages');
      return [...box.querySelectorAll('.msg-cites .badge')].map((b) => b.textContent);
    })()`);
    check('引用徽标随首个事件出现', cites.length > 0, `实际 ${cites.length} 条 ${cites[0] || ''}`);

    const before = await cdp.eval(`document.querySelector('#conv-messages').children.length`);
    await fill(cdp, '#conv-input', '那如果括号前面是减号呢');
    await cdp.eval(`document.querySelector('#conv-form').requestSubmit()`);
    await waitFor(cdp, `document.querySelector('#conv-messages').children.length >= ${before + 2}`,
      20000, '第二轮回答');
    check('连续追问第二轮正常', await cdp.eval(
      `document.querySelector('#conv-messages').children.length >= ${before + 2}`));
    await cdp.shot(`${SHOTS}/05-chat.png`);

    // ================= 10.4 Swagger 页面内试调 =================
    console.log('== 10.4 Swagger UI 页面内试调 ==');
    const swaggerToken = await cdp.eval(`(async () => {
      const r = await fetch('/api/login', { method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ username: 'teacher_a', password: 'teach123' }) });
      return (await r.json()).token;
    })()`);
    cdp.requests.length = 0;
    await cdp.goto(BASE + '/swagger/index.html');
    await waitFor(cdp, `document.querySelectorAll('.opblock').length > 0`, 20000, 'Swagger 渲染');
    const opCount = await cdp.eval(`document.querySelectorAll('.opblock').length`);
    check('页面渲染出接口列表', opCount >= 20, `${opCount} 个操作`);
    check('页面文档来自 /openapi.json（与机器读的是同一份 3.0 描述）',
      cdp.requests.some((u) => u.endsWith('/openapi.json')));

    await cdp.eval(`window.ui.preauthorizeApiKey('bearerAuth', ${sel(swaggerToken)})`);
    await sleep(600);

    // Swagger UI 的 .opblock 没有 data-path，按摘要路径文本定位。
    const OP = `[...document.querySelectorAll('.opblock')].find((o) => {
      const p = o.querySelector('.opblock-summary-path');
      return p && p.textContent.trim() === '/api/me';
    })`;

    const expanded = await cdp.eval(`(() => {
      const op = ${OP};
      if (!op) return 'not-found';
      const btn = op.querySelector('.opblock-summary-control') || op.querySelector('.opblock-summary');
      btn.click();
      return 'ok';
    })()`);
    check('找到 GET /api/me 操作', expanded === 'ok', expanded);
    await waitFor(cdp, `(() => { const op = ${OP}; return !!op && !!op.querySelector('.opblock-body'); })()`,
      8000, '操作块展开');

    const tryOutReady = await (async () => {
      try {
        await waitFor(cdp, `(() => {
          const op = ${OP};
          return !!op && [...op.querySelectorAll('button')]
            .some((b) => b.textContent.toLowerCase().includes('try it out'));
        })()`, 8000, 'Try it out 按钮');
        return true;
      } catch { return false; }
    })();
    check('页面提供「Try it out」入口', tryOutReady);
    await cdp.eval(`(() => {
      const op = ${OP};
      const b = [...op.querySelectorAll('button')]
        .find((x) => x.textContent.toLowerCase().includes('try it out'));
      if (b) b.click();
    })()`);
    await sleep(700);

    const executeReady = await (async () => {
      try {
        await waitFor(cdp, `(() => {
          const op = ${OP};
          return !!op && [...op.querySelectorAll('button')]
            .some((b) => b.textContent.trim().toLowerCase() === 'execute');
        })()`, 8000, 'Execute 按钮');
        return true;
      } catch { return false; }
    })();
    check('页面提供「Execute」按钮', executeReady);
    await cdp.eval(`(() => {
      const op = ${OP};
      const b = [...op.querySelectorAll('button')]
        .find((x) => x.textContent.trim().toLowerCase() === 'execute');
      if (b) b.click();
    })()`);

    await waitFor(cdp, `(() => {
      const op = ${OP};
      return !!op && !!op.querySelector('.live-responses-table, .responses-table.live-responses-table');
    })()`, 20000, '执行结果');
    const respStatus = await cdp.eval(`(() => {
      const op = ${OP};
      const cells = [...op.querySelectorAll('.response-col_status')]
        .map((c) => c.textContent.trim());
      return cells.find((t) => /^\\d{3}$/.test(t)) || cells.join('|');
    })()`);
    check('页面内执行需登录接口返回 200', respStatus === '200', `实际 ${respStatus}`);
    const respBody = await cdp.eval(`(() => {
      const op = ${OP};
      const b = op.querySelector('.response-col_description pre, .live-responses-table pre');
      return b ? b.textContent.slice(0, 160) : '';
    })()`);
    check('响应正文为当前用户信息', respBody.includes('teacher_a'), `实际 ${respBody.slice(0, 80)}`);
    await cdp.shot(`${SHOTS}/06-swagger-exec.png`);

    // ================= 10.6 页面加载无外部请求 =================
    console.log('== 10.6 运行期不依赖外网 ==');
    cdp.requests.length = 0;
    await cdp.goto(BASE + '/swagger/index.html');
    await waitFor(cdp, `document.querySelectorAll('.opblock').length > 0`, 20000, '再次渲染');
    const external = cdp.requests.filter((u) => !u.startsWith(BASE) && !u.startsWith('data:'));
    check('页面加载期间无任何外部请求', external.length === 0,
      external.length ? `外部请求 ${external.slice(0, 3)}` : `${cdp.requests.length} 个请求全部指向本机`);

    console.log(`\n===== 汇总: PASS=${pass}  FAIL=${fail} =====`);
    if (failed.length) {
      console.log('失败项：');
      failed.forEach((n) => console.log('  - ' + n));
    }
    return fail ? 1 : 0;
  } finally {
    cdp.close();
  }
}

process.exitCode = await main();
