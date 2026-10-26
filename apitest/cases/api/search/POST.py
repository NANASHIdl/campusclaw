"""POST /api/search —— 混合检索与班级隔离。

断言三类：状态码、命中项可回溯、数据库回查切片正文。
"""

import os
import sys

_ROOT = os.path.dirname(os.path.abspath(__file__))
while not os.path.isdir(os.path.join(_ROOT, "common")):
    _ROOT = os.path.dirname(_ROOT)
sys.path.insert(0, _ROOT)

from common import db  # noqa: E402

NAME = "POST /api/search · 检索与班级隔离"

QUERY = "去括号"


def run(run_env, case):
    anon = run_env.anon.post("/api/search", json_body={"q": QUERY, "mode": "hybrid"})
    case.status(anon, 401, "未登录检索 = 401")
    case.field(anon, "error", "unauthorized")

    teacher = run_env.client("teacher")
    resp = teacher.post("/api/search", json_body={"q": QUERY, "mode": "hybrid"})
    case.status(resp, 200, "教师检索 = 200")
    case.json_ok(resp)
    case.truthy(resp, "hits.0.chunk_id", "命中至少一条切片")

    hits = (resp.json or {}).get("hits") or []
    first = hits[0] if hits else {}
    case.check("命中项含标题/序号/区间/摘录",
               all(first.get(k) is not None for k in
                   ("material_title", "chunk_index", "start_offset", "end_offset", "snippet")),
               f"实际 {sorted(first)}")
    case.check("命中项带融合分数", first.get("score_final") is not None)

    # 数据库回查：命中的切片真实存在，且摘录来自切片正文。
    chunk_id = first.get("chunk_id")
    if chunk_id:
        case.db_count(f"SELECT COUNT(*) FROM knowledge_chunks WHERE id = {chunk_id}", 1,
                      "命中切片在库中存在")
        stored = db.text(f"SELECT HEX(chunk_text) FROM knowledge_chunks WHERE id = {chunk_id}") or ""
        snippet = (first.get("snippet") or "")[:20]
        case.check("摘录来自切片正文", bool(snippet) and snippet in stored,
                   f"摘录 {snippet!r}")
        case.db_value(f"SELECT class_id FROM knowledge_chunks WHERE id = {chunk_id}", 1,
                      "命中切片属于本班")

    # 空查询被拒绝
    empty = teacher.post("/api/search", json_body={"q": "", "mode": "hybrid"})
    case.status(empty, 400, "空查询 = 400")

    # 班级隔离：B 班学生检索 A 班内容，返回 200 但命中为空。
    other = run_env.client("student_b")
    cross = other.post("/api/search", json_body={"q": QUERY, "mode": "hybrid"})
    case.status(cross, 200, "跨班检索 = 200")
    case.check("跨班命中为空", (cross.json or {}).get("hits") == [],
               f"实际 {len((cross.json or {}).get('hits') or [])} 条")
    case.db_at_least("SELECT COUNT(*) FROM materials WHERE class_id = 2", 1,
                     "B 班确有材料（不是没数据才为空）")


if __name__ == "__main__":
    from common.runner import standalone

    sys.exit(standalone(__file__))
