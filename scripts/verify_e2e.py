"""第 3 课 CampusClaw 端到端验收脚本。

对运行中的三容器栈（默认 http://127.0.0.1:8080）逐条执行 spec 场景。
不依赖第三方库，只用标准库。
"""

import http.cookiejar
import json
import sys
import urllib.error
import urllib.request
import uuid

import os
BASE = os.environ.get("BASE_URL", "http://127.0.0.1:8080")

PASS, FAIL = [], []


def check(name, cond, detail=""):
    if cond:
        PASS.append(name)
        print(f"  PASS  {name}  {detail}")
    else:
        FAIL.append(name)
        print(f"  FAIL  {name}  {detail}")


def opener():
    cj = http.cookiejar.CookieJar()
    return urllib.request.build_opener(urllib.request.HTTPCookieProcessor(cj)), cj


def call(op, method, path, json_body=None, raw_body=None, headers=None):
    h = dict(headers or {})
    data = None
    if json_body is not None:
        data = json.dumps(json_body).encode()
        h["Content-Type"] = "application/json"
    elif raw_body is not None:
        data = raw_body
    r = urllib.request.Request(BASE + path, data=data, headers=h, method=method)
    try:
        resp = op.open(r)
        return resp.status, dict(resp.headers), resp.read()
    except urllib.error.HTTPError as e:
        return e.code, dict(e.headers), e.read()


def multipart(field, filename, content):
    boundary = "----cc" + uuid.uuid4().hex
    body = b"".join([
        f"--{boundary}\r\n".encode(),
        f'Content-Disposition: form-data; name="{field}"; filename="{filename}"\r\n'.encode(),
        b"Content-Type: application/octet-stream\r\n\r\n",
        content,
        f"\r\n--{boundary}--\r\n".encode(),
    ])
    return body, {"Content-Type": f"multipart/form-data; boundary={boundary}"}


def jload(raw):
    try:
        return json.loads(raw)
    except Exception:
        return None


