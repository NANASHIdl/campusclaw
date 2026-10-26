"""POST /api/conversations/{id}/messages —— 发送一条消息（SSE 流式）。

路径含动态段，目录只能表达静态部分，因此用 PATH 常量写全路由。

断言三类：状态码与响应头、事件流内容、数据库回查（消息落库、标题改写）。
"""

import os
import re
import sys

_ROOT = os.path.dirname(os.path.abspath(__file__))
while not os.path.isdir(os.path.join(_ROOT, "common")):
    _ROOT = os.path.dirname(_ROOT)
sys.path.insert(0, _ROOT)

from common import db  # noqa: E402

NAME = "POST /api/conversations/{id}/messages · 流式提问"
PATH = "/api/conversations/{id}/messages"
METHOD = "POST"

QUESTION = "去括号的时候系数要不要乘到括号里的每一项"


def run(run_env, case):
    agent_id = db.one("SELECT id FROM tutor_agents WHERE class_id = 1 AND enabled = 1 "
                      "ORDER BY id LIMIT 1")
    case.check("本班存在已启用的智能体", agent_id is not None, "找不到可用智能体")
    if not agent_id:
        return

    student = run_env.client("student")
    created = student.post("/api/conversations", json_body={"agent_id": int(agent_id)})
    case.status(created, 201, "新建会话 = 201")
    conv_id = (created.json or {}).get("id")
    if not conv_id:
        case.check("拿到会话编号", False, f"响应 {created.text[:120]}")
        return

    # 未认证
    anon_status, _, _ = run_env.anon.sse(f"/api/conversations/{conv_id}/messages",
                                        {"content": QUESTION})
    case.check("未登录提问 = 401", anon_status == 401, f"实际 {anon_status}")

    status, headers, events = student.sse(f"/api/conversations/{conv_id}/messages",
                                          {"content": QUESTION})
    case.check("提问返回 200", status == 200, f"实际 {status}")
    ctype = headers.get("Content-Type", "")
    case.check("Content-Type 为 text/event-stream", "text/event-stream" in ctype, f"实际 {ctype}")
    case.check("Cache-Control 为 no-cache",
               "no-cache" in headers.get("Cache-Control", ""), f"实际 {headers.get('Cache-Control')}")

    order = [e["event"] for e in events]
    case.check("第一个事件是 meta", bool(order) and order[0] == "meta", f"实际 {order[:3]}")
    case.check("至少一条 delta", order.count("delta") >= 1, f"delta {order.count('delta')} 条")
    case.check("最后一个事件是 done", bool(order) and order[-1] == "done", f"实际 {order[-3:]}")

    meta = next((e["data"] for e in events if e["event"] == "meta"), {}) or {}
    case.check("meta 含 status 与引用列表",
               "status" in meta and isinstance(meta.get("citations"), list),
               f"实际 {sorted(meta)}")

    answer = "".join((e["data"] or {}).get("text", "") for e in events if e["event"] == "delta")
    case.check("流式正文非空", bool(answer.strip()), f"长度 {len(answer)}")

    done = next((e["data"] for e in events if e["event"] == "done"), {}) or {}
    message_id = done.get("message_id")
    case.check("done 返回助手消息编号", isinstance(message_id, int) and message_id > 0,
               f"实际 {done}")

    # 数据库回查：拼接结果等于落库全文；一问一答两条消息。
    if isinstance(message_id, int):
        stored = db.text(f"SELECT HEX(content) FROM messages WHERE id = {message_id}")
        case.check("delta 拼接等于落库全文", stored == answer,
                   f"库 {len(stored or '')} 字，流 {len(answer)} 字")
        case.db_value(f"SELECT role FROM messages WHERE id = {message_id}", "assistant",
                      "该条消息角色为 assistant")
        case.db_value(f"SELECT conversation_id FROM messages WHERE id = {message_id}", conv_id,
                      "该条消息属于本会话")
    case.db_count(f"SELECT COUNT(*) FROM messages WHERE conversation_id = {conv_id} "
                  f"AND role = 'user'", 1, "提问已落库")
    case.db_count(f"SELECT COUNT(*) FROM messages WHERE conversation_id = {conv_id} "
                  f"AND role = 'assistant'", 1, "回答已落库")

    # 首轮提问改写标题（前 24 字）。
    title = db.text(f"SELECT HEX(title) FROM conversations WHERE id = {conv_id}")
    case.check("首轮提问后标题改为前 24 字", title == QUESTION[:24],
               f"实际 {title!r}")

    # 越界与不存在的会话
    too_short = student.post(f"/api/conversations/{conv_id}/messages", json_body={"content": "啊"})
    case.status(too_short, 400, "content 仅 1 字符 = 400")
    too_long = student.post(f"/api/conversations/{conv_id}/messages",
                            json_body={"content": "字" * 501})
    case.status(too_long, 400, "content 超 500 字符 = 400")
    ghost_status, ghost_headers, _ = student.sse("/api/conversations/999999/messages",
                                                 {"content": QUESTION})
    case.check("不存在的会话 = 404", ghost_status == 404, f"实际 {ghost_status}")
    case.check("不存在的会话不建立事件流",
               "text/event-stream" not in (ghost_headers.get("Content-Type") or ""),
               f"实际 {ghost_headers.get('Content-Type')}")

    # 引用编号与引用列表对齐（有依据时）
    cites = meta.get("citations") or []
    if cites:
        numbers = [int(n) for n in re.findall(r"\[(\d+)\]", answer)]
        case.check("正文中的 [n] 不超出引用条数", all(1 <= n <= len(cites) for n in numbers),
                   f"编号 {numbers}，引用 {len(cites)} 条")


if __name__ == "__main__":
    from common.runner import standalone

    sys.exit(standalone(__file__))
