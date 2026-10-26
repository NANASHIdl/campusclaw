"""PUT /api/agents/{id} —— 提示词的新建与修改（含清空）。

提示词是智能体的字段，没有独立的 /api/prompt 端点（见 proposal 的 Non-goals），
所以「提示词 GET/PUT」在这里落地：GET 读智能体拿提示词，PUT 改智能体写提示词。

断言三类：状态码、响应字段、数据库回查（提示词原文与清空后的长度）。
"""

import os
import sys
import time

_ROOT = os.path.dirname(os.path.abspath(__file__))
while not os.path.isdir(os.path.join(_ROOT, "common")):
    _ROOT = os.path.dirname(_ROOT)
sys.path.insert(0, _ROOT)

from common import db  # noqa: E402

NAME = "PUT /api/agents/{id} · 提示词读写"


def run(run_env, case):
    teacher = run_env.client("teacher")
    suffix = time.strftime("%m%d%H%M%S") + str(os.getpid() % 1000)
    name = f"apitest·提示词·{suffix}"
    prompt = "你是教研助手，回答前先看学生已经写到哪一步，只指出下一步该检查什么。"

    created = teacher.post("/api/agents", json_body={
        "name": name, "prompt": "", "enabled": True, "skill_ids": [], "material_ids": []})
    case.status(created, 201, "新建智能体（提示词为空）= 201")
    agent_id = (created.json or {}).get("id")
    case.truthy(created, "id", "返回智能体编号")
    case.db_count(f"SELECT COUNT(*) FROM tutor_agents WHERE id = {agent_id}", 1,
                  "智能体已落库")

    # GET：读取智能体即读取提示词。
    got = teacher.get(f"/api/agents/{agent_id}")
    case.status(got, 200, "读取智能体 = 200")
    case.field(got, "prompt", "", "新建时提示词为空")

    # PUT：写入提示词。
    updated = teacher.put(f"/api/agents/{agent_id}", json_body={
        "name": name, "prompt": prompt, "enabled": True, "skill_ids": [], "material_ids": []})
    case.status(updated, 200, "修改提示词 = 200")
    case.field(updated, "prompt", prompt, "响应回显新提示词")
    case.db_text(f"SELECT HEX(prompt) FROM tutor_agents WHERE id = {agent_id}", prompt,
                 "库中提示词与提交逐字一致")

    # PUT：清空提示词（规约要求允许为空，回退默认角色）。
    cleared = teacher.put(f"/api/agents/{agent_id}", json_body={
        "name": name, "prompt": "", "enabled": True, "skill_ids": [], "material_ids": []})
    case.status(cleared, 200, "清空提示词 = 200")
    case.db_count(f"SELECT CHAR_LENGTH(COALESCE(prompt, '')) FROM tutor_agents WHERE id = {agent_id}",
                  0, "库中提示词已清空")

    # 超长被拒
    too_long = teacher.put(f"/api/agents/{agent_id}", json_body={
        "name": name, "prompt": "字" * 2001, "enabled": True, "skill_ids": [], "material_ids": []})
    case.status(too_long, 400, "提示词超 2000 字 = 400")
    case.db_count(f"SELECT CHAR_LENGTH(COALESCE(prompt, '')) FROM tutor_agents WHERE id = {agent_id}",
                  0, "越界请求没有改库")

    # 权限：学生不能改
    student = run_env.client("student")
    denied = student.put(f"/api/agents/{agent_id}", json_body={
        "name": name, "prompt": "学生改的提示词", "enabled": True,
        "skill_ids": [], "material_ids": []})
    case.status(denied, 403, "学生修改提示词 = 403")
    case.db_count(f"SELECT CHAR_LENGTH(COALESCE(prompt, '')) FROM tutor_agents WHERE id = {agent_id}",
                  0, "学生被拒后提示词未变")


if __name__ == "__main__":
    from common.runner import standalone

    sys.exit(standalone(__file__))
