"""POST /api/skills —— 用技能文件新建技能。

断言三类：状态码（201/400/403）、原文回读、数据库回查 source_text 与计数。
用例结束时删除自己新建的技能，避免污染后续运行。
"""

import os
import sys
import time

_ROOT = os.path.dirname(os.path.abspath(__file__))
while not os.path.isdir(os.path.join(_ROOT, "common")):
    _ROOT = os.path.dirname(_ROOT)
sys.path.insert(0, _ROOT)

from common import db  # noqa: E402

NAME = "POST /api/skills · 用技能文件新建"

TEMPLATE = """名称：{name}
适用条件：方程,去括号
资料：数学
工具：knowledge_search
启用：是

学生正在自己做数学作业。先看他卡在哪一步，只指出下一步该检查什么，
不要算出最终得数，也不要把整道题做完。
"""


def run(run_env, case):
    teacher = run_env.client("teacher")
    suffix = time.strftime("%m%d%H%M%S") + str(os.getpid() % 1000)
    name = f"apitest·技能·{suffix}"
    source = TEMPLATE.format(name=name)

    anon = run_env.anon.post("/api/skills", json_body={"source": source})
    case.status(anon, 401, "未登录新建技能 = 401")

    before = db.count("SELECT COUNT(*) FROM skills WHERE class_id = 1")

    created = teacher.post("/api/skills", json_body={"source": source})
    case.status(created, 201, "新建技能 = 201")
    case.json_ok(created)
    case.field(created, "name", name, "解析出的名称正确")
    case.field(created, "trigger", "方程,去括号", "解析出的适用条件正确")
    case.field(created, "subject", "数学", "解析出的资料字段正确")
    case.field(created, "enabled", True, "解析出的启用状态正确")
    case.field(created, "tools.0", "knowledge_search", "工具名通过白名单")
    skill_id = (created.json or {}).get("id")
    case.truthy(created, "id", "返回技能编号")

    # 原文回读：提交什么就存什么，便于取回再编辑。
    detail = teacher.get(f"/api/skills/{skill_id}")
    case.status(detail, 200, "回读新建的技能 = 200")
    case.field(detail, "source", source, "回读原文与提交一致")
    case.db_text(f"SELECT HEX(source_text) FROM skills WHERE id = {skill_id}", source,
                 "库中 source_text 与提交逐字一致")
    case.db_count(f"SELECT COUNT(*) FROM skills WHERE class_id = 1", before + 1,
                  "技能总数 +1")

    # 解析失败：空文件、缺分隔空行。
    empty = teacher.post("/api/skills", json_body={"source": ""})
    case.status(empty, 400, "空技能文件 = 400")
    case.field(empty, "error", "技能文件是空的", "空文件错误文案可读")

    no_gap = teacher.post("/api/skills", json_body={
        "source": "名称：缺空行\n适用条件：x\n启用：是"})
    case.status(no_gap, 400, "缺分隔空行 = 400")
    case.text_contains(no_gap, "空行", "缺空行文案提到空行")

    case.db_count("SELECT COUNT(*) FROM skills WHERE class_id = 1", before + 1,
                  "失败的创建没有留下记录")

    # 权限
    student = run_env.client("student")
    denied = student.post("/api/skills", json_body={"source": TEMPLATE.format(name=name + "学生")})
    case.status(denied, 403, "学生新建技能 = 403")
    case.db_count("SELECT COUNT(*) FROM skills WHERE class_id = 1", before + 1,
                  "学生被拒后技能数不变")

    # 清理
    removed = teacher.delete(f"/api/skills/{skill_id}")
    case.status(removed, 204, "删除自己新建的技能 = 204")
    case.empty_body(removed, "204 无响应体")
    case.db_count(f"SELECT COUNT(*) FROM skills WHERE id = {skill_id}", 0, "技能已从库中删除")


if __name__ == "__main__":
    from common.runner import standalone

    sys.exit(standalone(__file__))
