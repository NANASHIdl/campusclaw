"""POST /api/mcp/servers/{id}/connect —— 连接、发现与断开。

路径含动态段，用 PATH 常量写全路由。

断言三类：状态码、发现的工具清单、数据库里的工具条数。
"""

import os
import sys
import time

_ROOT = os.path.dirname(os.path.abspath(__file__))
while not os.path.isdir(os.path.join(_ROOT, "common")):
    _ROOT = os.path.dirname(_ROOT)
sys.path.insert(0, _ROOT)

from common import db, env  # noqa: E402

NAME = "POST /api/mcp/servers/{id}/connect · 连接与发现"
PATH = "/api/mcp/servers/{id}/connect"
METHOD = "POST"


def run(run_env, case):
    token = env.ENV.get("MCP_AUTH_TOKEN", "")
    if not token:
        case.check("已配置 MCP_AUTH_TOKEN", False, "缺少令牌，无法连接")
        return

    teacher = run_env.client("teacher")
    suffix = time.strftime("%m%d%H%M%S") + str(os.getpid() % 1000)
    name = f"apitest-conn-{suffix}"

    created = teacher.post("/api/mcp/servers", json_body={
        "name": name, "base_url": "http://mcp-demo:8090", "auth_token": token})
    case.status(created, 201, "先登记一台服务器 = 201")
    server_id = (created.json or {}).get("id")
    if not server_id:
        case.check("拿到服务器编号", False, f"响应 {created.text[:120]}")
        return

    student = run_env.client("student").post(f"/api/mcp/servers/{server_id}/connect", json_body={})
    case.status(student, 403, "学生连接 = 403")

    conn = teacher.post(f"/api/mcp/servers/{server_id}/connect", json_body={})
    case.status(conn, 200, "连接并发现 = 200")
    case.json_ok(conn)
    case.field(conn, "connected", True, "连接后 connected 为 true")

    tools = (conn.json or {}).get("tools") or []
    names = [t.get("name") for t in tools]
    case.check("发现至少两个工具", len(tools) >= 2, f"实际 {names}")
    case.check("含 formula_hint", "formula_hint" in names, f"实际 {names}")
    case.check("工具项含说明与输入结构",
               all(t.get("description") and t.get("input_schema") for t in tools))

    db_count = db.count(f"SELECT COUNT(*) FROM mcp_tools WHERE server_id = {server_id}")
    case.check("工具清单已落库", db_count == len(tools), f"库 {db_count}，接口 {len(tools)}")
    case.db_value(f"SELECT connected FROM mcp_servers WHERE id = {server_id}", 1, "库中已连接")

    again = teacher.post(f"/api/mcp/servers/{server_id}/connect", json_body={})
    again_tools = (again.json or {}).get("tools") or []
    case.check("连续连接两次清单不翻倍", len(again_tools) == len(tools),
               f"{len(tools)} → {len(again_tools)}")

    listing = teacher.get(f"/api/mcp/servers/{server_id}/tools")
    case.status(listing, 200, "查看工具清单 = 200")

    off = teacher.post(f"/api/mcp/servers/{server_id}/disconnect", json_body={})
    case.status(off, 200, "断开 = 200")
    case.field(off, "connected", False, "断开后 connected 为 false")
    case.db_count(f"SELECT COUNT(*) FROM mcp_tools WHERE server_id = {server_id}", 0,
                  "断开后工具清单清空")
    case.db_value(f"SELECT connected FROM mcp_servers WHERE id = {server_id}", 0, "库中已断开")


if __name__ == "__main__":
    from common.runner import standalone

    sys.exit(standalone(__file__))
