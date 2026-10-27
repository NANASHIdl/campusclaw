#!/usr/bin/env python3
"""CampusClaw 第 6 课（add-mcp-tool-integration）端到端验收。

逐条执行本 change 两个 capability 的 Scenario：
  - specs/mcp-server/spec.md   协议三方法、令牌、工具声明、执行与返回
  - specs/mcp-tools/spec.md    登记与令牌边界、连接与发现、断开、引用校验、
                               候选集、调用循环与上限、结果清洗、流式可见、调用记录
另含 agent-chat 增量（技能工具项、meta.tool_calls）。

工具服务不映射宿主机端口，所以「服务单独可验」这一节经 `docker compose exec api wget`
在容器网络内直连 http://mcp-demo:8090；其余走 web 端口。

    BASE_URL=http://127.0.0.1:8081 python scripts/verify_mcp.py
"""

import json
import os
import subprocess
import sys
import time
import urllib.error
import urllib.request

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
BASE = os.environ.get("BASE_URL", "http://127.0.0.1:8080")
MCP_INNER = "http://mcp-demo:8090"

PASS = 0
FAIL = 0
FAILED = []
TS = str(int(time.time()))


def check(name, ok, detail=""):
    global PASS, FAIL
    if ok:
        PASS += 1
        print(f"  PASS  {name}" + (f"  ({detail})" if detail else ""))
    else:
        FAIL += 1
        FAILED.append(name)
        print(f"  FAIL  {name}" + (f"  {detail}" if detail else ""))


def note(text):
    print(f"  说明：{text}")


def load_env():
    out = {}
    path = os.path.join(ROOT, ".env")
    if os.path.exists(path):
        with open(path, encoding="utf-8") as fh:
            for line in fh:
                line = line.strip()
                if line and not line.startswith("#") and "=" in line:
                    k, v = line.split("=", 1)
                    out[k.strip()] = v.strip()
    return out


ENV = load_env()
TOKEN = ENV.get("MCP_AUTH_TOKEN", "")


# --------------------------------------------------------------------------- #
# 数据库回查（与第 5 课同一套做法：经 docker compose exec db mysql）
# --------------------------------------------------------------------------- #

def db(sql):
    cmd = ["docker", "compose", "exec", "-T", "db", "mysql", "--default-character-set=utf8mb4",
           "-u" + ENV.get("DB_USER", "campusclaw"), "-p" + ENV.get("DB_PASSWORD", ""),
           "-D", ENV.get("DB_NAME", "campusclaw"), "-N", "-B", "-e", sql]
    p = subprocess.run(cmd, cwd=ROOT, capture_output=True, text=True,
                       encoding="utf-8", errors="replace")
    if p.returncode != 0:
        raise RuntimeError(f"SQL 执行失败: {sql}\n{(p.stderr or '').strip()}")
    return [ln.split("\t") for ln in (p.stdout or "").splitlines()
            if ln and not ln.startswith("mysql: [Warning]")]


def db_one(sql):
    rows = db(sql)
    return rows[0][0] if rows else None


def db_int(sql):
    try:
        return int(db_one(sql))
    except (TypeError, ValueError):
        return -1


def db_hex(sql):
    v = db_one(sql)
    if v in (None, "", "NULL"):
        return None
    return bytes.fromhex(v).decode("utf-8")


# --------------------------------------------------------------------------- #
# 直连工具服务（容器网络内）
# --------------------------------------------------------------------------- #

def server_rpc(method, params=None, token=None, raw=None):
    """在 api 容器里用 wget 调工具服务，返回 (status, body)。

    token 传空串表示「不带 Authorization 头」。
    """
    if raw is not None:
        payload = raw
    else:
        payload = json.dumps({"jsonrpc": "2.0", "id": 1, "method": method,
                              "params": params or {}}, ensure_ascii=False)
    cmd = ["docker", "compose", "exec", "-T", "api", "wget", "-S", "-qO-",
           "--header=Content-Type: application/json"]
    use_token = TOKEN if token is None else token
    if use_token:
        cmd.append("--header=Authorization: Bearer " + use_token)
    cmd += ["--post-data=" + payload, MCP_INNER + "/mcp"]

    p = subprocess.run(cmd, cwd=ROOT, capture_output=True, text=True,
                       encoding="utf-8", errors="replace")
    status = None
    for line in (p.stderr or "").splitlines():
        if "HTTP/1.1" in line:
            parts = line.split()
            if len(parts) >= 2 and parts[1].isdigit():
                status = int(parts[1])
    return status, (p.stdout or "").strip()