def main():
    print("=== 1. 健康检查 ===")
    op, _ = opener()
    st, _, body = call(op, "GET", "/health")
    check("GET /health 免登录返回 200", st == 200, f"status={st} body={body[:60]!r}")
    check("health 响应不含材料数据", b"material" not in body.lower(), "")

    print("=== 2. 未认证访问受保护接口 ===")
    for path in ["/api/materials", "/api/me"]:
        st, _, _ = call(op, "GET", path)
        check(f"未登录 {path} -> 401", st == 401, f"status={st}")
    st, _, _ = call(op, "POST", "/api/materials/upload")
    check("未登录上传 -> 401", st == 401, f"status={st}")
    st, _, _ = call(op, "GET", "/api/materials/1/file")
    check("未登录下载 -> 401", st == 401, f"status={st}")

    print("=== 3. 统一失败响应 ===")
    op2, _ = opener()
    st_a, _, body_a = call(op2, "POST", "/api/login", {"username": "no_such_user_xyz", "password": "whatever"})
    op3, _ = opener()
    st_b, _, body_b = call(op3, "POST", "/api/login", {"username": "teacher_a", "password": "wrong-password"})
    check("账号不存在与口令错误状态码一致", st_a == st_b, f"{st_a} vs {st_b}")
    check("账号不存在与口令错误响应体一致", body_a == body_b, f"{body_a!r} vs {body_b!r}")

    print("=== 4. 教师登录与会话 ===")
    teach, teach_cj = opener()
    st, hdr, body = call(teach, "POST", "/api/login", {"username": "teacher_a", "password": "teach123"})
    info = jload(body) or {}
    check("教师登录 -> 200", st == 200, f"status={st}")
    check("响应体含 id/username/role/class_id", all(k in info for k in ("id", "username", "role", "class_id")), str(info))
    check("响应体不含口令明文", "teach123" not in body.decode(errors="ignore"), "")
    check("响应体不含口令哈希", "pbkdf2" not in body.decode(errors="ignore"), "")
    sc = hdr.get("Set-Cookie", "")
    check("Set-Cookie 已下发", "cc_session=" in sc, sc[:80])
    check("Cookie 设置 HttpOnly", "HttpOnly" in sc, "")
    check("Cookie 设置 SameSite", "SameSite" in sc, "")
    check("Cookie 不含角色或班级", ("teacher" not in sc.lower()) and ("class" not in sc.lower()), "")
    cookie_val = sc.split("cc_session=")[1].split(";")[0] if "cc_session=" in sc else ""
    teacher_cookie = cookie_val

    print("=== 5. 会话固定防御：登录成功换发标识 ===")
    fix_op, _ = opener()
    st, _, _ = call(fix_op, "POST", "/api/login", {"username": "teacher_a", "password": "teach123"},
                    headers={"Cookie": "cc_session=attacker-preset-token-abc"})
    new_sc = ""
    # 重新取一次响应头
    fix_op2, _ = opener()
    st2, hdr2, _ = call(fix_op2, "POST", "/api/login", {"username": "teacher_a", "password": "teach123"},
                        headers={"Cookie": "cc_session=attacker-preset-token-abc"})
    new_sc = hdr2.get("Set-Cookie", "")
    issued = new_sc.split("cc_session=")[1].split(";")[0] if "cc_session=" in new_sc else ""
    check("登录成功签发了新的会话标识", bool(issued) and issued != "attacker-preset-token-abc", issued[:16])
    op_old, _ = opener()
    st_old, _, _ = call(op_old, "GET", "/api/materials", headers={"Cookie": "cc_session=attacker-preset-token-abc"})
    check("预置的旧标识立即失效 -> 401", st_old == 401, f"status={st_old}")

    print("=== 6. 班级隔离：列表与详情 ===")
    st, _, body = call(teach, "GET", "/api/materials")
    listing = jload(body) or {}
    titles = [m["title"] for m in listing.get("materials", [])]
    check("教师 A 列表 200", st == 200, f"status={st}")
    check("教师 A 列表不含 B 班材料", not any("B 班" in t for t in titles), str(titles))
    check("教师 A 列表含 A 班材料", any("A 班" in t for t in titles), str(titles))

    # 找出 B 班材料的 id：用学生 B1 的会话查询
    print("=== 7. 学生 B1 登录并查看本班 ===")
    b1, _ = opener()
    st, _, body = call(b1, "POST", "/api/login", {"username": "student_b1", "password": "stu123"})
    check("学生 B1 登录 -> 200", st == 200, f"status={st}")
    st, _, body = call(b1, "GET", "/api/materials")
    b_list = (jload(body) or {}).get("materials", [])
    b_titles = [m["title"] for m in b_list]
    check("学生 B1 列表不含 A 班材料", not any("A 班" in t for t in b_titles), str(b_titles))
    b_material_id = b_list[0]["id"] if b_list else None

    print("=== 8. 同构 404：跨班访问 vs 记录不存在 ===")
    st_cross, _, body_cross = call(teach, "GET", f"/api/materials/{b_material_id}")
    st_miss, _, body_miss = call(teach, "GET", "/api/materials/999999")
    check("跨班详情 -> 404", st_cross == 404, f"status={st_cross}")
    check("不存在详情 -> 404", st_miss == 404, f"status={st_miss}")
    check("跨班与不存在的响应完全一致", (st_cross, body_cross) == (st_miss, body_miss),
          f"{st_cross},{body_cross!r} vs {st_miss},{body_miss!r}")

    print("=== 9. 客户端自报身份不改变判定 ===")
    st1, _, body1 = call(teach, "GET", "/api/materials")
    st2, _, body2 = call(teach, "GET", "/api/materials?class_id=2&role=teacher",
                         headers={"X-Class-Id": "2", "X-Role": "teacher"})
    check("带 class_id/role 的列表结果不变", body1 == body2, "")

    print("=== 10. 学生上传被拒绝 ===")
    s1, _ = opener()
    call(s1, "POST", "/api/login", {"username": "student_a1", "password": "stu123"})
    st, _, _ = call(s1, "GET", "/api/materials")
    before = len((jload(_) or {}).get("materials", [])) if False else None
    st_before, _, body_before = call(teach, "GET", "/api/materials")
    count_before = len((jload(body_before) or {}).get("materials", []))
    mp_body, mp_hdr = multipart("file", "student_try.md", "# 学生尝试上传\n".encode())
    st, _, body = call(s1, "POST", "/api/materials/upload", raw_body=mp_body, headers=mp_hdr)
    check("学生上传 -> 403", st == 403, f"status={st} body={body[:60]!r}")
    st_after, _, body_after = call(teach, "GET", "/api/materials")
    count_after = len((jload(body_after) or {}).get("materials", []))
    check("学生上传后材料数不变", count_before == count_after, f"{count_before} -> {count_after}")

    print("=== 11. 教师上传与两表入库 ===")
    mp_body, mp_hdr = multipart("file", "三角函数讲义.md", "# 三角函数\n\n正弦、余弦、正切。\n".encode())
    st, _, body = call(teach, "POST", "/api/materials/upload", raw_body=mp_body, headers=mp_hdr)
    up = jload(body) or {}
    check("教师上传 -> 201", st == 201, f"status={st} body={body[:80]!r}")
    check("返回含 material_id", "material_id" in up, str(up))
    new_id = up.get("material_id")

    st, _, body = call(teach, "GET", "/api/materials")
    titles_after = [m["title"] for m in (jload(body) or {}).get("materials", [])]
    check("上传后教师列表可见新记录", any("三角函数讲义" in t for t in titles_after), str(titles_after))

    st, _, body = call(s1, "GET", "/api/materials")
    s_titles = [m["title"] for m in (jload(body) or {}).get("materials", [])]
    check("同班学生也能看到新记录", any("三角函数讲义" in t for t in s_titles), str(s_titles))
    check("学生列表仍不含 B 班材料", not any("B 班" in t for t in s_titles), "")

    print("=== 12. 白名单外扩展名被拒绝且无孤儿 ===")
    st_before, _, body_before = call(teach, "GET", "/api/materials")
    count_before = len((jload(body_before) or {}).get("materials", []))
    mp_body, mp_hdr = multipart("file", "讲义.pdf", b"%PDF-1.4 fake")
    st, _, body = call(teach, "POST", "/api/materials/upload", raw_body=mp_body, headers=mp_hdr)
    check("上传 .pdf -> 400", st == 400, f"status={st}")
    mp_body, mp_hdr = multipart("file", "空文件.md", b"")
    st, _, body = call(teach, "POST", "/api/materials/upload", raw_body=mp_body, headers=mp_hdr)
    check("上传空 .md -> 400", st == 400, f"status={st}")
    st_after, _, body_after = call(teach, "GET", "/api/materials")
    count_after = len((jload(body_after) or {}).get("materials", []))
    check("失败上传未产生记录", count_before == count_after, f"{count_before} -> {count_after}")

    print("=== 13. 下载鉴权 ===")
    op_anon, _ = opener()
    st, _, _ = call(op_anon, "GET", f"/api/materials/{new_id}/file")
    check("未认证下载 -> 401", st == 401, f"status={st}")

    st, _, body = call(teach, "GET", f"/api/materials/{new_id}/file")
    check("本班材料下载 -> 200", st == 200, f"status={st}")
    check("下载内容与上传一致", "正弦" in body.decode("utf-8", "ignore"), body[:40])

    st_cross, _, body_cross = call(teach, "GET", f"/api/materials/{b_material_id}/file")
    st_miss, _, body_miss = call(teach, "GET", "/api/materials/999999/file")
    check("跨班下载 -> 404", st_cross == 404, f"status={st_cross}")
    check("跨班下载与不存在下载响应一致", (st_cross, body_cross) == (st_miss, body_miss), "")
    check("跨班下载未返回文件内容", "光合作用" not in body_cross.decode("utf-8", "ignore"), "")

    print("=== 14. 登出后会话立即失效 ===")
    lo, _ = opener()
    call(lo, "POST", "/api/login", {"username": "student_a1", "password": "stu123"})
    st, _, _ = call(lo, "GET", "/api/materials")
    check("登出前可访问", st == 200, f"status={st}")
    call(lo, "POST", "/api/logout")
    st, _, _ = call(lo, "GET", "/api/materials")
    check("登出后 -> 401", st == 401, f"status={st}")

    print()
    print(f"===== 汇总：PASS={len(PASS)}  FAIL={len(FAIL)} =====")
    if FAIL:
        print("失败项：")
        for f in FAIL:
            print("  -", f)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
