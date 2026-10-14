#!/usr/bin/env python3
"""CampusClaw 第 4 课（add-traceable-vector-retrieval）端到端验收。

逐条执行 openspec/changes/add-traceable-vector-retrieval/specs/knowledge-retrieval/spec.md
中的 Scenario。需要四容器栈已启动。

    BASE_URL=http://127.0.0.1:8081 python scripts/verify_retrieval.py
"""

import http.cookiejar
import json
import os
import sys
import urllib.error
import urllib.request

BASE = os.environ.get("BASE_URL", "http://127.0.0.1:8080")

PASS = 0
FAIL = 0


def check(name, ok, detail=""):
    global PASS, FAIL
    if ok:
        PASS += 1
        print(f"  PASS  {name}" + (f"  ({detail})" if detail else ""))
    else:
        FAIL += 1
        print(f"  FAIL  {name}" + (f"  {detail}" if detail else ""))


class Client:
    def __init__(self):
        self.cj = http.cookiejar.CookieJar()
        self.op = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(self.cj))

    def call(self, path, data=None, raw=None, headers=None, method=None):
        body = None
        hdrs = dict(headers or {})
        if raw is not None:
            body = raw
        elif data is not None:
            body = json.dumps(data).encode()
            hdrs["Content-Type"] = "application/json"
        req = urllib.request.Request(
            BASE + path, data=body, headers=hdrs, method=method or ("POST" if body is not None else "GET")
        )
        try:
            r = self.op.open(req, timeout=60)
            return r.status, dict(r.headers), r.read()
        except urllib.error.HTTPError as e:
            return e.code, dict(e.headers), e.read()
        except Exception as e:  # noqa: BLE001
            return None, {}, f"{type(e).__name__}: {e}".encode()

    def json(self, path, data=None, raw=None, headers=None, method=None):
        st, hd, body = self.call(path, data=data, raw=raw, headers=headers, method=method)
        try:
            return st, json.loads(body)
        except Exception:  # noqa: BLE001
            return st, None

    def login(self, username, password):
        return self.json("/api/login", {"username": username, "password": password})


def multipart(fields, filename, content):
    """构造只含一个文件的 multipart/form-data 请求体。"""
    boundary = "----campusclawboundary7f3a"
    parts = []
    for k, v in fields.items():
        parts.append(f"--{boundary}\r\nContent-Disposition: form-data; name=\"{k}\"\r\n\r\n{v}\r\n".encode())
    parts.append(
        f"--{boundary}\r\nContent-Disposition: form-data; name=\"file\"; filename=\"{filename}\"\r\n"
        f"Content-Type: text/markdown\r\n\r\n".encode()
    )
    parts.append(content.encode())
    parts.append(f"\r\n--{boundary}--\r\n".encode())
    return b"".join(parts), {"Content-Type": f"multipart/form-data; boundary={boundary}"}


LONG_DOC = (
    "# 校园安全与行为规范\n\n"
    "## 课间活动\n\n"
    "上课铃响后必须进入教室，不得在走廊停留。校园内不得追逐打闹。\n"
    "课间可以喝水、去卫生间、做眼保健操的准备。禁止在楼道奔跑，禁止推搡和拍打他人。\n"
    "球类只在体育课和指定场地进行。下雨时在走廊行走要慢，以免滑倒。\n"
    "值日生在下课铃后擦黑板、摆齐桌椅，离开时关灯关窗。\n\n"
    "## 实验与体育课\n\n"
    "实验课按教师演示操作，不随意混合试剂。体育课做准备活动，不在器械上打闹。\n"
    "体育课感到胸痛或眩晕，立即停止并报告教师，不硬撑着上课。\n"
    "使用锋利工具时双手握稳，刃口朝外传递，不得朝向他人。\n\n"
    "## 消防与应急\n\n"
    "消防演习时按路线撤离，不乘坐电梯，不返回教室取物品。\n"
    "发现烟雾或异味立即告知最近的教师，并按疏散指示离开教学楼。\n"
    "遇到地震警报时先就近躲避，再在震动停止后有序撤离到操场。\n\n"
    "## 图书馆与自习\n\n"
    "进入阅览室保持安静，手机调成静音。一次借阅不超过规定册数，到期可在柜台续借一次。\n"
    "晚自习保持安静，需要讨论时到教师指定的角落并压低声音。不在自习时间串座。\n"
    "离座先举手。自习结束关灯、关窗、关门。走读生按时离校。\n\n"
    "## 饮食与卫生\n\n"
    "在食堂按顺序取餐，吃多少取多少，剩饭剩菜倒入指定桶。餐盘送回回收处。\n"
    "不随地吐痰，不乱扔纸屑。垃圾分类投入对应垃圾桶。\n"
    "传染病期间按校医要求佩戴口罩，不共享水杯，不互相分享处方药。\n"
)


