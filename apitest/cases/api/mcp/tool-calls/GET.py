"""GET /api/mcp/tool-calls —— 工具调用记录。

断言三类：状态码（403/200）、记录字段齐全、数据库条数与状态一致。
"""

import os
import sys

_ROOT = os.path.dirname(os.path.abspath(__file__))
while not os.path.isdir(os.path.join(_ROOT, "common")):
    _ROOT = os.path.dirname(_ROOT)
sys.path.insert(0, _ROOT)

from common import db, env  # noqa: E402

NAME = "GET /api/mcp/tool-calls · 调用记录"


def run(run_env, case):
    anon = run_env.anon.get("/api/mcp/tool-calls")
    case.status(anon, 401, "未登录读取记录 = 401")

    student = run_env.client("student").get("/api/mcp/tool-calls")
    case.status(student, 403, "学生读取记录 = 403")

    teacher = run_env.client("teacher")
    resp = teacher.get("/api/mcp/tool-calls")
    case.status(resp, 200, "教师读取记录 = 200")
    case.json_ok(resp)

    calls = (resp.json or {}).get("calls")
    case.check("返回 calls 数组", isinstance(calls, list), f"实际 {type(calls).__name__}")
    case.check("默认不超过 50 条", len(calls or []) <= 50, f"实际 {len(calls or [])}")

    if calls:
        first = calls[0]
        case.check("记录含五个问题的答案",
                   all(k in first for k in
                       ("tool_ref", "server_name", "status", "user_id",
                        "conversation_id", "round_no", "created_at")),
                   f"实际 {sorted(first)}")
        case.check("状态取值合法",
                   all(c.get("status") in ("ok", "denied", "error") for c in calls),
                   f"实际 {sorted({c.get('status') for c in calls})}")

    if env.ENV.get("MCP_AUTH_TOKEN"):
        case.check("记录响应不含令牌", env.ENV["MCP_AUTH_TOKEN"] not in resp.text)

    total = db.count("SELECT COUNT(*) FROM mcp_tool_calls WHERE class_id = 1")
    case.check("接口条数 = 库中条数（不超过 50）", len(calls or []) == min(total, 50),
               f"接口 {len(calls or [])}，库 {total}")


if __name__ == "__main__":
    from common.runner import standalone

    sys.exit(standalone(__file__))
