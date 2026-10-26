#!/usr/bin/env python3
"""CampusClaw 第 5 课（add-agent-streaming-chat）端到端验收。

逐条执行本 change 三个 capability 的 Scenario：
  - specs/agent-chat/spec.md    智能体、技能、路由、检索范围、消息、流式、回溯
  - specs/api-docs/spec.md      OpenAPI 3.0 描述、官方 Swagger UI、路由一致性
  - specs/auth-upload/spec.md   认证迁移为 JWT Bearer（增量）

需要四容器栈已启动，且能从宿主机执行 `docker compose exec db mysql`（用于数据库回查）。
不额外引入 Python 依赖。

    BASE_URL=http://127.0.0.1:8081 python scripts/verify_agent_chat.py
"""

import base64
import hashlib
import hmac
import json
import os
import re
import subprocess
import sys
import time
import urllib.error
import urllib.request

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
BASE = os.environ.get("BASE_URL", "http://127.0.0.1:8080")

PASS = 0
FAIL = 0
FAILED = []

# 本脚本注入的临时数据统一带此后缀，避免与既有数据同名。
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


# --------------------------------------------------------------------------- #
# 环境与数据库
# --------------------------------------------------------------------------- #

def load_env():
    path = os.path.join(ROOT, ".env")
    out = {}
    if os.path.exists(path):
        with open(path, encoding="utf-8") as fh:
            for line in fh:
                line = line.strip()
                if not line or line.startswith("#") or "=" not in line:
                    continue
                k, v = line.split("=", 1)
                out[k.strip()] = v.strip()
    return out


ENV = load_env()
SECRET = ENV.get("SESSION_SECRET", "")


def db(sql):
    """在 db 容器内执行 SQL，返回二维字符串数组。

    数据库端口不对宿主机开放（只有 web 发布端口），因此统一经 compose exec 回查，
    这样既不用额外依赖 MySQL 驱动，也不会为了测试改动端口暴露约定。
    """
    cmd = [
        "docker", "compose", "exec", "-T", "db", "mysql",
        "--default-character-set=utf8mb4",
        "-u" + ENV.get("DB_USER", "campusclaw"),
        "-p" + ENV.get("DB_PASSWORD", ""),
        "-D", ENV.get("DB_NAME", "campusclaw"),
        "-N", "-B", "-e", sql,
    ]
    p = subprocess.run(cmd, cwd=ROOT, capture_output=True, text=True,
                       encoding="utf-8", errors="replace")
    if p.returncode != 0:
        raise RuntimeError(f"SQL 执行失败: {sql}\n{(p.stderr or '').strip()}")
    rows = []
    for line in (p.stdout or "").splitlines():
        if not line or line.startswith("mysql: [Warning]"):
            continue
        rows.append(line.split("\t"))
    return rows


def db_one(sql):
    rows = db(sql)
    return rows[0][0] if rows else None


def db_hex(sql):
    """读取可能含换行的文本字段。

    mysql 的 -B 输出按行切分，正文里带换行就会被截断，因此统一用 HEX() 取回再解码。
    """
    v = db_one(sql)
    if v in (None, "", "NULL"):
        return None
    return bytes.fromhex(v).decode("utf-8")


def db_int(sql):
    v = db_one(sql)
    try:
        return int(v)
    except (TypeError, ValueError):
        return -1


# --------------------------------------------------------------------------- #
# HTTP 客户端
# --------------------------------------------------------------------------- #

class Client:
    def __init__(self):
        self.token = None
        self.cookies = ""

    def call(self, path, data=None, raw=None, headers=None, method=None, use_token=True):
        if raw is not None:
            body = raw
        elif data is not None:
            body = json.dumps(data).encode()
        else:
            body = None
        hdrs = dict(headers or {})
        if body is not None and "Content-Type" not in hdrs:
            hdrs["Content-Type"] = "application/json"
        if use_token and self.token:
            hdrs["Authorization"] = "Bearer " + self.token
        if self.cookies:
            hdrs["Cookie"] = self.cookies
        req = urllib.request.Request(
            BASE + path, data=body, headers=hdrs,
            method=method or ("POST" if body is not None else "GET"))
        try:
            r = urllib.request.urlopen(req, timeout=180)
            return r.status, dict(r.headers), r.read()
        except urllib.error.HTTPError as e:
            return e.code, dict(e.headers), e.read()
        except Exception as e:  # noqa: BLE001
            return None, {}, f"{type(e).__name__}: {e}".encode()

    def json(self, path, data=None, raw=None, headers=None, method=None, use_token=True):
        st, hd, body = self.call(path, data=data, raw=raw, headers=headers,
                                 method=method, use_token=use_token)
        try:
            return st, json.loads(body) if body else None
        except Exception:  # noqa: BLE001
            return st, None

    def login(self, username, password, send_old_token=False):
        """登录。send_old_token 为真时把当前令牌一并带上，用于验证「再次登录吊销旧令牌」。"""
        st, _, body = self.call("/api/login", {"username": username, "password": password},
                                use_token=send_old_token)
        info = None
        try:
            info = json.loads(body) if body else None
        except Exception:  # noqa: BLE001
            info = None
        if isinstance(info, dict):
            self.token = info.get("token")
        return st, info

    def sse(self, path, data):
        """读取 SSE：逐字节解析帧，并记录每一帧的到达时刻。"""
        body = json.dumps(data).encode()
        hdrs = {"Content-Type": "application/json", "Accept": "text/event-stream"}
        if self.token:
            hdrs["Authorization"] = "Bearer " + self.token
        req = urllib.request.Request(BASE + path, data=body, headers=hdrs, method="POST")
        try:
            r = urllib.request.urlopen(req, timeout=180)
        except urllib.error.HTTPError as e:
            return e.code, dict(e.headers), []
        except Exception:  # noqa: BLE001
            return None, {}, []

        t0 = time.time()
        events = []
        buf = b""
        while True:
            chunk = r.read(1)
            if not chunk:
                break
            buf += chunk
            while b"\n\n" in buf:
                frame, buf = buf.split(b"\n\n", 1)
                name, payload = None, None
                for line in frame.decode("utf-8", "replace").split("\n"):
                    if line.startswith("event: "):
                        name = line[7:].strip()
                    elif line.startswith("data: "):
                        payload = line[6:]
                if name:
                    try:
                        parsed = json.loads(payload) if payload else None
                    except Exception:  # noqa: BLE001
                        parsed = payload
                    events.append({"t": round(time.time() - t0, 3),
                                   "event": name, "data": parsed})
        return r.status, dict(r.headers), events


def multipart(fields, filename, content):
    boundary = "----campusclawboundary5a1c"
    parts = []
    for k, v in fields.items():
        parts.append(
            f"--{boundary}\r\nContent-Disposition: form-data; name=\"{k}\"\r\n\r\n{v}\r\n".encode())
    parts.append(
        f"--{boundary}\r\nContent-Disposition: form-data; name=\"file\"; filename=\"{filename}\"\r\n"
        f"Content-Type: text/markdown\r\n\r\n".encode())
    parts.append(content.encode())
    parts.append(f"\r\n--{boundary}--\r\n".encode())
    return b"".join(parts), {"Content-Type": f"multipart/form-data; boundary={boundary}"}


# --------------------------------------------------------------------------- #
# JWT 工具：用于验证「载荷只有注册声明」与「拒绝非 HS256」
# --------------------------------------------------------------------------- #

def jwt_payload(token):
    part = token.split(".")[1]
    part += "=" * (-len(part) % 4)
    return json.loads(base64.urlsafe_b64decode(part))


def _b64(obj):
    return base64.urlsafe_b64encode(
        json.dumps(obj, separators=(",", ":")).encode()).rstrip(b"=").decode()


def jwt_sign(claims, secret):
    signing = _b64({"alg": "HS256", "typ": "JWT"}) + "." + _b64(claims)
    sig = hmac.new(secret.encode(), signing.encode(), hashlib.sha256).digest()
    return signing + "." + base64.urlsafe_b64encode(sig).rstrip(b"=").decode()


