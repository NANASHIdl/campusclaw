"""断言记录器。

三类断言分别对应任务要求：
  - status()     看状态码；
  - field()/keys() 看响应内容；
  - db_*()       回查数据库，确认真的落库了。
"""

import json

from . import db


def dig(payload, path, default=None):
    """按 "a.b.0.c" 取嵌套值；取不到返回 default。"""
    cur = payload
    for part in str(path).split("."):
        if isinstance(cur, dict):
            if part not in cur:
                return default
            cur = cur[part]
        elif isinstance(cur, list):
            try:
                cur = cur[int(part)]
            except (ValueError, IndexError):
                return default
        else:
            return default
    return cur


class Case:
    """一条用例的断言收集器。"""

    def __init__(self, name):
        self.name = name
        self.results = []

    # --- 记录 -------------------------------------------------------------

    def check(self, title, ok, detail=""):
        self.results.append({"title": title, "ok": bool(ok), "detail": str(detail)})
        return bool(ok)

    @property
    def ok(self):
        return all(r["ok"] for r in self.results)

    @property
    def passed(self):
        return sum(1 for r in self.results if r["ok"])

    @property
    def failed(self):
        return sum(1 for r in self.results if not r["ok"])

    # --- 状态码 -----------------------------------------------------------

    def status(self, resp, expect, title=None):
        title = title or f"HTTP {expect}"
        return self.check(title, resp.status == expect,
                          f"实际 {resp.status} {resp.text[:160]}")

    def in_status(self, resp, expects, title=None):
        title = title or f"HTTP ∈ {list(expects)}"
        return self.check(title, resp.status in expects,
                          f"实际 {resp.status} {resp.text[:160]}")

    # --- 响应内容 ---------------------------------------------------------

    def field(self, resp, path, expect, title=None):
        title = title or f"响应字段 {path} = {expect!r}"
        actual = dig(resp.json, path, default="<缺失>")
        return self.check(title, actual == expect, f"实际 {actual!r}")

    def truthy(self, resp, path, title=None):
        title = title or f"响应字段 {path} 非空"
        actual = dig(resp.json, path)
        return self.check(title, bool(actual), f"实际 {actual!r}")

    def keys(self, resp, *paths, title=None):
        title = title or "响应含 " + "、".join(paths)
        missing = [p for p in paths if dig(resp.json, p, default="<缺失>") == "<缺失>"]
        return self.check(title, not missing, f"缺失 {missing}")

    def no_keys(self, resp, *keys, title=None):
        title = title or "响应不含 " + "、".join(keys)
        payload = resp.json if isinstance(resp.json, dict) else {}
        present = [k for k in keys if k in payload]
        return self.check(title, not present, f"出现了 {present}")

    def json_ok(self, resp, title="响应体是合法 JSON"):
        return self.check(title, resp.json is not None, f"正文 {resp.text[:160]!r}")

    def empty_body(self, resp, title="响应体为空"):
        return self.check(title, (resp.body or b"") in (b"", b"\n"), f"实际 {resp.body!r}")

    def text_contains(self, resp, needle, title=None):
        title = title or f"正文含 {needle!r}"
        return self.check(title, needle in resp.text, f"正文 {resp.text[:160]!r}")

    # --- 数据库回查 -------------------------------------------------------

    def db_value(self, sql, expect, title=None):
        title = title or f"库中 {expect!r}"
        actual = db.one(sql)
        return self.check(title, actual == str(expect), f"实际 {actual!r}（{sql}）")

    def db_text(self, sql, expect, title=None):
        title = title or "库中文本与提交一致"
        actual = db.text(sql)
        return self.check(title, actual == expect,
                          f"库 {len(actual or '')} 字，期望 {len(expect or '')} 字（{sql}）")

    def db_count(self, sql, expect, title=None):
        title = title or f"库中计数 = {expect}"
        actual = db.count(sql)
        return self.check(title, actual == expect, f"实际 {actual}（{sql}）")

    def db_at_least(self, sql, expect, title=None):
        title = title or f"库中计数 ≥ {expect}"
        actual = db.count(sql)
        return self.check(title, actual >= expect, f"实际 {actual}（{sql}）")

    def db_contains(self, sql, needle, title=None):
        title = title or f"库中文本含 {needle!r}"
        actual = db.text(sql) or ""
        return self.check(title, needle in actual, f"实际 {actual[:120]!r}")

    # --- 汇总 -------------------------------------------------------------

    def as_dict(self):
        return {
            "name": self.name,
            "ok": self.ok,
            "passed": self.passed,
            "failed": self.failed,
            "results": self.results,
        }

    def dump(self):
        print(json.dumps(self.as_dict(), ensure_ascii=False, indent=2))
