"""POST /api/login —— 登录换取 JWT 访问令牌。

断言三类：状态码、响应内容（含令牌载荷）、数据库回查（角色来自 users 表）。
"""

import base64
import json
import os
import sys

_ROOT = os.path.dirname(os.path.abspath(__file__))
while not os.path.isdir(os.path.join(_ROOT, "common")):
    _ROOT = os.path.dirname(_ROOT)
sys.path.insert(0, _ROOT)

from common import env  # noqa: E402

NAME = "POST /api/login · 登录返回 JWT 令牌"


def jwt_payload(token):
    part = token.split(".")[1]
    part += "=" * (-len(part) % 4)
    return json.loads(base64.urlsafe_b64decode(part))


def run(run_env, case):
    anon = run_env.anon

    resp = anon.post("/api/login",
                     json_body={"username": env.TEACHER[0], "password": env.TEACHER[1]})
    case.status(resp, 200, "教师登录 = 200")
    case.json_ok(resp)
    case.keys(resp, "token", "user.id", "user.username", "user.role", "user.class_id")
    case.field(resp, "user.username", env.TEACHER[0], "用户名回显正确")
    case.field(resp, "user.role", "teacher", "角色为 teacher")

    case.check("不以 Set-Cookie 下发凭据", resp.header("Set-Cookie") is None,
               f"实际 {resp.header('Set-Cookie')}")
    lowered = resp.text.lower()
    case.check("响应不含口令或哈希",
               "password" not in lowered and "pbkdf2" not in lowered and "salt" not in lowered,
               f"正文 {resp.text[:120]!r}")

    token = (resp.json or {}).get("token") or ""
    case.check("令牌是三段式 JWT", token.count(".") == 2, f"实际 {token[:32]}…")
    payload = jwt_payload(token) if token.count(".") == 2 else {}
    case.check("载荷仅含 sub/jti/iat/exp",
               set(payload.keys()) == {"sub", "jti", "iat", "exp"}, f"实际 {sorted(payload.keys())}")
    case.check("载荷不含角色与班级",
               "role" not in payload and "class_id" not in payload, f"实际 {sorted(payload.keys())}")

    # 数据库回查：角色与班级只存在库里，令牌里没有。
    case.db_value("SELECT role FROM users WHERE username = 'teacher_a'", "teacher",
                  "库中角色为 teacher")
    case.db_count(f"SELECT COUNT(*) FROM revoked_tokens WHERE jti = '{payload.get('jti', '')}'", 0,
                  "登录本身不写吊销表")

    # 失败口径统一：账号不存在与口令错误对外必须一模一样。
    bad = anon.post("/api/login", json_body={"username": env.TEACHER[0], "password": "错误口令"})
    case.status(bad, 401, "口令错误 = 401")
    case.field(bad, "error", "invalid_credentials", "失败正文为 invalid_credentials")

    ghost = anon.post("/api/login", json_body={"username": "no_such_user", "password": "x"})
    case.status(ghost, 401, "账号不存在 = 401")
    case.check("账号不存在与口令错误响应一致", ghost.body == bad.body,
               f"{ghost.body!r} vs {bad.body!r}")


if __name__ == "__main__":
    from common.runner import standalone

    sys.exit(standalone(__file__))