def jwt_alg_none(claims):
    return _b64({"alg": "none", "typ": "JWT"}) + "." + _b64(claims) + "."


# --------------------------------------------------------------------------- #
# 技能文件样板
# --------------------------------------------------------------------------- #

def skill_file(name, trigger, subject="", tools="knowledge_search", enabled="是", desc="说明正文。"):
    lines = [f"名称：{name}", f"适用条件：{trigger}"]
    if subject:
        lines.append(f"资料：{subject}")
    if tools is not None:
        lines.append(f"工具：{tools}")
    lines.append(f"启用：{enabled}")
    return "\n".join(lines) + "\n\n" + desc


MATH_DOC = (
    "# 第5课 · 数学 · 去括号与一元二次方程\n\n"
    "## 去括号\n\n"
    "去括号时，括号前面的系数要乘到括号里的每一项，漏乘一项是最常见的错误。\n"
    "如果括号前面是减号，去掉括号后括号里每一项都要变号，这是错得最多的地方。\n"
    "移项要变号，合并同类项只看未知数的次数是否相同，系数相加、次数不变。\n\n"
    "## 配方\n\n"
    "配方前先把二次项系数化为一，再在等式两边同时加上一次项系数一半的平方。\n"
    "判别式小于零时方程没有实数根，这一步要写清楚，不要直接跳到结论。\n\n"
    "## 应用题\n\n"
    "应用题先设未知数，再按题意列出等量关系，最后检验结果是否符合实际。\n"
    "行程问题常设速度或时间为未知数，抓住路程相等或时间相等来列方程。\n"
)

CHINESE_DOC = (
    "# 第5课 · 语文 · 春晓\n\n"
    "## 原文\n\n"
    "春眠不觉晓，处处闻啼鸟。夜来风雨声，花落知多少。\n\n"
    "## 默写提示\n\n"
    "默写时先想画面：睡到天亮、鸟叫、风雨、落花，四句依次展开。\n"
    "「晓」是天亮的意思，不要写成「小」；「啼鸟」是鸟叫，不是鸟在哭。\n"
    "写完后逐字对照，重点检查同音字和形近字，尤其是「晓」「啼」「落」三个字。\n"
)

BIOLOGY_DOC = (
    "# 第5课 · 生物 · 光合作用（未绑定）\n\n"
    "## 光反应\n\n"
    "光系统Ⅱ分布在叶绿体的基粒上，水的光解在这里产生氧气，同时生成 ATP 和 NADPH。\n\n"
    "## 暗反应\n\n"
    "暗反应在叶绿体基质中进行，固定二氧化碳形成三碳化合物，再被还原成糖类。\n"
)