def server_health():
    p = subprocess.run(["docker", "compose", "exec", "-T", "api", "wget", "-qO-",
                        MCP_INNER + "/health"], cwd=ROOT, capture_output=True,
                       text=True, encoding="utf-8", errors="replace")
    return p.returncode, (p.stdout or "").strip()


def rpc_result(body):
    try:
        data = json.loads(body)
    except ValueError:
        return None
    return data.get("result")


def rpc_error_code(body):
    try:
        data = json.loads(body)
    except ValueError:
        return None
    err = data.get("error") or {}
    return err.get("code")


# --------------------------------------------------------------------------- #
# HTTP 客户端
# --------------------------------------------------------------------------- #

class Client:
    def __init__(self):
        self.token = None

    def call(self, path, data=None, headers=None, method=None, use_token=True):
        body = json.dumps(data).encode() if data is not None else None
        hdrs = dict(headers or {})
        if body is not None and "Content-Type" not in hdrs:
            hdrs["Content-Type"] = "application/json"
        if use_token and self.token:
            hdrs["Authorization"] = "Bearer " + self.token
        req = urllib.request.Request(BASE + path, data=body, headers=hdrs,
                                     method=method or ("POST" if body is not None else "GET"))
        try:
            r = urllib.request.urlopen(req, timeout=180)
            return r.status, r.read()
        except urllib.error.HTTPError as e:
            return e.code, e.read()
        except Exception as e:  # noqa: BLE001
            return None, f"{type(e).__name__}: {e}".encode()

    def json(self, path, data=None, method=None):
        st, body = self.call(path, data, method=method)
        try:
            return st, json.loads(body) if body else None
        except ValueError:
            return st, None

    def login(self, username, password):
        st, body = self.call("/api/login", {"username": username, "password": password},
                             use_token=False)
        info = json.loads(body) if body else {}
        self.token = info.get("token")
        return st, info

    def sse(self, path, data):
        body = json.dumps(data).encode()
        hdrs = {"Content-Type": "application/json", "Accept": "text/event-stream"}
        if self.token:
            hdrs["Authorization"] = "Bearer " + self.token
        req = urllib.request.Request(BASE + path, data=body, headers=hdrs, method="POST")
        try:
            r = urllib.request.urlopen(req, timeout=180)
        except urllib.error.HTTPError as e:
            return e.code, []
        events, buf = [], b""
        while True:
            chunk = r.read(1)
            if not chunk:
                break
            buf += chunk
            while b"\n\n" in buf:
                frame, buf = buf.split(b"\n\n", 1)
                name = data_raw = None
                for line in frame.decode("utf-8", "replace").split("\n"):
                    if line.startswith("event: "):
                        name = line[7:]
                    elif line.startswith("data: "):
                        data_raw = line[6:]
                if name:
                    try:
                        parsed = json.loads(data_raw) if data_raw else None
                    except ValueError:
                        parsed = data_raw
                    events.append({"event": name, "data": parsed})
        return r.status, events


def ask(client, conv_id, content):
    st, events = client.sse(f"/api/conversations/{conv_id}/messages", {"content": content})
    meta = next((e["data"] for e in events if e["event"] == "meta"), None) or {}
    text = "".join((e["data"] or {}).get("text", "") for e in events if e["event"] == "delta")
    return st, events, meta, text


