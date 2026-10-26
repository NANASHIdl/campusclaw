"""GET /api/agents —— 智能体列表与可见性差异。

断言三类：状态码、响应字段（不含 class_id）、数据库回查条数。
"""

import os
import sys

_ROOT = os.path.dirname(os.path.abspath(__file__))
while not os.path.isdir(os.path.join(_ROOT, "common")):
    _ROOT = os.path.dirname(_ROOT)
sys.path.insert(0, _ROOT)

from common import db  # noqa: E402

NAME = "GET /api/agents · 列表与可见性"


def run(run_env, case):
    anon = run_env.anon.get("/api/agents")
    case.status(anon, 401, "未登录读取智能体 = 401")

    teacher = run_env.client("teacher")
    t_resp = teacher.get("/api/agents")
    case.status(t_resp, 200, "教师读取 = 200")
    case.json_ok(t_resp)
    t_list = (t_resp.json or {}).get("agents")
    case.check("返回 agents 数组", isinstance(t_list, list), f"实际 {type(t_list).__name__}")
    case.check("教师至少可见一个智能体", bool(t_list), "列表为空")
    if t_list:
        case.check("智能体项不含 class_id", "class_id" not in t_list[0],
                   f"实际字段 {sorted(t_list[0])}")
        case.check("智能体项含绑定", "skill_ids" in t_list[0] and "material_ids" in t_list[0],
                   f"实际字段 {sorted(t_list[0])}")

    student = run_env.client("student")
    s_resp = student.get("/api/agents")
    case.status(s_resp, 200, "学生读取 = 200")
    s_list = (s_resp.json or {}).get("agents") or []
    case.check("学生只看到已启用的",
               all(a.get("enabled") is True for a in s_list),
               f"实际 {[a.get('enabled') for a in s_list]}")

    # 数据库回查：两个角色拿到的条数分别等于「本班全部」与「本班已启用」。
    total = db.count("SELECT COUNT(*) FROM tutor_agents WHERE class_id = 1")
    enabled = db.count("SELECT COUNT(*) FROM tutor_agents WHERE class_id = 1 AND enabled = 1")
    case.check("教师列表条数 = 本班全部", len(t_list or []) == total, f"{len(t_list or [])} vs {total}")
    case.check("学生列表条数 = 本班已启用", len(s_list) == enabled, f"{len(s_list)} vs {enabled}")

    ghost = student.get("/api/agents/999999")
    case.status(ghost, 404, "不存在的智能体 = 404")


if __name__ == "__main__":
    from common.runner import standalone

    sys.exit(standalone(__file__))