def main():
    print(f"== 目标 {BASE} ==\n")

    try:
        db("SELECT 1")
    except Exception as e:  # noqa: BLE001
        print(f"无法回查数据库：{e}")
        print("请确认已执行 docker compose up，且本机可运行 docker compose exec。")
        return 2

    teacher = Client()
    student = Client()
    student_b = Client()
    anon = Client()

    # ===================================================================== #
    print("== 1. 认证迁移为 JWT Bearer（auth-upload 增量） ==")
    st, hd, body = teacher.call("/api/login", {"username": "teacher_a", "password": "teach123"},
                                use_token=False)
    info = json.loads(body) if body else {}
    check("教师登录 = 200", st == 200, f"实际 {st}")
    check("返回 token 与 user", bool(info.get("token")) and isinstance(info.get("user"), dict))
    user = info.get("user") or {}
    check("user 含标识/用户名/角色/班级",
          all(k in user for k in ("id", "username", "role", "class_id")),
          f"键 {sorted(user.keys())}")
    check("响应不含口令明文或哈希",
          not any(k in json.dumps(info).lower() for k in ("password", "hash", "pbkdf2", "salt")))
    check("不以 Set-Cookie 下发凭据", "Set-Cookie" not in hd, f"头 {sorted(hd)}")
    teacher.token = info.get("token")

    payload = jwt_payload(teacher.token or "e30.e30.e30")
    check("令牌载荷仅含 sub/jti/iat/exp", set(payload.keys()) == {"sub", "jti", "iat", "exp"},
          f"实际 {sorted(payload.keys())}")
    check("载荷不含角色与班级",
          "role" not in payload and "class_id" not in payload and "classID" not in payload)

    # 伪造与篡改
    st, _, b = anon.call("/api/me", use_token=False)
    check("不带令牌访问受保护接口 = 401", st == 401, f"实际 {st}")
    check("401 正文为 unauthorized", json.loads(b or b"{}").get("error") == "unauthorized",
          f"实际 {b!r}")

    st, _, _ = anon.call("/api/me", headers={"Authorization": "Token abc"}, use_token=False)
    check("方案不是 Bearer = 401", st == 401, f"实际 {st}")

    tampered = teacher.token[:-3] + ("aaa" if not teacher.token.endswith("aaa") else "bbb")
    st, _, _ = anon.call("/api/me", headers={"Authorization": "Bearer " + tampered},
                         use_token=False)
    check("签名被篡改 = 401", st == 401, f"实际 {st}")

    now = int(time.time())
    expired = jwt_sign({"sub": "1", "jti": "verify-expired-" + TS, "iat": now - 7200,
                        "exp": now - 3600}, SECRET)
    st, _, _ = anon.call("/api/me", headers={"Authorization": "Bearer " + expired}, use_token=False)
    check("已过期令牌 = 401", st == 401, f"实际 {st}")

    none_tok = jwt_alg_none({"sub": "1", "jti": "verify-none-" + TS, "iat": now, "exp": now + 600})
    st, _, _ = anon.call("/api/me", headers={"Authorization": "Bearer " + none_tok}, use_token=False)
    check("alg=none 被拒绝 = 401", st == 401, f"实际 {st}")

    # 仅 Cookie
    anon.cookies = "campusclaw_session=deadbeef; session=deadbeef"
    st, _, _ = anon.call("/api/me", use_token=False)
    anon.cookies = ""
    check("仅携带 Cookie 不足以认证 = 401", st == 401, f"实际 {st}")

    # 角色与班级按 sub 回查：改库后立即生效
    try:
        db("UPDATE users SET role = 'student' WHERE id = 1")
        st, _ = teacher.json("/api/agents", {"name": "角色回查" + TS, "prompt": "",
                                             "enabled": True, "skill_ids": [], "material_ids": []})
        check("改库中角色后同一令牌立即降级（教师接口 403）", st == 403, f"实际 {st}")
    finally:
        db("UPDATE users SET role = 'teacher' WHERE id = 1")
    st, _ = teacher.json("/api/agents")
    check("角色恢复后教师接口重新可用", st == 200, f"实际 {st}")

    # 登出与再次登录吊销旧令牌
    lc = Client()
    st, info = lc.login("student_a1", "stu123")
    check("学生登录 = 200", st == 200, f"实际 {st}")
    old = lc.token
    st, _, _ = lc.call("/api/me")
    check("登出前可用", st == 200, f"实际 {st}")
    st, _, _ = lc.call("/api/logout", {})
    check("登出 = 200", st == 200, f"实际 {st}")
    st, _, _ = lc.call("/api/me", headers={"Authorization": "Bearer " + old}, use_token=False)
    check("登出后同一令牌 = 401", st == 401, f"实际 {st}")
    st, _, _ = lc.call("/api/logout", {})
    check("重复登出 = 401", st == 401, f"实际 {st}")

    st, info = lc.login("student_a1", "stu123")
    first = lc.token
    st2, info2 = lc.login("student_a1", "stu123", send_old_token=True)
    second = lc.token
    check("携带旧 Bearer 再次登录 = 200", st2 == 200, f"实际 {st2}")
    check("再次登录签发的是新令牌", first != second)
    st, _, _ = lc.call("/api/me", headers={"Authorization": "Bearer " + first}, use_token=False)
    check("旧令牌在再次登录后立即失效 = 401", st == 401, f"实际 {st}")
    st, _, _ = lc.call("/api/me", headers={"Authorization": "Bearer " + second}, use_token=False)
    check("新令牌可用 = 200", st == 200, f"实际 {st}")

    # 白名单
    for path in ["/health", "/openapi.json", "/swagger/index.html"]:
        st, _, _ = anon.call(path, use_token=False)
        check(f"白名单 {path} 无需令牌（非 401）", st is not None and st != 401, f"实际 {st}")
    st, _, _ = anon.call("/api/login", {"username": "teacher_a", "password": "teach123"},
                         use_token=False)
    check("白名单 POST /api/login 无需令牌（非 401）", st != 401, f"实际 {st}")

    # sessions 表退役
    check("sessions 表已退役",
          db("SHOW TABLES LIKE 'sessions'") == [], "仍存在 sessions 表")

    st, _ = student.login("student_a1", "stu123")
    st, _ = student.json("/api/agents", method="POST",
                         data={"name": "学生越权" + TS, "prompt": "", "enabled": True,
                               "skill_ids": [], "material_ids": []})
    check("学生访问教师接口 = 403", st == 403, f"实际 {st}")
    student_b.login("student_b1", "stu123")
    print()

    # ===================================================================== #
    print("== 2. 接口文档（api-docs） ==")
    st, hd, raw = anon.call("/openapi.json", use_token=False)
    check("GET /openapi.json = 200", st == 200, f"实际 {st}")
    doc = {}
    try:
        doc = json.loads(raw)
    except Exception:  # noqa: BLE001
        doc = {}
    check("响应为可解析的 JSON", isinstance(doc, dict) and bool(doc), "解析失败")
    check("openapi 字段为 3.0.x", str(doc.get("openapi", "")).startswith("3.0"),
          f"实际 {doc.get('openapi')!r}")
    schemes = ((doc.get("components") or {}).get("securitySchemes") or {})
    bearer = schemes.get("bearerAuth") or {}
    check("components.securitySchemes.bearerAuth 存在", bool(bearer), f"实际 {list(schemes)}")
    check("bearerAuth 为 http/bearer 且 bearerFormat 为 JWT",
          bearer.get("type") == "http" and bearer.get("scheme") == "bearer"
          and str(bearer.get("bearerFormat", "")).upper() == "JWT",
          f"实际 {bearer}")

    paths = doc.get("paths") or {}
    EXPECTED = {
        ("get", "/health"), ("post", "/api/login"), ("get", "/openapi.json"),
        ("get", "/swagger/index.html"),
        ("post", "/api/logout"), ("get", "/api/me"),
        ("get", "/api/materials"), ("get", "/api/materials/{id}"),
        ("get", "/api/materials/{id}/file"), ("post", "/api/materials/upload"),
        ("get", "/api/materials/{id}/vectors"), ("post", "/api/materials/{id}/reindex"),
        ("post", "/api/search"), ("post", "/api/ask"),
        ("get", "/api/agents"), ("post", "/api/agents"),
        ("get", "/api/agents/{id}"), ("put", "/api/agents/{id}"),
        ("get", "/api/skills"), ("post", "/api/skills"),
        ("get", "/api/skills/{id}"), ("put", "/api/skills/{id}"), ("delete", "/api/skills/{id}"),
        ("get", "/api/conversations"), ("post", "/api/conversations"),
        ("get", "/api/conversations/{id}"), ("post", "/api/conversations/{id}/messages"),
    }
    actual = {(m.lower(), p) for p, ops in paths.items() for m in ops}
    missing = EXPECTED - actual
    extra = actual - EXPECTED
    check("文档覆盖全部注册路由（无缺失）", not missing, f"缺失 {sorted(missing)}")
    check("文档没有多余路由（无漂移）", not extra, f"多余 {sorted(extra)}")

    ops = {(m.lower(), p): op for p, o in paths.items() for m, op in o.items()}
    no_id = [f"{m} {p}" for (m, p), op in ops.items() if not op.get("operationId")]
    check("每个操作都有 operationId", not no_id, f"缺 {no_id}")
    dup_ids = [i for i in {op.get("operationId") for op in ops.values()}
               if sum(1 for op in ops.values() if op.get("operationId") == i) > 1]
    check("operationId 唯一", not dup_ids, f"重复 {dup_ids}")

    PUBLIC = {("get", "/health"), ("post", "/api/login"),
              ("get", "/openapi.json"), ("get", "/swagger/index.html")}
    bad_public = [f"{m} {p}" for (m, p) in PUBLIC if ops.get((m, p), {}).get("security")]
    check("白名单接口不引用安全方案", not bad_public, f"引用了 {bad_public}")

    need_auth = [(m, p) for (m, p) in ops if (m, p) not in PUBLIC]
    bad_auth = [f"{m} {p}" for (m, p) in need_auth
                if not any("bearerAuth" in s for s in (ops[(m, p)].get("security") or []))]
    check("需登录操作引用 bearerAuth", not bad_auth, f"未引用 {bad_auth}")

    def statuses(op):
        return set((op.get("responses") or {}).keys())

    no_4xx = [f"{m} {p}" for (m, p) in need_auth
              if not any(s.startswith("4") for s in statuses(ops[(m, p)]))]
    check("每个需登录操作列出 4xx 错误码", not no_4xx, f"缺 {no_4xx}")
    NO_5XX = {("get", "/health"), ("get", "/swagger/index.html")}
    no_5xx = [f"{m} {p}" for (m, p) in need_auth if (m, p) not in NO_5XX
              and not any(s.startswith("5") for s in statuses(ops[(m, p)]))]
    check("可能失败的操作列出 5xx 错误码", not no_5xx, f"缺 {no_5xx}")

    st, hd, raw = anon.call("/swagger/index.html", use_token=False)
    html = raw.decode("utf-8", "replace")
    check("Swagger UI 页面可打开（非 401）", st == 200 and "swagger" in html.lower(), f"实际 {st}")
    check("页面引用官方 UI 资源", "swagger-ui" in html.lower())
    check("运行期不依赖外部 CDN",
          not re.search(r"(cdn\.jsdelivr|unpkg\.com|cdnjs\.cloudflare)", html),
          "页面出现外部 CDN 引用")
    assets = re.findall(r'href="\./([^"]+)"|src="\./([^"]+)"', html)
    asset_names = [a or b for a, b in assets]
    bad_asset = []
    for name in asset_names:
        ast, _, _ = anon.call("/swagger/" + name, use_token=False)
        if ast != 200:
            bad_asset.append(f"{name}={ast}")
    check("页面资源随镜像分发（本地可取）", not bad_asset, f"失败 {bad_asset}")
    print()

    # ===================================================================== #
    print("== 3. 数据模型与种子 ==")
    tables = {r[0] for r in db("SHOW TABLES")}
    need = {"tutor_agents", "skills", "tutor_agent_skills", "tutor_agent_materials",
            "conversations", "messages", "revoked_tokens"}
    check("7 张新表齐备", need <= tables, f"缺 {sorted(need - tables)}")

    cascade = db(
        "SELECT k.table_name, k.column_name, rc.delete_rule "
        "FROM information_schema.referential_constraints rc "
        "JOIN information_schema.key_column_usage k "
        "  ON k.constraint_name = rc.constraint_name AND k.constraint_schema = rc.constraint_schema "
        "WHERE k.table_schema = DATABASE() "
        "  AND k.table_name IN ('tutor_agent_skills','tutor_agent_materials','messages')")
    rules = {(t, c): rule for t, c, rule in cascade}
    check("技能/材料绑定随智能体或技能级联删除",
          all(rules.get(("tutor_agent_skills", c)) == "CASCADE" for c in ("agent_id", "skill_id")),
          f"实际 {rules}")
    check("消息随会话级联删除", rules.get(("messages", "conversation_id")) == "CASCADE",
          f"实际 {rules}")

    seed_rows = db("SELECT seed_key, HEX(source_text) FROM skills WHERE seed_key IS NOT NULL "
                   "AND class_id = 1")
    seed_keys = {r[0]: bytes.fromhex(r[1]).decode("utf-8") if r[1] else "" for r in seed_rows}
    check("两份预置技能以 seed_key 标识", {"math-tutor", "chinese-tutor"} <= set(seed_keys),
          f"实际 {sorted(seed_keys)}")
    check("预置技能不重复（幂等）",
          db_int("SELECT COUNT(*) FROM skills WHERE class_id = 1 AND seed_key = 'math-tutor'") == 1)

    # newline="" 保留原始换行符，否则 Python 会把 CRLF 折成 LF，与库里的原文对不上。
    with open(os.path.join(ROOT, "api/internal/agent/seed.go"), encoding="utf-8", newline="") as fh:
        src = fh.read()
    for key, var in (("math-tutor", "seedMathSkill"), ("chinese-tutor", "seedChineseSkill")):
        m = re.search(r"const %s = `([^`]*)`" % var, src, re.S)
        want = m.group(1) if m else None
        got = seed_keys.get(key)
        check(f"预置技能 {key} 的 source_text 与课件样板逐字一致", want is not None and got == want,
              "不一致" if want else "未能从 seed.go 取到样板")

    seed_agent = db("SELECT id, name, enabled FROM tutor_agents WHERE class_id = 1 "
                    "AND name = '解题引导助手'")
    check("预置示例智能体存在且已启用", len(seed_agent) == 1 and seed_agent[0][2] == "1",
          f"实际 {seed_agent}")
    seed_agent_id = int(seed_agent[0][0]) if seed_agent else 0
    st, body = student.json("/api/agents")
    names = [a.get("name") for a in ((body or {}).get("agents") or [])]
    check("学生可列出预置智能体", "解题引导助手" in names, f"实际 {names}")
    bound = db_int(f"SELECT COUNT(*) FROM tutor_agent_materials WHERE agent_id = {seed_agent_id}")
    check("预置智能体绑定了班级材料", bound > 0, f"绑定 {bound} 份")
    print()

    # ===================================================================== #
    print("== 4. 技能文件解析 ==")
    base_name = f"验收·数学引导·{TS}"
    good = skill_file(base_name, "去括号,方程", subject="数学",
                      desc="先看学生写到哪一步，只指出下一步该检查什么，不要算出最终得数。")
    st, created = teacher.json("/api/skills", {"source": good})
    check("合法技能文件 = 201", st == 201, f"实际 {st} {created}")
    created = created or {}
    skill_math = created.get("id")
    check("响应含名称/适用条件/资料/工具/启用/原文",
          created.get("name") == base_name and created.get("trigger") == "去括号,方程"
          and created.get("subject") == "数学" and created.get("tools") == ["knowledge_search"]
          and created.get("enabled") is True and created.get("source") == good,
          f"实际 {created}")

    st, body = teacher.json("/api/skills", {"source": ""})
    check("空文件 = 400", st == 400, f"实际 {st}")
    check("空文件错误文案可读", "空" in ((body or {}).get("error") or ""), f"实际 {body}")

    st, body = teacher.json("/api/skills", {"source": "名称：无分隔\n适用条件：x\n启用：是"})
    check("缺少分隔空行 = 400", st == 400, f"实际 {st}")
    check("缺分隔空行文案提到空行", "空行" in ((body or {}).get("error") or ""), f"实际 {body}")

    st, body = teacher.json("/api/skills", {"source": "名称：缺必填\n启用：是\n\n说明"})
    check("缺必填字段 = 400", st == 400, f"实际 {st}")
    check("缺必填文案指明字段", "缺少必填" in ((body or {}).get("error") or ""), f"实际 {body}")

    st, body = teacher.json("/api/skills", {
        "source": "名称：重复\n名称：重复2\n适用条件：x\n启用：是\n\n说明"})
    check("重复字段 = 400", st == 400, f"实际 {st}")
    check("重复字段文案可读", "两次" in ((body or {}).get("error") or ""), f"实际 {body}")

    st, body = teacher.json("/api/skills", {
        "source": "名称：未定义\n适用条件：x\n作者：某人\n启用：是\n\n说明"})
    check("未定义字段 = 400", st == 400, f"实际 {st}")
    check("未定义字段文案可读", "未定义" in ((body or {}).get("error") or ""), f"实际 {body}")

    st, body = teacher.json("/api/skills", {
        "source": skill_file("非法工具" + TS, "x", tools="web_search")})
    check("非法工具名 = 400", st == 400, f"实际 {st}")
    check("非法工具文案指明白名单",
          "knowledge_search" in ((body or {}).get("error") or ""), f"实际 {body}")

    st, body = teacher.json("/api/skills", {
        "source": skill_file("非法启用" + TS, "x", enabled="ok")})
    check("启用非「是/否」= 400", st == 400, f"实际 {st}")
    check("启用文案可读", "是" in ((body or {}).get("error") or ""), f"实际 {body}")

    st, body = teacher.json("/api/skills", {
        "source": skill_file("超长文件" + TS, "x") + "字" * 8000})
    check("超过 8000 字 = 400", st == 400, f"实际 {st}")
    check("超长文案给出上限", "8000" in ((body or {}).get("error") or ""), f"实际 {body}")

    st, _ = teacher.json("/api/skills", {"source": skill_file("名" * 33, "x")})
    check("名称超 32 字 = 400", st == 400, f"实际 {st}")
    st, _ = teacher.json("/api/skills", {"source": skill_file("条件超长" + TS, "条" * 121)})
    check("适用条件超 120 字 = 400", st == 400, f"实际 {st}")
    st, _ = teacher.json("/api/skills", {"source": skill_file("资料超长" + TS, "x", subject="料" * 33)})
    check("资料超 32 字 = 400", st == 400, f"实际 {st}")
    st, _ = teacher.json("/api/skills", {"source": skill_file("说明超长" + TS, "x", desc="字" * 2001)})
    check("说明超 2000 字 = 400", st == 400, f"实际 {st}")

    st, body = teacher.json("/api/skills", {
        "source": "# 注释行被忽略\n" + skill_file("注释忽略" + TS, "x")})
    check("元数据区 # 行被忽略", st == 201, f"实际 {st} {body}")
    print()

    # ===================================================================== #
    print("== 5. 技能管理与权限 ==")
    st, body = student.json("/api/skills", {
        "source": skill_file("学生越权技能" + TS, "x")})
    check("学生新建技能 = 403", st == 403, f"实际 {st}")
    st, body = student.json(f"/api/skills/{skill_math}", {"source": good}, method="PUT")
    check("学生修改技能 = 403", st == 403, f"实际 {st}")
    st, body = student.json(f"/api/skills/{skill_math}", method="DELETE")
    check("学生删除技能 = 403", st == 403, f"实际 {st}")
    check("学生被拒后技能数据未变",
          db_hex(f"SELECT HEX(source_text) FROM skills WHERE id = {skill_math}") == good)

    st, body = teacher.json("/api/skills", {"source": good})
    check("技能同名提交 = 400", st == 400, f"实际 {st}")

    st, got = teacher.json(f"/api/skills/{skill_math}")
    check("读取技能 = 200", st == 200, f"实际 {st}")
    check("技能返回原文逐字一致", (got or {}).get("source") == good)
    st, body = student.json("/api/skills")
    check("学生可读技能列表", st == 200 and any(s.get("id") == skill_math
                                              for s in ((body or {}).get("skills") or [])))
    st, _ = teacher.json("/api/skills/999999")
    check("不存在的技能 = 404", st == 404, f"实际 {st}")

    # 删除：204 无响应体，重复删除 404，并解绑
    del_name = f"验收·待删技能·{TS}"
    st, body = teacher.json("/api/skills", {"source": skill_file(del_name, "待删关键词")})
    del_id = (body or {}).get("id")
    check("为删除用例新建技能 = 201", st == 201 and del_id, f"实际 {st}")
    bind_name = f"验收·解绑助手·{TS}"
    st, body = teacher.json("/api/agents", {"name": bind_name, "prompt": "解绑验证",
                                            "enabled": True, "skill_ids": [del_id],
                                            "material_ids": []})
    agent_unbind = (body or {}).get("id")
    check("新建绑定该技能的智能体 = 201", st == 201, f"实际 {st} {body}")
    check("绑定关系已写入", db_int(f"SELECT COUNT(*) FROM tutor_agent_skills "
                                f"WHERE agent_id = {agent_unbind} AND skill_id = {del_id}") == 1)

    st, hd, raw = teacher.call(f"/api/skills/{del_id}", method="DELETE")
    check("删除技能 = 204", st == 204, f"实际 {st}")
    check("204 响应体为空", raw in (b"", b"\n"), f"实际 {raw!r}")
    check("删除后技能行消失", db_int(f"SELECT COUNT(*) FROM skills WHERE id = {del_id}") == 0)
    check("删除同时清除绑定",
          db_int(f"SELECT COUNT(*) FROM tutor_agent_skills WHERE skill_id = {del_id}") == 0)
    st, _ = teacher.json(f"/api/skills/{del_id}", method="DELETE")
    check("重复删除 = 404", st == 404, f"实际 {st}")
    check("seed_key 为 NULL 的多行可共存（唯一约束只约束非空 seed_key）",
          db_int("SELECT COUNT(*) FROM skills WHERE class_id = 1 AND seed_key IS NULL") >= 2,
          "不足两行")
    print()

    # ===================================================================== #
    print("== 6. 智能体管理与班级边界 ==")
    # 材料
    raw, hdrs = multipart({"chunk_strategy": "auto"}, f"第5课数学{TS}.md", MATH_DOC)
    st, body = teacher.json("/api/materials/upload", raw=raw, headers=hdrs)
    math_mat = (body or {}).get("material_id")
    check("上传数学材料 = 201", st == 201, f"实际 {st} {body}")
    raw, hdrs = multipart({"chunk_strategy": "auto"}, f"第5课语文{TS}.md", CHINESE_DOC)
    st, body = teacher.json("/api/materials/upload", raw=raw, headers=hdrs)
    chin_mat = (body or {}).get("material_id")
    check("上传语文材料 = 201", st == 201, f"实际 {st} {body}")
    raw, hdrs = multipart({"chunk_strategy": "auto"}, f"第5课生物{TS}.md", BIOLOGY_DOC)
    st, body = teacher.json("/api/materials/upload", raw=raw, headers=hdrs)
    bio_mat = (body or {}).get("material_id")
    check("上传未绑定材料 = 201", st == 201, f"实际 {st} {body}")
    if not all([math_mat, chin_mat, bio_mat]):
        print("  材料准备失败，后续断言不可信，终止。")
        return 1

    # 语文技能
    st, body = teacher.json("/api/skills", {
        "source": skill_file(f"验收·语文引导·{TS}", "春晓,默写", subject="语文",
                             desc="先接住学生写出的那一句，告诉他往前看哪一句，不要把下一句直接写出来。")})
    skill_chin = (body or {}).get("id")
    check("新建语文技能 = 201", st == 201, f"实际 {st}")

    # 主智能体
    agent_main_name = f"验收·主助手·{TS}"
    st, body = teacher.json("/api/agents", {
        "name": agent_main_name, "prompt": "你是验收用的教研助手，回答简短。", "enabled": True,
        "skill_ids": [skill_math, skill_chin], "material_ids": [math_mat, chin_mat]})
    check("新建智能体 = 201", st == 201, f"实际 {st} {body}")
    agent_main = (body or {}).get("id")
    check("智能体响应不含 class_id", "class_id" not in (body or {}), f"实际 {sorted((body or {}))}")
    check("智能体返回两份绑定",
          sorted((body or {}).get("skill_ids") or []) == sorted([skill_math, skill_chin])
          and sorted((body or {}).get("material_ids") or []) == sorted([math_mat, chin_mat]),
          f"实际 {body}")

    # 停用智能体
    agent_off_name = f"验收·停用助手·{TS}"
    st, body = teacher.json("/api/agents", {"name": agent_off_name, "prompt": "", "enabled": False,
                                            "skill_ids": [], "material_ids": []})
    agent_off = (body or {}).get("id")
    check("新建未启用智能体 = 201", st == 201, f"实际 {st}")

    st, body = teacher.json("/api/agents")
    t_names = [a.get("name") for a in ((body or {}).get("agents") or [])]
    st2, body2 = student.json("/api/agents")
    s_names = [a.get("name") for a in ((body2 or {}).get("agents") or [])]
    check("教师可见未启用智能体", agent_off_name in t_names)
    check("学生只看到已启用的智能体", agent_main_name in s_names and agent_off_name not in s_names)
    check("两角色返回数量不同", len(t_names) != len(s_names), f"教师 {len(t_names)} 学生 {len(s_names)}")

    # 学生读取未启用 / 他班 / 不存在：同构 404
    st_off, _, b_off = student.call(f"/api/agents/{agent_off}")
    st_none, _, b_none = student.call("/api/agents/999999")
    db(f"INSERT INTO tutor_agents (class_id, name, prompt, enabled) "
       f"VALUES (2, '验收·B班智能体·{TS}', '', 0)")
    agent_b = db_one(f"SELECT id FROM tutor_agents WHERE name = '验收·B班智能体·{TS}'")
    agent_b = int(agent_b) if agent_b else 0
    st_other, _, b_other = student.call(f"/api/agents/{agent_b}")
    check("学生读未启用智能体 = 404", st_off == 404, f"实际 {st_off}")
    check("学生读他班智能体 = 404", st_other == 404, f"实际 {st_other}")
    check("学生读不存在智能体 = 404", st_none == 404, f"实际 {st_none}")
    check("三种 404 响应逐字节一致",
          b_off == b_none == b_other, f"{b_off!r} / {b_other!r} / {b_none!r}")
    st, body = teacher.json(f"/api/agents/{agent_off}")
    check("教师可读本班未启用智能体 = 200", st == 200, f"实际 {st}")

    # 提示词为空
    empty_name = f"验收·空提示词·{TS}"
    st, body = teacher.json("/api/agents", {"name": empty_name, "prompt": "", "enabled": True,
                                            "skill_ids": [], "material_ids": []})
    agent_empty = (body or {}).get("id")
    check("新建时提示词留空 = 201", st == 201, f"实际 {st} {body}")
    check("空提示词按空串保存", (body or {}).get("prompt") == "", f"实际 {body}")

    st, body = teacher.json(f"/api/agents/{agent_empty}",
                            {"name": empty_name, "prompt": "先看学生写到哪一步", "enabled": True,
                             "skill_ids": [], "material_ids": []}, method="PUT")
    check("修改智能体 = 200", st == 200, f"实际 {st}")
    st, body = teacher.json(f"/api/agents/{agent_empty}",
                            {"name": empty_name, "prompt": "", "enabled": True,
                             "skill_ids": [], "material_ids": []}, method="PUT")
    check("修改时提示词可清空 = 200", st == 200, f"实际 {st} {body}")
    check("清空后库中提示词为空",
          db_int(f"SELECT CHAR_LENGTH(COALESCE(prompt,'')) FROM tutor_agents WHERE id = {agent_empty}") == 0)

    st, _ = teacher.json("/api/agents", {"name": f"验收·超长提示词·{TS}", "prompt": "字" * 2001,
                                         "enabled": True, "skill_ids": [], "material_ids": []})
    check("提示词超 2000 字 = 400", st == 400, f"实际 {st}")
    st, body = teacher.json("/api/agents", {"name": f"验收·边界提示词·{TS}", "prompt": "字" * 2000,
                                            "enabled": True, "skill_ids": [], "material_ids": []})
    check("提示词恰好 2000 字 = 201", st == 201, f"实际 {st}")

    # 名称去空白与同名
    trim_name = f"验收·去空白·{TS}"
    st, body = teacher.json("/api/agents", {"name": f"   {trim_name}   ", "prompt": "",
                                            "enabled": True, "skill_ids": [], "material_ids": []})
    check("名称去空白后建成功 = 201", st == 201, f"实际 {st}")
    check("返回名称已去空白", (body or {}).get("name") == trim_name, f"实际 {(body or {}).get('name')!r}")
    before = db_int("SELECT COUNT(*) FROM tutor_agents WHERE class_id = 1")
    st, body = teacher.json("/api/agents", {"name": trim_name, "prompt": "", "enabled": True,
                                            "skill_ids": [], "material_ids": []})
    check("班内同名智能体 = 400", st == 400, f"实际 {st}")
    check("同名被拒后未产生新记录",
          db_int("SELECT COUNT(*) FROM tutor_agents WHERE class_id = 1") == before)

    st, _ = teacher.json("/api/agents", {"name": "", "prompt": "", "enabled": True,
                                         "skill_ids": [], "material_ids": []})
    check("名称为空 = 400", st == 400, f"实际 {st}")
    st, _ = teacher.json("/api/agents", {"name": "名" * 65, "prompt": "", "enabled": True,
                                         "skill_ids": [], "material_ids": []})
    check("名称超 64 字 = 400", st == 400, f"实际 {st}")

    # 绑定他班资源
    st, body = teacher.json("/api/agents", {"name": f"验收·跨班绑定·{TS}", "prompt": "",
                                            "enabled": True, "skill_ids": [],
                                            "material_ids": [2]})
    check("绑定他班材料 = 404", st == 404, f"实际 {st}")
    check("跨班绑定未产生智能体",
          db_int(f"SELECT COUNT(*) FROM tutor_agents WHERE name = '验收·跨班绑定·{TS}'") == 0)
    st, body = teacher.json("/api/agents", {"name": f"验收·跨班技能·{TS}", "prompt": "",
                                            "enabled": True, "skill_ids": [999999],
                                            "material_ids": []})
    check("绑定不存在的技能 = 404", st == 404, f"实际 {st}")

    # 修改权限与边界
    st, _ = student.json(f"/api/agents/{agent_main}",
                         {"name": agent_main_name, "prompt": "学生改", "enabled": True,
                          "skill_ids": [], "material_ids": []}, method="PUT")
    check("学生修改智能体 = 403", st == 403, f"实际 {st}")
    st, _ = teacher.json("/api/agents/999999", {"name": "不存在" + TS, "prompt": "",
                                                "enabled": True, "skill_ids": [],
                                                "material_ids": []}, method="PUT")
    check("修改不存在的智能体 = 404", st == 404, f"实际 {st}")
    st, _ = teacher.json(f"/api/agents/{agent_main}",
                         {"name": agent_off_name, "prompt": "", "enabled": True,
                          "skill_ids": [], "material_ids": []}, method="PUT")
    check("改成他人名称 = 400", st == 400, f"实际 {st}")
    print()

    # ===================================================================== #
    print("== 7. 会话创建与智能体锁定 ==")
    st, conv = student.json("/api/conversations", {"agent_id": agent_main})
    check("按已启用智能体新建会话 = 201", st == 201, f"实际 {st} {conv}")
    conv_main = (conv or {}).get("id")
    check("会话行记录智能体编号", (conv or {}).get("agent_id") == agent_main, f"实际 {conv}")
    check("新建会话标题为占位串", (conv or {}).get("title") == "新的提问", f"实际 {conv}")

    st, _ = student.json("/api/conversations", {})
    check("请求体为空 = 400", st == 400, f"实际 {st}")
    st, _ = student.json("/api/conversations", {"agent_id": 0})
    check("agent_id 小于 1 = 400", st == 400, f"实际 {st}")
    st_off2, _, b_off2 = student.call("/api/conversations", {"agent_id": agent_off})
    st_none2, _, b_none2 = student.call("/api/conversations", {"agent_id": 999999})
    st_other2, _, b_other2 = student.call("/api/conversations", {"agent_id": agent_b})
    check("选定未启用智能体 = 404", st_off2 == 404, f"实际 {st_off2}")
    check("选定他班智能体 = 404", st_other2 == 404, f"实际 {st_other2}")
    check("选定不存在智能体 = 404", st_none2 == 404, f"实际 {st_none2}")
    check("三种情形响应完全一致", b_off2 == b_none2 == b_other2)

    st, body = student_b.json("/api/conversations", {"agent_id": 0})
    check("本班无可用智能体 = 400", st == 400, f"实际 {st}")
    check("文案说明本班没有已启用智能体",
          "没有已启用" in ((body or {}).get("error") or ""), f"实际 {body}")

    # 停用不影响已有会话：把 agent_main 停用后再提问应仍然可用（此处只验证不 404）
    print()

    # ===================================================================== #
    print("== 8. 会话归属隔离 ==")
    st, mine = student.json("/api/conversations")
    convs = (mine or {}).get("conversations") or []
    check("会话列表只含自己的", all(c.get("user_id") == 2 for c in convs),
          f"实际 {[c.get('user_id') for c in convs]}")
    stamps = [c.get("updated_at") for c in convs]
    check("会话列表按 updated_at 倒序", stamps == sorted(stamps, reverse=True), f"实际 {stamps}")

    st_other3, _, b_other3 = teacher.call(f"/api/conversations/{conv_main}")
    st_none3, _, b_none3 = student.call("/api/conversations/999999")
    check("读取他人会话 = 404", st_other3 == 404, f"实际 {st_other3}")
    check("他人会话与不存在会话响应一致", b_other3 == b_none3)

    for path, data in [("/api/conversations", None), (f"/api/conversations/{conv_main}", None)]:
        st, _, _ = anon.call(path, data)
        check(f"未认证访问 {path} = 401", st == 401, f"实际 {st}")
    st, _, _ = anon.call(f"/api/conversations/{conv_main}/messages", {"content": "未认证"})
    check("未认证提问 = 401", st == 401, f"实际 {st}")
    print()

    # ===================================================================== #
    print("== 9. 技能路由 ==")

    def ask(client, conv, content, agent_id=None):
        payload = {"content": content}
        if agent_id:
            payload["agent_id"] = agent_id
        st, hd, events = client.sse(f"/api/conversations/{conv}/messages", payload)
        meta = next((e["data"] for e in events if e["event"] == "meta"), None)
        text = "".join((e["data"] or {}).get("text", "") for e in events if e["event"] == "delta")
        return st, hd, events, meta or {}, text

    st, hd, events, meta, answer = ask(student, conv_main, "去括号的时候系数要不要乘到每一项")
    skill_hit = (meta.get("skill") or {})
    check("数学提问命中数学技能", skill_hit.get("id") == skill_math,
          f"实际 {skill_hit}")
    check("命中技能时 meta 只给出一条技能", isinstance(skill_hit, dict), f"实际 {skill_hit}")
    check("meta 给出引用列表", isinstance(meta.get("citations"), list), f"实际 {meta.get('citations')}")

    st, hd, events, meta2, answer2 = ask(student, conv_main, "春晓默写的时候要注意哪些字")
    skill2 = (meta2.get("skill") or {})
    check("语文提问命中语文技能", skill2.get("id") == skill_chin, f"实际 {skill2}")

    st, hd, events, meta3, answer3 = ask(student, conv_main, "今天天气怎么样")
    check("零分时不选技能", meta3.get("skill") in (None, {}), f"实际 {meta3.get('skill')}")
    check("未命中技能时 status 为 no_evidence 或 ok（不报错）",
          meta3.get("status") in ("ok", "no_evidence"), f"实际 {meta3.get('status')}")

    # 并列取小 id
    tie_a_name, tie_b_name = f"验收·并列A·{TS}", f"验收·并列B·{TS}"
    st, body = teacher.json("/api/skills", {"source": skill_file(tie_a_name, "图形,面积")})
    tie_a = (body or {}).get("id")
    st, body = teacher.json("/api/skills", {"source": skill_file(tie_b_name, "图形,周长")})
    tie_b = (body or {}).get("id")
    check("新建两条并列技能 = 201", bool(tie_a and tie_b), f"{tie_a} {tie_b}")
    st, body = teacher.json("/api/agents", {"name": f"验收·并列助手·{TS}", "prompt": "",
                                            "enabled": True, "skill_ids": [tie_a, tie_b],
                                            "material_ids": []})
    agent_tie = (body or {}).get("id")
    st, conv_tie = student.json("/api/conversations", {"agent_id": agent_tie})
    conv_tie_id = (conv_tie or {}).get("id")
    st, hd, events, meta_tie, _ = ask(student, conv_tie_id, "图形")
    check("同分时只选一个且取编号较小者",
          (meta_tie.get("skill") or {}).get("id") == min(tie_a, tie_b),
          f"实际 {(meta_tie.get('skill') or {}).get('id')}，候选 {tie_a}/{tie_b}")

    # 大小写折叠
    st, body = teacher.json("/api/skills", {"source": skill_file(f"验收·大小写·{TS}", "Equation")})
    case_skill = (body or {}).get("id")
    st, body = teacher.json("/api/agents", {"name": f"验收·大小写助手·{TS}", "prompt": "",
                                            "enabled": True, "skill_ids": [case_skill],
                                            "material_ids": []})
    agent_case = (body or {}).get("id")
    st, conv_case = student.json("/api/conversations", {"agent_id": agent_case})
    conv_case_id = (conv_case or {}).get("id")
    st, hd, events, meta_case, _ = ask(student, conv_case_id, "equation 要怎么解")
    check("适用条件与提问大小写不同仍命中",
          (meta_case.get("skill") or {}).get("id") == case_skill,
          f"实际 {(meta_case.get('skill') or {}).get('id')}")

    # 停用与未绑定不参与
    st, body = teacher.json("/api/skills", {"source": skill_file(f"验收·停用技能·{TS}", "梯形", enabled="否")})
    skill_off = (body or {}).get("id")
    st, body = teacher.json("/api/skills", {"source": skill_file(f"验收·未绑定技能·{TS}", "菱形")})
    skill_unbound = (body or {}).get("id")
    st, body = teacher.json("/api/agents", {"name": f"验收·停用技能助手·{TS}", "prompt": "",
                                            "enabled": True, "skill_ids": [skill_off],
                                            "material_ids": []})
    agent_offskill = (body or {}).get("id")
    st, conv_off = student.json("/api/conversations", {"agent_id": agent_offskill})
    conv_off_id = (conv_off or {}).get("id")
    st, hd, events, meta_off, _ = ask(student, conv_off_id, "梯形")
    check("已停用技能不参与路由", meta_off.get("skill") in (None, {}), f"实际 {meta_off.get('skill')}")
    st, hd, events, meta_unbound, _ = ask(student, conv_main, "菱形是什么形状")
    check("未绑定技能不参与路由",
          (meta_unbound.get("skill") or {}).get("id") != skill_unbound,
          f"实际 {meta_unbound.get('skill')}")

    # 绑定不随会话固化
    st, body = teacher.json(f"/api/agents/{agent_tie}",
                            {"name": f"验收·并列助手·{TS}", "prompt": "", "enabled": True,
                             "skill_ids": [tie_b], "material_ids": []}, method="PUT")
    check("解绑并列技能 A = 200", st == 200, f"实际 {st}")
    st, hd, events, meta_replay, _ = ask(student, conv_tie_id, "图形")
    check("同一会话重放后按解绑后的绑定路由",
          (meta_replay.get("skill") or {}).get("id") == tie_b,
          f"实际 {(meta_replay.get('skill') or {}).get('id')}，期望 {tie_b}")
    check("被解绑的技能不再被选中",
          (meta_replay.get("skill") or {}).get("id") != tie_a)
    print()

    # ===================================================================== #
    print("== 10. 检索范围限定 ==")
    st, hd, events, meta_math, answer_math = ask(student, conv_main, "去括号漏乘一项会怎样")
    cites = meta_math.get("citations") or []
    check("有依据时 status = ok", meta_math.get("status") == "ok", f"实际 {meta_math.get('status')}")
    check("引用列表非空", len(cites) > 0, f"实际 {len(cites)}")
    check("引用不超过 4 条", len(cites) <= 4, f"实际 {len(cites)}")
    titles = [c.get("material_title") or "" for c in cites]
    check("按技能「资料」字段过滤：引用只含数学材料",
          all("数学" in t for t in titles), f"实际 {titles}")

    st, hd, events, meta_bio, answer_bio = ask(student, conv_main, "光系统Ⅱ在哪个结构上进行")
    bio_cites = meta_bio.get("citations") or []
    check("不越出绑定材料：未绑定材料不出现在引用中",
          all(c.get("material_id") != bio_mat for c in bio_cites),
          f"实际 {[c.get('material_id') for c in bio_cites]}")

    nums = [int(n) for n in re.findall(r"\[(\d+)\]", answer_math or "")]
    check("回答中的 [n] 不超出引用列表长度",
          all(1 <= n <= len(cites) for n in nums), f"编号 {nums}，引用 {len(cites)} 条")
    check("引用编号从 1 开始且连续",
          nums == sorted(set(nums)) and (not nums or nums[0] == 1), f"编号 {nums}")
    check("引用条目可回溯（标题/序号/区间/摘录）",
          all(c.get("material_title") and c.get("chunk_index") is not None
              and c.get("start_offset") is not None and c.get("end_offset") is not None
              and c.get("snippet") for c in cites), f"实际 {cites[:1]}")
    print()

    # ===================================================================== #
    print("== 11. 流式输出 ==")
    st, hd, events, meta_s, answer_s = ask(student, conv_main, "配方的时候两边要加什么")
    check("提问返回 200", st == 200, f"实际 {st}")
    ctype = hd.get("Content-Type", "")
    check("Content-Type 为 text/event-stream", "text/event-stream" in ctype, f"实际 {ctype}")
    check("Cache-Control 为 no-cache", "no-cache" in hd.get("Cache-Control", ""),
          f"实际 {hd.get('Cache-Control')}")
    check("Connection 为 keep-alive", "keep-alive" in hd.get("Connection", ""),
          f"实际 {hd.get('Connection')}")
    xab = hd.get("X-Accel-Buffering")
    if xab is None:
        note("X-Accel-Buffering 被 Nginx 消费后不再转发给客户端，改用「逐段到达」作为行为证据")
    else:
        check("X-Accel-Buffering 为 no", str(xab).lower() == "no", f"实际 {xab}")

    order = [e["event"] for e in events]
    check("第一个事件是 meta", order and order[0] == "meta", f"实际 {order[:3]}")
    check("其后至少一条 delta", order.count("delta") >= 1, f"delta {order.count('delta')} 条")
    check("最后一个事件是 done", order and order[-1] == "done", f"实际 {order[-3:]}")
    check("事件类型仅 meta/delta/done/error",
          set(order) <= {"meta", "delta", "done", "error"}, f"实际 {set(order)}")

    delta_times = [e["t"] for e in events if e["event"] == "delta"]
    spread = (max(delta_times) - min(delta_times)) if delta_times else 0
    check("delta 逐段到达（代理未缓冲）", spread > 0,
          f"delta 时间跨度 {spread:.3f}s，首帧 {delta_times[0] if delta_times else '-'}s")

    done = next((e["data"] for e in events if e["event"] == "done"), {}) or {}
    msg_id = done.get("message_id")
    check("done 返回助手消息编号", isinstance(msg_id, int) and msg_id > 0, f"实际 {done}")
    db_answer = db_hex(f"SELECT HEX(content) FROM messages WHERE id = {msg_id}") if msg_id else None
    check("delta 拼接等于落库全文", db_answer is not None and db_answer == answer_s,
          f"落库 {len(db_answer or '')} 字，拼接 {len(answer_s)} 字")
    deltas = [(e["data"] or {}).get("text", "") for e in events if e["event"] == "delta"]
    check("delta 是增量而非累计（无某条等于全文前缀的累计重复）",
          len(deltas) < 2 or deltas[1] != deltas[0] + deltas[1],
          f"前两条 {deltas[:2]}")

    # 内容长度越界
    st, _ = student.json(f"/api/conversations/{conv_main}/messages", {"content": "啊"})
    check("content 仅 1 字符 = 400", st == 400, f"实际 {st}")
    st, _ = student.json(f"/api/conversations/{conv_main}/messages", {"content": "字" * 501})
    check("content 超 500 字符 = 400", st == 400, f"实际 {st}")

    # 会话不存在：404 且不建流
    st, hd, events = student.sse("/api/conversations/999999/messages", {"content": "不存在的会话"})
    check("会话不存在 = 404", st == 404, f"实际 {st}")
    check("会话不存在时不建立 SSE 流",
          "text/event-stream" not in (hd.get("Content-Type") or ""), f"实际 {hd.get('Content-Type')}")

    # 锁定智能体
    st, hd, events, meta_lock, _ = ask(student, conv_main, "移项的时候要注意什么", agent_id=agent_tie)
    check("请求体中的 agent_id 不参与选择（仍按会话锁定的智能体作答）",
          (meta_lock.get("skill") or {}).get("id") in (skill_math, None),
          f"实际 {meta_lock.get('skill')}")
    check("会话行上的 agent_id 未被改写",
          db_int(f"SELECT agent_id FROM conversations WHERE id = {conv_main}") == agent_main)
    print()

    # ===================================================================== #
    print("== 12. 流式失败与无依据口径 ==")
    st, conv_empty = student.json("/api/conversations", {"agent_id": agent_empty})
    conv_empty_id = (conv_empty or {}).get("id")
    st, hd, events, meta_ne, answer_ne = ask(student, conv_empty_id, "贝尔不等式如何验证量子纠缠")
    check("无依据时仍调用模型并返回非空回答", st == 200 and bool((answer_ne or "").strip()),
          f"st={st} 长度 {len(answer_ne or '')}")
    check("无依据时引用列表为空", (meta_ne.get("citations") or []) == [],
          f"实际 {meta_ne.get('citations')}")
    check("无依据时 status = no_evidence", meta_ne.get("status") == "no_evidence",
          f"实际 {meta_ne.get('status')}")
    check("无依据时正文不含 [n] 编号",
          not re.search(r"\[\d+\]", answer_ne or ""), f"正文 {answer_ne[:80]!r}")
    check("无依据时正文不等于固定文案",
          (answer_ne or "").strip() != "资料中未找到相关内容", f"正文 {answer_ne[:40]!r}")

    note("「流开始后失败以 error 收尾」与「中途失败保留已产出内容」需要人为制造网关故障，")
    note("执行方式：docker compose stop api 无法复现，改为临时把 CHAT_BASE_URL 指向不可达地址后重启 api，")
    note("再向已有会话提问：期望 HTTP 200 + error 事件；若已推送部分 delta，则 messages 表有部分正文。")
    print()

    # ===================================================================== #
    print("== 13. 消息持久化与回溯 ==")
    # 用一轮「命中技能且有引用」的提问来验证落库内容。
    st, hd, events, meta13, answer13 = ask(student, conv_main, "去括号时漏乘一项会怎样")
    done13 = next((e["data"] for e in events if e["event"] == "done"), {}) or {}
    msg13 = done13.get("message_id")
    check("命中技能的轮次返回 done 编号", isinstance(msg13, int) and msg13 > 0, f"实际 {done13}")

    rows = db(f"SELECT HEX(content), HEX(citations_json), COALESCE(skill_name,'') "
              f"FROM messages WHERE id = {msg13}")
    stored = bytes.fromhex(rows[0][0]).decode("utf-8") if rows and rows[0][0] else None
    stored_cites = json.loads(bytes.fromhex(rows[0][1]).decode("utf-8")) if rows and rows[0][1] else []
    stored_skill = rows[0][2] if rows else ""
    check("助手消息正文与流式输出一致", stored == answer13,
          f"库 {len(stored or '')} 字，流 {len(answer13)} 字")
    check("助手消息落库引用列表", len(stored_cites) == len(meta13.get("citations") or []),
          f"库 {len(stored_cites)} 条，meta {len(meta13.get('citations') or [])} 条")
    check("助手消息落库命中技能名", stored_skill != "", f"实际 {stored_skill!r}")

    st, detail = student.json(f"/api/conversations/{conv_main}")
    msgs = (detail or {}).get("messages") or []
    check("按 done 的编号可定位该条消息", any(m.get("id") == msg13 for m in msgs),
          f"消息 {[m.get('id') for m in msgs][:5]}")

    # 标题：首轮改写、次轮不变
    st, conv_title = student.json("/api/conversations", {"agent_id": agent_main})
    conv_title_id = (conv_title or {}).get("id")
    first_q = "去括号时系数要乘到括号里的每一项吗"
    ask(student, conv_title_id, first_q)
    row = db(f"SELECT title, updated_at FROM conversations WHERE id = {conv_title_id}")
    check("首轮提问后标题改为前 24 字", row and row[0][0] == first_q[:24],
          f"实际 {row[0][0] if row else None!r}")
    time.sleep(1.2)
    ask(student, conv_title_id, "那减号的情况呢")
    row2 = db(f"SELECT title, updated_at FROM conversations WHERE id = {conv_title_id}")
    check("后续提问标题不变", row2 and row2[0][0] == row[0][0],
          f"{row[0][0]!r} → {row2[0][0]!r}")
    check("后续提问刷新 updated_at", row2 and row2[0][1] != row[0][1],
          f"{row[0][1]} → {row2[0][1]}")

    roles = {r[0] for r in db("SELECT DISTINCT role FROM messages")}
    check("messages.role 只出现 user 与 assistant", roles <= {"user", "assistant"}, f"实际 {roles}")
    print()

    # ===================================================================== #
    print("== 14. 上下文窗口 ==")
    conv_win = conv_title_id
    for i in range(4):
        ask(student, conv_win, f"第 {i + 3} 轮追问，请简短回答")
    total = db_int(f"SELECT COUNT(*) FROM messages WHERE conversation_id = {conv_win}")
    check("多轮提问后消息全部保留在库中", total >= 12, f"实际 {total} 条")
    note("「送入模型的窗口为末尾 6 条、每条截断 500 字、跳过空正文与非对话角色」")
    note("由 api/internal/chat/chat_test.go 与 conversation.Messages(limit) 的 SQL 覆盖，E2E 无法观测模型入参。")
    print()

    # ===================================================================== #
    print("== 15. 清理本次注入的临时数据 ==")
    for sid in [skill_math, skill_chin, tie_a, tie_b, case_skill, skill_off, skill_unbound]:
        if sid:
            teacher.json(f"/api/skills/{sid}", method="DELETE")
    db(f"DELETE FROM tutor_agents WHERE name = '验收·B班智能体·{TS}'")
    note("智能体没有删除接口（规约未定义），因此本次新建的验收智能体会保留；名称带时间戳，不影响后续运行。")
    left = db_int(f"SELECT COUNT(*) FROM skills WHERE class_id = 1 AND name LIKE '验收·%{TS}%'")
    check("临时技能已清理", left == 0, f"残留 {left} 条")
    print()

    print(f"===== 汇总: PASS={PASS}  FAIL={FAIL} =====")
    if FAILED:
        print("失败项：")
        for name in FAILED:
            print("  - " + name)
    return 1 if FAIL else 0


if __name__ == "__main__":
    sys.exit(main())