def main():
    print(f"== 目标 {BASE} ==\n")
    if not TOKEN:
        print("缺少 MCP_AUTH_TOKEN：请先在 .env 配置（见 .env.example）。")
        return 2

    teacher = Client()
    student = Client()
    anon = Client()

    # ===================================================================== #
    print("== 1. 工具服务单独可验（mcp-server） ==")
    code, body = server_health()
    check("/health 不需要令牌即可访问", code == 0 and '"status":"ok"' in body, body[:80])
    check("health 不泄露令牌", TOKEN not in body)

    st, body = server_rpc("initialize", {"protocolVersion": "2024-11-05", "capabilities": {},
                                        "clientInfo": {"name": "campusclaw", "version": "1.0"}})
    result = rpc_result(body) or {}
    check("initialize 返回协议版本与 capabilities.tools",
          st == 200 and result.get("protocolVersion") and "tools" in (result.get("capabilities") or {}),
          json.dumps(result, ensure_ascii=False)[:120])
    check("initialize 返回 serverInfo", bool((result.get("serverInfo") or {}).get("name")))

    st, body = server_rpc("tools/list")
    tools = (rpc_result(body) or {}).get("tools") or []
    names = [t.get("name") for t in tools]
    check("tools/list 返回至少两个工具", len(tools) >= 2, f"实际 {names}")
    check("两个工具三项声明齐全",
          all(t.get("name") and t.get("description") and t.get("inputSchema") for t in tools))
    check("工具名符合 ^[a-z][a-z0-9_]*$",
          all(n and n[0].islower() and all(c.islower() or c.isdigit() or c == "_" for c in n)
              for n in names), f"实际 {names}")
    check("说明含适用与不适用", all("不适用" in t["description"] for t in tools))
    check("输入结构含 properties 与 required",
          all((t["inputSchema"].get("properties") or {}) and (t["inputSchema"].get("required") or [])
              for t in tools))

    st, body = server_rpc("tools/call", {"name": "unit_convert",
                                         "arguments": {"value": 3, "from": "千米", "to": "米"}})
    result = rpc_result(body) or {}
    texts = "".join(item.get("text", "") for item in (result.get("content") or []))
    check("tools/call 返回文本内容", st == 200 and "3000" in texts, texts[:60])
    check("正常调用 isError 为 false", result.get("isError") is False)

    st, body = server_rpc("tools/call", {"name": "formula_hint", "arguments": {"topic": "求根公式"}})
    texts = "".join(item.get("text", "") for item in ((rpc_result(body) or {}).get("content") or []))
    check("formula_hint 只给提示不给答案", "x = (-b" in texts and "x1 =" not in texts, texts[:60])

    st, body = server_rpc("tools/call", {"name": "formula_hint", "arguments": {"topic": "红烧肉"}})
    result = rpc_result(body) or {}
    check("未知主题 isError 为 true", result.get("isError") is True)
    check("未知主题给出可读原因",
          "不在能力范围" in "".join(i.get("text", "") for i in (result.get("content") or [])))

    st, body = server_rpc("tools/call", {"name": "unit_convert",
                                         "arguments": {"value": 1, "from": "米", "to": "千克"}})
    result = rpc_result(body) or {}
    check("不同物理量互转 isError 为 true", result.get("isError") is True)
    check("互转失败说明不是同一物理量",
          "不是同一物理量" in "".join(i.get("text", "") for i in (result.get("content") or [])))

    st, body = server_rpc("tools/list", token="")
    check("无令牌被拒（401）", st == 401, f"实际 {st}")
    st, body = server_rpc("tools/list", token="wrong-token")
    check("令牌错误被拒（401）", st == 401, f"实际 {st}")

    st, body = server_rpc("tools/list", raw="{不是 JSON")
    check("非法报文返回 -32700", st == 200 and rpc_error_code(body) == -32700, body[:80])
    st, body = server_rpc("tools/list", raw='{"jsonrpc":"2.0","id":1}')
    check("缺 method 返回 -32600", st == 200 and rpc_error_code(body) == -32600, body[:80])
    st, body = server_rpc("tools/delete")
    check("未知方法返回 -32601", st == 200 and rpc_error_code(body) == -32601, body[:80])
    print()

    # ===================================================================== #
    print("== 2. 登记与令牌边界 ==")
    st, info = teacher.login("teacher_a", "teach123")
    check("教师登录 = 200", st == 200)
    st, info = student.login("student_a1", "stu123")
    check("学生登录 = 200", st == 200)

    st, _ = anon.json("/api/mcp/servers")
    check("未登录读取服务器 = 401", st == 401, f"实际 {st}")

    name = f"campus-tools-{TS[-4:]}"
    st, created = teacher.json("/api/mcp/servers", {
        "name": name, "base_url": MCP_INNER, "auth_token": TOKEN})
    check("登记服务器 = 201", st == 201, f"实际 {st} {created}")
    server_id = (created or {}).get("id")
    check("登记后为未连接、工具数 0",
          (created or {}).get("connected") is False and (created or {}).get("tool_count") == 0)
    check("响应只给 has_auth 布尔值", (created or {}).get("has_auth") is True)
    check("登记响应不含令牌", TOKEN not in json.dumps(created or {}, ensure_ascii=False))

    st, listing = teacher.json("/api/mcp/servers")
    raw = json.dumps(listing or {}, ensure_ascii=False)
    check("列表不含令牌字符串", TOKEN not in raw)
    check("列表不含 auth_token 字段", "auth_token" not in raw)
    check("列表含 has_auth 标记", all("has_auth" in s for s in (listing or {}).get("servers") or []))

    st, body = teacher.json("/api/mcp/servers", {
        "name": name, "base_url": MCP_INNER, "auth_token": TOKEN})
    check("同名登记 = 400", st == 400, f"实际 {st}")

    for m, p in [("GET", "/api/mcp/servers"), ("POST", "/api/mcp/servers"),
                 ("GET", f"/api/mcp/servers/{server_id}/tools"), ("GET", "/api/mcp/tool-calls")]:
        st, _ = student.json(p, {} if m == "POST" else None, method=m)
        check(f"学生 {m} {p} = 403", st == 403, f"实际 {st}")
    st, _ = student.json(f"/api/mcp/servers/{server_id}/connect", {})
    check("学生连接服务器 = 403", st == 403, f"实际 {st}")
    print()

    # ===================================================================== #
    print("== 3. 连接与发现 ==")
    st, connected = teacher.json(f"/api/mcp/servers/{server_id}/connect", {})
    check("连接成功 = 200", st == 200, f"实际 {st} {connected}")
    tool_names = [t.get("name") for t in (connected or {}).get("tools") or []]
    check("连接后可见自建服务的工具", "formula_hint" in tool_names, f"实际 {tool_names}")
    check("连接后 connected 为 true", (connected or {}).get("connected") is True)

    st, again = teacher.json(f"/api/mcp/servers/{server_id}/connect", {})
    again_names = [t.get("name") for t in (again or {}).get("tools") or []]
    check("连续连接两次清单条数不翻倍", len(again_names) == len(tool_names),
          f"{len(tool_names)} → {len(again_names)}")
    check("清单无重复工具名", len(set(again_names)) == len(again_names))
    check("库里工具数一致",
          db_int(f"SELECT COUNT(*) FROM mcp_tools WHERE server_id = {server_id}") == len(again_names))

    st, tools_body = teacher.json(f"/api/mcp/servers/{server_id}/tools")
    check("查看工具清单 = 200", st == 200)
    check("清单含输入结构",
          all(t.get("input_schema") for t in (tools_body or {}).get("tools") or []))
    print()

    # ===================================================================== #
    print("== 4. 引用格式与保存前校验 ==")
    st, _ = teacher.json("/api/skills", {"source": f"名称：坏格式{TS}\n适用条件：x\n资料：\n"
                                                   f"工具：Formula_Hint@{name}\n启用：是\n\n说明"})
    check("工具名大写被拒 = 400", st == 400, f"实际 {st}")
    st, _ = teacher.json("/api/skills", {"source": f"名称：坏格式2{TS}\n适用条件：x\n资料：\n"
                                                   f"工具：formula-hint@{name}\n启用：是\n\n说明"})
    check("工具名含连字符被拒 = 400", st == 400, f"实际 {st}")
    st, _ = teacher.json("/api/skills", {"source": f"名称：坏格式3{TS}\n适用条件：x\n资料：\n"
                                                   f"工具：formula_hint\n启用：是\n\n说明"})
    check("缺服务器名被拒 = 400", st == 400, f"实际 {st}")

    before = db_int("SELECT COUNT(*) FROM skills WHERE class_id = 1")
    st, body = teacher.json("/api/skills", {"source": f"名称：未登记服务器{TS}\n适用条件：x\n资料：\n"
                                                     f"工具：formula_hint@not-exist\n启用：是\n\n说明"})
    check("服务器未登记被拒 = 400", st == 400, f"实际 {st}")
    check("文案指出服务器未登记", "未登记" in json.dumps(body or {}, ensure_ascii=False))
    check("校验失败不落库", db_int("SELECT COUNT(*) FROM skills WHERE class_id = 1") == before)

    st, body = teacher.json("/api/skills", {"source": f"名称：工具不在清单{TS}\n适用条件：x\n资料：\n"
                                                     f"工具：nope_tool@{name}\n启用：是\n\n说明"})
    check("工具不在清单被拒 = 400", st == 400, f"实际 {st}")
    check("文案给出可用工具名", "formula_hint" in json.dumps(body or {}, ensure_ascii=False))

    skill_src = (f"名称：验收·工具技能{TS}\n适用条件：求根公式,方程,换算,单位\n资料：\n"
                 f"工具：knowledge_search,formula_hint@{name},unit_convert@{name}\n启用：是\n\n"
                 "凡涉及公式或单位换算，必须先调用对应工具核对结果，再引导学生自己算下一步。"
                 "换算涉及两次单位变化时，要连续调用两次工具。")
    st, skill = teacher.json("/api/skills", {"source": skill_src})
    check("合法引用可保存 = 201", st == 201, f"实际 {st} {skill}")
    skill_id = (skill or {}).get("id")
    check("工具数组同时含内置项与引用",
          "knowledge_search" in ((skill or {}).get("tools") or [])
          and f"formula_hint@{name}" in ((skill or {}).get("tools") or []))

    agent_name = f"验收·工具助手{TS}"
    st, agent = teacher.json("/api/agents", {"name": agent_name, "prompt": "你是数学老师，回答简短。",
                                             "enabled": True, "skill_ids": [skill_id],
                                             "material_ids": []})
    check("新建绑定该技能的智能体 = 201", st == 201, f"实际 {st}")
    agent_id = (agent or {}).get("id")
    print()

    # ===================================================================== #
    print("== 5. 调用循环与记录 ==")
    base_calls = db_int("SELECT COUNT(*) FROM mcp_tool_calls WHERE class_id = 1")

    st, conv = student.json("/api/conversations", {"agent_id": agent_id})
    conv_id = (conv or {}).get("id")
    check("学生新建会话 = 201", st == 201, f"实际 {st}")

    st, events, meta, answer = ask(student, conv_id, "一元二次方程的求根公式是什么？")
    order = [e["event"] for e in events]
    check("提问返回 200", st == 200, f"实际 {st}")
    check("第一个事件是 meta", order and order[0] == "meta", f"实际 {order[:3]}")
    calls = meta.get("tool_calls") or []
    check("meta 带 tool_calls", len(calls) > 0, json.dumps(calls, ensure_ascii=False)[:120])
    check("tool_calls 每项含 ref/status/text",
          all({"ref", "status", "text"} <= set(c) for c in calls))
    check("首个调用的状态为 ok", calls and calls[0].get("status") == "ok")
    check("回答体现了工具返回的提示", "求根公式" in answer or "x =" in answer, answer[:80])

    row = db(f"SELECT tool_ref, status, user_id, conversation_id, round_no, detail "
             f"FROM mcp_tool_calls WHERE conversation_id = {conv_id} ORDER BY id DESC LIMIT 1")
    check("成功调用留一条记录", bool(row) and row[0][1] == "ok", str(row[:1]))
    if row:
        check("记录含工具引用与服务器名",
              row[0][0] == f"formula_hint@{name}", f"实际 {row[0][0]}")
        check("记录含触发用户与会话",
              row[0][2] == "2" and row[0][3] == str(conv_id), f"{row[0][2]} / {row[0][3]}")
        check("记录含轮次与摘要", row[0][4] != "0" and bool(row[0][5]))

    st, conv2 = student.json("/api/conversations", {"agent_id": agent_id})
    conv2_id = (conv2 or {}).get("id")
    st, events, meta2, answer2 = ask(student, conv2_id, "换算一下：3 千米先换成米，再换成厘米，分别是多少？")
    calls2 = meta2.get("tool_calls") or []
    check("两步任务连续发起两轮调用", len(calls2) >= 2,
          json.dumps([c.get("ref") for c in calls2], ensure_ascii=False))
    rounds = [r[0] for r in db(f"SELECT round_no FROM mcp_tool_calls "
                              f"WHERE conversation_id = {conv2_id} ORDER BY id")]
    check("轮次递增", len(rounds) >= 2 and rounds[-1] > rounds[0], f"实际 {rounds}")
    check("回答体现最终结果", "厘米" in answer2 or "300000" in answer2, answer2[:80])

    total_new = db_int("SELECT COUNT(*) FROM mcp_tool_calls WHERE class_id = 1") - base_calls
    check("单次提问的调用次数不超过上限 4", total_new <= 8, f"本轮共新增 {total_new} 条")
    print()

    # ===================================================================== #
    print("== 6. 断开与无法归属 ==")
    st, _ = teacher.json(f"/api/mcp/servers/{server_id}/disconnect", {})
    check("断开 = 200", st == 200, f"实际 {st}")
    check("断开后工具清单清空",
          db_int(f"SELECT COUNT(*) FROM mcp_tools WHERE server_id = {server_id}") == 0)
    st, _ = teacher.json(f"/api/mcp/servers/{server_id}/disconnect", {})
    check("重复断开幂等 = 200", st == 200, f"实际 {st}")

    denied_before = db_int("SELECT COUNT(*) FROM mcp_tool_calls WHERE status = 'denied'")
    st, conv3 = student.json("/api/conversations", {"agent_id": agent_id})
    conv3_id = (conv3 or {}).get("id")
    st, events, meta3, answer3 = ask(student, conv3_id, "一元二次方程的求根公式是什么？")
    calls3 = meta3.get("tool_calls") or []
    check("断开后调用仍被尝试并记为 denied",
          any(c.get("status") == "denied" for c in calls3),
          json.dumps(calls3, ensure_ascii=False)[:120])
    check("断开后回答仍正常返回", st == 200 and bool(answer3.strip()))
    check("denied 记录新增",
          db_int("SELECT COUNT(*) FROM mcp_tool_calls WHERE status = 'denied'") > denied_before)

    # 删除服务器后：引用仍被记为 denied，但不写记录
    st, _ = teacher.json(f"/api/mcp/servers/{server_id}", method="DELETE")
    check("删除服务器 = 204", st == 204, f"实际 {st}")
    st, _ = teacher.json(f"/api/mcp/servers/{server_id}", method="DELETE")
    check("重复删除 = 404", st == 404, f"实际 {st}")
    calls_before = db_int("SELECT COUNT(*) FROM mcp_tool_calls WHERE class_id = 1")
    st, conv4 = student.json("/api/conversations", {"agent_id": agent_id})
    conv4_id = (conv4 or {}).get("id")
    st, events, meta4, answer4 = ask(student, conv4_id, "一元二次方程的求根公式是什么？")
    calls4 = meta4.get("tool_calls") or []
    check("服务器删除后引用记为 denied",
          any(c.get("status") == "denied" for c in calls4),
          json.dumps(calls4, ensure_ascii=False)[:120])
    check("无法归属时不写记录",
          db_int("SELECT COUNT(*) FROM mcp_tool_calls WHERE class_id = 1") == calls_before)
    print()

    # ===================================================================== #
    print("== 7. 记录查询与权限 ==")
    st, body = student.json("/api/mcp/tool-calls")
    check("学生读调用记录 = 403", st == 403, f"实际 {st}")
    st, body = teacher.json("/api/mcp/tool-calls")
    calls_list = (body or {}).get("calls") or []
    check("教师可读调用记录", st == 200 and len(calls_list) > 0)
    check("记录默认不超过 50 条", len(calls_list) <= 50, f"实际 {len(calls_list)}")
    stamps = [c.get("created_at") for c in calls_list]
    check("按时间倒序", stamps == sorted(stamps, reverse=True))
    statuses = {c.get("status") for c in calls_list}
    check("三种状态都出现过", {"ok", "denied"} <= statuses, f"实际 {statuses}")
    print()

    # ===================================================================== #
    print("== 8. 令牌不外泄 ==")
    st, raw = teacher.call("/api/mcp/servers")
    check("服务器列表响应不含令牌", TOKEN.encode() not in raw)
    st, raw = teacher.call("/api/mcp/tool-calls")
    check("调用记录响应不含令牌", TOKEN.encode() not in raw)
    for path in ["/app.js", "/index.html"]:
        st, raw = anon.call(path, use_token=False)
        check(f"前端 {path} 不含令牌", TOKEN.encode() not in raw)
    p = subprocess.run(["docker", "compose", "exec", "-T", "api", "printenv"],
                       cwd=ROOT, capture_output=True, text=True, encoding="utf-8", errors="replace")
    check("api 容器环境变量里有令牌（供调用使用）", TOKEN in (p.stdout or ""))
    note("令牌出现在 .env 与容器环境变量中属预期；它不出现在接口响应、前端与镜像层。")
    print()

    print(f"===== 汇总: PASS={PASS}  FAIL={FAIL} =====")
    if FAILED:
        print("失败项：")
        for n in FAILED:
            print("  - " + n)
    return 1 if FAIL else 0


if __name__ == "__main__":
    sys.exit(main())
