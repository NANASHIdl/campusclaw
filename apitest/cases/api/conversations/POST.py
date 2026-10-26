"""POST /api/conversations —— 按智能体新建会话。

断言三类：状态码（201/400/404）、会话字段、数据库回查归属与锁定。
"""

import os
import sys

_ROOT = os.path.dirname(os.path.abspath(__file__))
while not os.path.isdir(os.path.join(_ROOT, "common")):
    _ROOT = os.path.dirname(_ROOT)
sys.path.insert(0, _ROOT)

from common import db  # noqa: E402

NAME = "POST /api/conversations · 新建会话"


def run(run_env, case):
    agent_id = db.one("SELECT id FROM tutor_agents WHERE class_id = 1 AND enabled = 1 "
                      "ORDER BY id LIMIT 1")
    case.check("本班存在已启用的智能体", agent_id is not None, "找不到可用智能体，后续断言无意义")
    if not agent_id:
        return

    student = run_env.client("student")
    created = student.post("/api/conversations", json_body={"agent_id": int(agent_id)})
    case.status(created, 201, "新建会话 = 201")
    case.json_ok(created)
    case.field(created, "agent_id", int(agent_id), "会话记录了选定的智能体")
    case.field(created, "title", "新的提问", "新建会话标题为占位串")
    conv_id = (created.json or {}).get("id")
    case.truthy(created, "id", "返回会话编号")

    # 数据库回查：归属与班级来自会话，不是客户端自报。
    case.db_value(f"SELECT agent_id FROM conversations WHERE id = {conv_id}", agent_id,
                  "库中 agent_id 与会话一致")
    case.db_value(f"SELECT user_id FROM conversations WHERE id = {conv_id}", 2,
                  "库中 user_id 为当前学生")
    case.db_value(f"SELECT class_id FROM conversations WHERE id = {conv_id}", 1,
                  "库中 class_id 为该生所在班级")

    # 参数与边界
    no_agent = student.post("/api/conversations", json_body={})
    case.status(no_agent, 400, "未指定智能体 = 400")

    zero = student.post("/api/conversations", json_body={"agent_id": 0})
    case.status(zero, 400, "agent_id 小于 1 = 400")

    ghost = student.post("/api/conversations", json_body={"agent_id": 999999})
    case.status(ghost, 404, "不存在的智能体 = 404")

    # 隔离：他人会话与不存在的会话返回完全一致的 404。
    teacher = run_env.client("teacher")
    others = teacher.get(f"/api/conversations/{conv_id}")
    missing = teacher.get("/api/conversations/999999")
    case.status(others, 404, "读取他人会话 = 404")
    case.check("他人会话与不存在会话响应一致", others.body == missing.body,
               f"{others.body!r} vs {missing.body!r}")

    anon = run_env.anon.post("/api/conversations", json_body={"agent_id": int(agent_id)})
    case.status(anon, 401, "未登录新建会话 = 401")


if __name__ == "__main__":
    from common.runner import standalone

    sys.exit(standalone(__file__))
