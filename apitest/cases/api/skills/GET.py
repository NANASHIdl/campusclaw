"""GET /api/skills —— 技能列表与文件原文。

断言三类：状态码、原文回读、数据库回查条数与 seed_key。
"""

import os
import sys

_ROOT = os.path.dirname(os.path.abspath(__file__))
while not os.path.isdir(os.path.join(_ROOT, "common")):
    _ROOT = os.path.dirname(_ROOT)
sys.path.insert(0, _ROOT)

from common import db  # noqa: E402

NAME = "GET /api/skills · 列表与文件原文"


def run(run_env, case):
    anon = run_env.anon.get("/api/skills")
    case.status(anon, 401, "未登录读取技能 = 401")

    teacher = run_env.client("teacher")
    resp = teacher.get("/api/skills")
    case.status(resp, 200, "教师读取技能列表 = 200")
    case.json_ok(resp)
    skills = (resp.json or {}).get("skills")
    case.check("返回 skills 数组", isinstance(skills, list), f"实际 {type(skills).__name__}")
    case.check("预置技能存在", bool(skills), "列表为空")
    if skills:
        first = skills[0]
        case.keys(resp, "skills.0.id", "skills.0.name", "skills.0.source")
        case.check("列表项含结构化字段",
                   all(k in first for k in ("trigger", "subject", "tools", "enabled")),
                   f"实际字段 {sorted(first)}")

    # 单条读取：原文要能取回再编辑。
    detail_id = skills[0]["id"] if skills else None
    if detail_id:
        detail = teacher.get(f"/api/skills/{detail_id}")
        case.status(detail, 200, "读取单条技能 = 200")
        case.field(detail, "id", detail_id)
        source = (detail.json or {}).get("source") or ""
        case.check("返回技能文件原文", bool(source.strip()), f"原文长度 {len(source)}")

        # 数据库回查：原文与库中 source_text 逐字一致。
        stored = db.text(f"SELECT HEX(source_text) FROM skills WHERE id = {detail_id}")
        case.check("接口原文与库中原文一致", stored == source,
                   f"库 {len(stored or '')} 字，接口 {len(source)} 字")

    student = run_env.client("student")
    s_resp = student.get("/api/skills")
    case.status(s_resp, 200, "学生读取技能列表 = 200")

    total = db.count("SELECT COUNT(*) FROM skills WHERE class_id = 1")
    case.check("列表条数 = 本班技能数", len(skills or []) == total,
               f"{len(skills or [])} vs {total}")
    case.db_value("SELECT COUNT(*) FROM skills WHERE class_id = 1 AND seed_key = 'math-tutor'", 1,
                  "预置技能以 seed_key 标识且只有一份")

    ghost = teacher.get("/api/skills/999999")
    case.status(ghost, 404, "不存在的技能 = 404")


if __name__ == "__main__":
    from common.runner import standalone

    sys.exit(standalone(__file__))