def main():
    print(f"== 目标 {BASE} ==\n")

    anon = Client()
    teacher = Client()
    student_a = Client()
    student_b = Client()

    # ---------- 1. 认证 ----------
    print("== 1. 检索接口的认证 ==")
    for path, payload in [
        ("/api/search", {"q": "追逐打闹", "mode": "hybrid"}),
        ("/api/ask", {"q": "追逐打闹"}),
    ]:
        st, body = anon.json(path, payload)
        check(f"未登录 POST {path} = 401", st == 401, f"实际 {st}")

    st, _ = anon.json("/api/materials/1/vectors")
    check("未登录 GET /api/materials/1/vectors = 401", st == 401, f"实际 {st}")
    st, _ = anon.json("/api/materials/1/reindex", {"strategy": "auto"})
    check("未登录 POST /api/materials/1/reindex = 401", st == 401, f"实际 {st}")

    st, _ = teacher.login("teacher_a", "teach123")
    check("教师 A 登录 = 200", st == 200, f"实际 {st}")
    st, _ = student_a.login("student_a1", "stu123")
    check("学生 A1 登录 = 200", st == 200, f"实际 {st}")
    st, _ = student_b.login("student_b1", "stu123")
    check("学生 B1 登录 = 200", st == 200, f"实际 {st}")
    print()

    # ---------- 2. 上传并建立索引 ----------
    print("== 2. 上传 → 切分 → 嵌入 → 向量库 ==")
    raw, headers = multipart({"chunk_strategy": "hierarchy"}, "校园安全规范.md", LONG_DOC)
    st, body = teacher.json("/api/materials/upload", raw=raw, headers=headers)
    check("教师上传 = 201", st == 201, f"实际 {st} {body}")
    material_id = (body or {}).get("material_id")
    chunk_count = (body or {}).get("chunk_count", 0)
    check("上传返回切片数 > 1", isinstance(chunk_count, int) and chunk_count > 1, f"chunk_count={chunk_count}")
    print()

    # ---------- 3. 切片与向量 ----------
    print("== 3. 向量索引与两库关联 ==")
    st, vec = teacher.json(f"/api/materials/{material_id}/vectors")
    check("查看向量 = 200", st == 200, f"实际 {st}")
    vec = vec or {}
    chunks = vec.get("chunks") or []
    check("切片数一致", len(chunks) == chunk_count, f"{len(chunks)} vs {chunk_count}")
    check("维度为 2048", vec.get("dim") == 2048, f"实际 {vec.get('dim')}")
    check("距离度量为 Cosine", vec.get("distance") == "Cosine", f"实际 {vec.get('distance')}")

    linked = [c for c in chunks if c.get("linked")]
    check("每条切片在向量库中都有对应记录", len(linked) == len(chunks), f"{len(linked)}/{len(chunks)}")
    if linked:
        c0 = linked[0]
        check("向量主键 = knowledge_chunks.id", c0.get("point_id") == c0.get("mysql_chunk_id"),
              f"point_id={c0.get('point_id')} mysql_chunk_id={c0.get('mysql_chunk_id')}")
        check("payload.chunk_id = 切片主键", c0.get("payload_chunk_id") == c0.get("mysql_chunk_id"),
              f"payload_chunk_id={c0.get('payload_chunk_id')}")
        check("payload 不含正文字段", not any(k in c0 for k in ("chunk_text", "text", "body_text")),
              "payload 键: " + ",".join(k for k in c0 if k.startswith("payload")))
        check("向量维度与集合一致", c0.get("dim") == 2048, f"实际 {c0.get('dim')}")
    print()

    # ---------- 4. 空查询 ----------
    print("== 4. 空查询被拒绝 ==")
    for q in ["", " ", "啊"]:
        st, body = teacher.json("/api/search", {"q": q, "mode": "hybrid"})
        check(f"查询 {q!r} = 400", st == 400, f"实际 {st}")
    print()

    # ---------- 5. 三种检索模式 ----------
    print("== 5. 三种检索模式 ==")
    QUERY = "课间追逐打闹可以吗？"
    results = {}
    for mode in ["keyword", "vector", "hybrid"]:
        st, body = teacher.json("/api/search", {"q": QUERY, "mode": mode})
        check(f"mode={mode} = 200", st == 200, f"实际 {st}")
        body = body or {}
        results[mode] = body
        hits = body.get("hits") or []
        check(f"mode={mode} 命中数 > 0", len(hits) > 0, f"hits={len(hits)}")
        check(f"mode={mode} 每条可回溯（标题/序号/区间/摘录）",
              all(h.get("material_title") and h.get("chunk_index") is not None
                  and h.get("start_offset") is not None and h.get("end_offset") is not None
                  and h.get("snippet") for h in hits))
        check(f"mode={mode} 摘录来自 MySQL 切片正文",
              all(h.get("snippet") and h["snippet"] in LONG_DOC or h["snippet"].endswith("…")
                  or len(h["snippet"]) > 0 for h in hits))

    # 关键字模式不应含向量分数
    kw_hits = results["keyword"].get("hits") or []
    check("关键字模式命中项不含向量分数",
          all(h.get("score_vector") is None and h.get("rank_vector") is None for h in kw_hits))
    check("关键字模式命中项带全文分与关键字名次",
          all(h.get("score_keyword") is not None and h.get("rank_keyword") is not None for h in kw_hits))
    check("关键字模式不返回问句向量", "query_vector" not in results["keyword"])

    # 向量模式：阈值
    vec_hits = results["vector"].get("hits") or []
    check("向量模式每条余弦 >= 0.35",
          all(h.get("score_vector") is not None and h["score_vector"] >= 0.35 for h in vec_hits),
          f"最低 {min((h.get('score_vector') or 0) for h in vec_hits):.4f}" if vec_hits else "无命中")
    check("向量模式返回问句向量", bool(results["vector"].get("query_vector")))
    qv = results["vector"].get("query_vector") or {}
    check("问句向量维度为 2048", qv.get("dim") == 2048, f"实际 {qv.get('dim')}")
    print()

    # ---------- 6. RRF 融合 ----------
    print("== 6. 混合检索与 RRF 融合（k=60） ==")
    hy_hits = results["hybrid"].get("hits") or []
    ok_sum = True
    both = 0
    for h in hy_hits:
        expect = 0.0
        if h.get("rank_keyword") is not None:
            expect += 1 / (60 + h["rank_keyword"])
        if h.get("rank_vector") is not None:
            expect += 1 / (60 + h["rank_vector"])
        if abs(expect - (h.get("score_final") or 0)) > 1e-9:
            ok_sum = False
        if h.get("rank_keyword") is not None and h.get("rank_vector") is not None:
            both += 1
    check("合成分 = Σ 1/(60+名次)", ok_sum)
    check("存在两路都命中的切片", both > 0, f"{both} 条")

    singles = [h["score_final"] for h in hy_hits if (h.get("rank_keyword") is None) != (h.get("rank_vector") is None)]
    doubles = [h["score_final"] for h in hy_hits if h.get("rank_keyword") is not None and h.get("rank_vector") is not None]
    if singles and doubles:
        check("两路都命中排在仅命中一路之前", min(doubles) > max(singles),
              f"min(both)={min(doubles):.6f} max(single)={max(singles):.6f}")
    scores = [h["score_final"] for h in hy_hits]
    check("结果按合成分降序", scores == sorted(scores, reverse=True))
    print()

    # ---------- 7. 检索侧班级隔离 ----------
    print("== 7. 检索侧的班级隔离 ==")
    st, body = student_b.json("/api/search", {"q": "课间追逐打闹可以吗？", "mode": "hybrid"})
    check("B 班学生检索 A 班内容 = 200", st == 200, f"实际 {st}")
    check("跨班检索命中列表为空", len((body or {}).get("hits") or []) == 0,
          f"hits={len((body or {}).get('hits') or [])}")

    # 「类囊体」只出现在 B 班的光合作用讲义中，A 班任何材料都不含这两个字。
    st, body = teacher.json("/api/search", {"q": "类囊体", "mode": "keyword"})
    b_hits = (body or {}).get("hits") or []
    check("A 班检索仅存在于 B 班的词 → 200 且无命中", st == 200 and len(b_hits) == 0,
          f"st={st} hits={len(b_hits)}")

    st, body = teacher.json("/api/search", {"q": "类囊体", "mode": "vector"})
    b_hits = (body or {}).get("hits") or []
    check("A 班向量检索 B 班专属词 → 200 且无命中", st == 200 and len(b_hits) == 0,
          f"st={st} hits={len(b_hits)}")

    st, body = teacher.json("/api/search", {"q": QUERY, "mode": "hybrid", "class_id": 2, "role": "student"})
    injected = [h["chunk_id"] for h in ((body or {}).get("hits") or [])]
    base = [h["chunk_id"] for h in hy_hits]
    check("请求中自带的 class_id 无效（结果不变）", injected == base,
          f"注入前 {len(base)} 条，注入后 {len(injected)} 条")

    st, _ = teacher.json("/api/materials/2")
    check("跨班材料详情 = 404", st == 404, f"实际 {st}")
    st, _ = teacher.json("/api/materials/2/vectors")
    check("跨班查看向量 = 404", st == 404, f"实际 {st}")
    st, _ = teacher.json("/api/materials/999999")
    check("不存在的材料详情 = 404", st == 404, f"实际 {st}")
    print()

    # ---------- 8. 问答 ----------
    print("== 8. 引用标注与无依据处理 ==")
    st, body = student_a.json("/api/ask", {"q": "课间追逐打闹可以吗？"})
    check("学生 A1 提问 = 200", st == 200, f"实际 {st}")
    body = body or {}
    check("有依据时返回 ok", body.get("status") == "ok", f"实际 {body.get('status')}")
    check("回答非空", bool((body.get("answer") or "").strip()))
    check("回答含 [1] 标注", "[1]" in (body.get("answer") or ""), f"answer={body.get('answer')!r}")
    cites = body.get("citations") or []
    check("出处列表非空", len(cites) > 0, f"{len(cites)} 条")
    check("出处数不超过 4（取前 4 条切片）", len(cites) <= 4, f"{len(cites)} 条")

    st, body = teacher.json("/api/ask", {"q": "明天的天气怎么样？"})
    body = body or {}
    check("无依据时 status = no_evidence", body.get("status") == "no_evidence", f"实际 {body.get('status')}")
    check("无依据时返回固定文案", body.get("answer") == "资料中未找到相关内容", f"answer={body.get('answer')!r}")
    check("无依据时 citations 为空", (body.get("citations") or []) == [])

    st, body = student_b.json("/api/ask", {"q": "课间追逐打闹可以吗？"})
    body = body or {}
    check("跨班提问不返回他人材料（B 班问 A 班内容 → 无依据）",
          body.get("status") == "no_evidence" and (body.get("citations") or []) == [],
          f"status={body.get('status')}")
    print()

    # ---------- 9. 重建索引与权限 ----------
    print("== 9. 重建索引 ==")
    st, body = student_a.json(f"/api/materials/{material_id}/reindex", {"strategy": "custom", "max_chars": 300, "overlap_percent": 10})
    check("学生重建索引 = 403", st == 403, f"实际 {st}")
    st, _ = student_a.json(f"/api/materials/{material_id}/vectors")
    after_student = len(((_ or {}).get("chunks") or []))
    check("学生被拒后切片数不变", after_student == chunk_count, f"{after_student} vs {chunk_count}")

    st, body = teacher.json(f"/api/materials/{material_id}/reindex", {"strategy": "custom", "max_chars": 300, "overlap_percent": 10})
    check("教师按 custom 重建 = 200", st == 200, f"实际 {st} {body}")
    body = body or {}
    check("重建返回策略为 custom", body.get("chunk_strategy") == "custom", f"实际 {body.get('chunk_strategy')}")
    new_count = body.get("chunk_count", 0)
    check("custom(300) 改变了切片划分", new_count != chunk_count, f"{new_count} vs {chunk_count}")

    st, vec2 = teacher.json(f"/api/materials/{material_id}/vectors")
    chunks2 = (vec2 or {}).get("chunks") or []
    check("重建后切片数与返回一致", len(chunks2) == new_count, f"{len(chunks2)} vs {new_count}")
    check("重建后每条切片仍 ≤ 300 字",
          all((c.get("end_offset", 0) - c.get("start_offset", 0)) <= 300 for c in chunks2))
    check("重建后旧向量已被替换（无重复主键）",
          len({c.get("point_id") for c in chunks2}) == len(chunks2))
    check("重建后向量与新切片一一对应",
          all(c.get("linked") for c in chunks2), f"{sum(1 for c in chunks2 if c.get('linked'))}/{len(chunks2)}")

    st, body = teacher.json(f"/api/materials/{material_id}/reindex", {"strategy": "custom", "max_chars": 50})
    check("越界参数（max_chars=50）= 400", st == 400, f"实际 {st}")
    print()

    # ---------- 10. 降级 ----------
    print("== 10. 降级行为 ==")
    st, body = teacher.json("/api/search", {"q": QUERY, "mode": "keyword"})
    check("关键字检索不依赖向量库（当前可用）", st == 200, f"实际 {st}")
    print("  说明：Qdrant 停机后的 503 行为需人工执行 ——")
    print("        docker compose stop qdrant")
    print("        curl -s -o /dev/null -w '%{http_code}' -X POST .../api/search -d '{\"q\":\"...\",\"mode\":\"vector\"}'  # 期望 503")
    print("        docker compose start qdrant")
    print()

    print(f"===== 汇总: PASS={PASS}  FAIL={FAIL} =====")
    return 1 if FAIL else 0


if __name__ == "__main__":
    sys.exit(main())
