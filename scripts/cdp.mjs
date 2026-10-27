// 极简 CDP 客户端：用本机 Chrome 的 --remote-debugging-port 驱动真实浏览器，
// 不依赖任何 npm 包（Node 22 自带 WebSocket / fetch）。
import fs from 'node:fs/promises';
import { setTimeout as sleep } from 'node:timers/promises';

export class CDP {
  constructor(ws) {
    this.ws = ws;
    this.seq = 0;
    this.pending = new Map();
    this.handlers = new Map();
    this.requests = [];
    this.consoleErrors = [];
  }

  static async attach(port = 9222, timeoutMs = 30000) {
    const deadline = Date.now() + timeoutMs;
    let target = null;
    while (Date.now() < deadline) {
      try {
        const list = await (await fetch(`http://127.0.0.1:${port}/json/list`)).json();
        target = list.find((t) => t.type === 'page');
        if (target) break;
      } catch {
        /* 浏览器还没起来 */
      }
      await sleep(300);
    }
    if (!target) throw new Error('连不上 Chrome 调试端口');

    const ws = new WebSocket(target.webSocketDebuggerUrl);
    await new Promise((res, rej) => {
      ws.addEventListener('open', res, { once: true });
      ws.addEventListener('error', () => rej(new Error('WebSocket 连接失败')), { once: true });
    });

    const cdp = new CDP(ws);
    ws.addEventListener('message', (ev) => cdp._onMessage(JSON.parse(ev.data)));
    await cdp.send('Page.enable');
    await cdp.send('Runtime.enable');
    await cdp.send('Network.enable');
    // 原生 confirm() 一律接受，避免弹窗卡住脚本。
    cdp.on('Page.javascriptDialogOpening', () => {
      cdp.send('Page.handleJavaScriptDialog', { accept: true }).catch(() => {});
    });
    cdp.on('Network.requestWillBeSent', (p) => cdp.requests.push(p.request.url));
    cdp.on('Runtime.consoleAPICalled', (p) => {
      if (p.type === 'error') {
        cdp.consoleErrors.push((p.args || []).map((a) => a.value ?? a.description).join(' '));
      }
    });
    return cdp;
  }

  _onMessage(msg) {
    if (msg.id && this.pending.has(msg.id)) {
      const { res, rej } = this.pending.get(msg.id);
      this.pending.delete(msg.id);
      msg.error ? rej(new Error(JSON.stringify(msg.error))) : res(msg.result);
      return;
    }
    if (msg.method) (this.handlers.get(msg.method) || []).forEach((fn) => fn(msg.params));
  }

  send(method, params = {}) {
    const id = ++this.seq;
    return new Promise((res, rej) => {
      this.pending.set(id, { res, rej });
      this.ws.send(JSON.stringify({ id, method, params }));
    });
  }

  on(method, fn) {
    if (!this.handlers.has(method)) this.handlers.set(method, []);
    this.handlers.get(method).push(fn);
  }

  async eval(expression, { awaitPromise = true } = {}) {
    const r = await this.send('Runtime.evaluate', {
      expression, awaitPromise, returnByValue: true, userGesture: true,
    });
    if (r.exceptionDetails) {
      throw new Error('JS 异常：' + (r.exceptionDetails.exception?.description || r.exceptionDetails.text));
    }
    return r.result.value;
  }

  async goto(url, timeout = 20000) {
    await this.send('Page.navigate', { url });
    const deadline = Date.now() + timeout;
    while (Date.now() < deadline) {
      if ((await this.eval('document.readyState')) === 'complete') return;
      await sleep(100);
    }
    throw new Error('页面加载超时：' + url);
  }

  async reload() {
    await this.send('Page.reload', {});
    const deadline = Date.now() + 20000;
    while (Date.now() < deadline) {
      await sleep(150);
      try {
        if ((await this.eval('document.readyState')) === 'complete') return;
      } catch {
        /* 导航中 */
      }
    }
    throw new Error('刷新超时');
  }

  async shot(path) {
    const r = await this.send('Page.captureScreenshot', { format: 'png' });
    await fs.writeFile(path, Buffer.from(r.data, 'base64'));
  }

  close() {
    try { this.ws.close(); } catch { /* ignore */ }
  }
}

export async function waitFor(cdp, expr, timeout = 8000, label = expr) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    if (await cdp.eval(expr)) return true;
    await sleep(120);
  }
  throw new Error('等待超时：' + label);
}
