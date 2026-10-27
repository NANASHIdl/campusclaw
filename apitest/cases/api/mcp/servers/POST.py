"""POST /api/mcp/servers —— 登记工具服务。

断言三类：状态码（201/400/403）、响应只回 has_auth、数据库落库。
"""

import os
import sys
import time

_ROOT = os.path.dirname(os.path.abspath(__file__))
while not os.path.isdir(os.path.join(_ROOT, "common")):
    _ROOT = os.path.dirname(_ROOT)
sys.path.insert(0, _ROOT)

from common import db, env  # noqa: E402

NAME = "POST /api/mcp/servers · 登记工具服务"


def run(run_env, case):
    token = env.ENV.get("MCP_AUTH_TOKEN", "")
    case.check("已配置 MCP_AUTH_TOKEN", bool(token), "缺少令牌，登记会失败")

    teacher = run_env.client("teacher")
    suffix = time.strftime("%m%d%H%M%S") + str(os.getpid() % 1000)
    name = f"apitest-{suffix}"

    created = teacher.post("/api/mcp/servers", json_body={
        "name": name, "base_url": "http://mcp-demo:8090", "auth_token": token})
    case.status(created, 201, "登记 = 201")
    case.json_ok(created)
    case.field(created, "connected", False, "登记后为未连接")
    case.field(created, "tool_count", 0, "登记后工具数为 0")
    case.field(created, "has_auth", True, "只回「是否已配令牌」")
    case.check("响应不含令牌", token not in created.text, "响应里出现了令牌")

    server_id = (created.json or {}).get("id")
    case.db_count(f"SELECT COUNT(*) FROM mcp_servers WHERE id = {server_id}", 1, "服务器已落库")
    case.db_value(f"SELECT class_id FROM mcp_servers WHERE id = {server_id}", 1, "归属本班")

    dup = teacher.post("/api/mcp/servers", json_body={
        "name": name, "base_url": "http://mcp-demo:8090", "auth_token": token})
    case.status(dup, 400, "同名登记 = 400")
    case.db_count(f"SELECT COUNT(*) FROM mcp_servers WHERE class_id = 1 AND name = '{name}'", 1,
                  "同名被拒后仍只有一条")

    bad = teacher.post("/api/mcp/servers", json_body={
        "name": name + "-bad", "base_url": "mcp-demo:8090", "auth_token": token})
    case.status(bad, 400, "地址不是 http(s) = 400")

    student = run_env.client("student").post("/api/mcp/servers", json_body={
        "name": name + "-stu", "base_url": "http://mcp-demo:8090", "auth_token": token})
    case.status(student, 403, "学生登记 = 403")
    case.db_count(f"SELECT COUNT(*) FROM mcp_servers WHERE name = '{name}-stu'", 0,
                  "学生被拒后没有落库")


if __name__ == "__main__":
    from common.runner import standalone

    sys.exit(standalone(__file__))
