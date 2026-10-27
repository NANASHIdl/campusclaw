"""GET /api/mcp/servers —— 工具服务列表与令牌边界。

断言三类：状态码（401/403/200）、响应不含令牌、数据库条数一致。
"""

import json
import os
import sys

_ROOT = os.path.dirname(os.path.abspath(__file__))
while not os.path.isdir(os.path.join(_ROOT, "common")):
    _ROOT = os.path.dirname(_ROOT)
sys.path.insert(0, _ROOT)

from common import db, env  # noqa: E402

NAME = "GET /api/mcp/servers · 列表与令牌边界"


def run(run_env, case):
    token = env.ENV.get("MCP_AUTH_TOKEN", "")

    anon = run_env.anon.get("/api/mcp/servers")
    case.status(anon, 401, "未登录读取 = 401")

    student = run_env.client("student").get("/api/mcp/servers")
    case.status(student, 403, "学生读取 = 403")

    teacher = run_env.client("teacher")
    resp = teacher.get("/api/mcp/servers")
    case.status(resp, 200, "教师读取 = 200")
    case.json_ok(resp)

    servers = (resp.json or {}).get("servers")
    case.check("返回 servers 数组", isinstance(servers, list), f"实际 {type(servers).__name__}")
    if servers:
        item = servers[0]
        case.check("列表项含 has_auth 与 tool_count",
                   "has_auth" in item and "tool_count" in item, f"实际 {sorted(item)}")
        case.check("列表项不含 auth_token 字段", "auth_token" not in item, f"实际 {sorted(item)}")

    if token:
        case.check("整份响应不含令牌字符串", token not in resp.text, "响应里出现了令牌")

    total = db.count("SELECT COUNT(*) FROM mcp_servers WHERE class_id = 1")
    case.check("列表条数 = 本班服务器数", len(servers or []) == total,
               f"{len(servers or [])} vs {total}")


if __name__ == "__main__":
    from common.runner import standalone

    sys.exit(standalone(__file__))
