#!/usr/bin/env python3
"""一键执行 apitest 全部用例，并生成 report/index.html。

    python apitest/run_all.py
    python apitest/run_all.py --only skills      # 只跑路径含 skills 的用例
    BASE_URL=http://127.0.0.1:8080 python apitest/run_all.py

退出码：0 全部通过；1 有失败；2 环境不可用（服务未起或无法回查数据库）。
"""

import argparse
import datetime
import html
import os
import sys
import time

ROOT = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, ROOT)

from common import db, env, runner  # noqa: E402

REPORT_DIR = os.path.join(ROOT, "report")


def write_report(records, base_url, started_at):
    os.makedirs(REPORT_DIR, exist_ok=True)
    total = sum(r["passed"] + r["failed"] for r in records)
    passed = sum(r["passed"] for r in records)
    failed = total - passed
    case_failed = sum(1 for r in records if not r["ok"])
    duration = sum(r["duration_ms"] for r in records) / 1000

    rows = []
    for rec in records:
        badge = "ok" if rec["ok"] else "bad"
        label = "PASS" if rec["ok"] else "FAIL"
        items = []
        for item in rec["results"]:
            cls = "ok" if item["ok"] else "bad"
            mark = "PASS" if item["ok"] else "FAIL"
            detail = html.escape(item["detail"]) if item["detail"] else ""
            items.append(
                f'<li class="{cls}"><span class="mark">{mark}</span>'
                f'<span class="title">{html.escape(item["title"])}</span>'
                f'<span class="detail">{detail}</span></li>')
        rows.append(f"""
        <section class="case {badge}">
          <header>
            <span class="badge">{label}</span>
            <span class="name">{html.escape(rec["name"])}</span>
            <span class="meta">{html.escape(rec["rel"])} · {rec["passed"]}/{rec["passed"] + rec["failed"]} · {rec["duration_ms"]:.0f} ms</span>
          </header>
          <ul>{"".join(items)}</ul>
        </section>""")

    doc = f"""<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<title>CampusClaw 接口测试报告</title>
<style>
  :root {{ --red:#94070A; --ok:#1f7a3d; --bad:#b3261e; --ink:#1d1d1f; --muted:#6b6b70; --line:#e5e5ea; }}
  * {{ box-sizing:border-box; }}
  body {{ margin:0; padding:32px; background:#fafafa; color:var(--ink);
         font:14px/1.6 -apple-system,"Segoe UI","Microsoft YaHei",sans-serif; }}
  h1 {{ font-size:22px; margin:0 0 4px; }}
  h1 span {{ color:var(--red); }}
  .sub {{ color:var(--muted); margin-bottom:24px; }}
  .cards {{ display:flex; gap:12px; flex-wrap:wrap; margin-bottom:28px; }}
  .card {{ background:#fff; border:1px solid var(--line); border-radius:10px; padding:14px 18px; min-width:150px; }}
  .card b {{ display:block; font-size:22px; }}
  .card.ok b {{ color:var(--ok); }}
  .card.bad b {{ color:var(--bad); }}
  .card small {{ color:var(--muted); }}
  .case {{ background:#fff; border:1px solid var(--line); border-left:4px solid var(--ok);
           border-radius:10px; margin-bottom:14px; overflow:hidden; }}
  .case.bad {{ border-left-color:var(--bad); }}
  .case header {{ display:flex; align-items:baseline; gap:10px; padding:12px 16px;
                  border-bottom:1px solid var(--line); flex-wrap:wrap; }}
  .badge {{ font-size:12px; font-weight:700; padding:1px 8px; border-radius:999px;
            color:#fff; background:var(--ok); }}
  .case.bad .badge {{ background:var(--bad); }}
  .name {{ font-weight:600; }}
  .meta {{ color:var(--muted); font-size:12px; margin-left:auto; }}
  ul {{ list-style:none; margin:0; padding:8px 16px 12px; }}
  li {{ display:flex; gap:10px; padding:3px 0; border-bottom:1px dashed #f0f0f2; }}
  li:last-child {{ border-bottom:0; }}
  .mark {{ font-size:12px; font-weight:700; color:var(--ok); min-width:38px; }}
  li.bad .mark {{ color:var(--bad); }}
  .title {{ min-width:280px; }}
  .detail {{ color:var(--muted); font-family:ui-monospace,Consolas,monospace; font-size:12px;
             word-break:break-all; }}
  footer {{ color:var(--muted); margin-top:24px; font-size:12px; }}
</style>
</head>
<body>
  <h1>CampusClaw <span>接口测试报告</span></h1>
  <div class="sub">目标 {html.escape(base_url)} · 开始于 {html.escape(started_at)} · 用时 {duration:.1f} s</div>
  <div class="cards">
    <div class="card"><b>{len(records)}</b><small>用例脚本</small></div>
    <div class="card ok"><b>{passed}</b><small>通过断言</small></div>
    <div class="card bad"><b>{failed}</b><small>失败断言</small></div>
    <div class="card"><b>{case_failed}</b><small>失败用例</small></div>
  </div>
  {"".join(rows)}
  <footer>由 apitest/run_all.py 生成 · 报告与中间产物不入库</footer>
</body>
</html>
"""
    path = os.path.join(REPORT_DIR, "index.html")
    with open(path, "w", encoding="utf-8") as fh:
        fh.write(doc)
    return path


def main():
    parser = argparse.ArgumentParser(description="CampusClaw 接口自动化测试")
    parser.add_argument("--base-url", default=None, help="被测服务地址，默认取 .env 的 WEB_PORT")
    parser.add_argument("--only", default=None, help="只跑路径包含该子串的用例")
    args = parser.parse_args()

    if not db.ping():
        print("无法回查数据库：请确认已 docker compose up，且本机可执行 docker compose exec。")
        return 2

    run_env = runner.Env(args.base_url)
    discovered = runner.discover()
    if args.only:
        discovered = [(rel, path) for rel, path in discovered if args.only in rel.replace(os.sep, "/")]
    if not discovered:
        print("没有找到用例脚本。")
        return 1

    started_at = datetime.datetime.now().strftime("%Y-%m-%d %H:%M:%S")
    print(f"== 目标 {run_env.base_url} · {len(discovered)} 个用例 ==\n")

    records = []
    for rel, path in discovered:
        label = rel.replace(os.sep, "/")
        begin = time.time()
        case = runner.run_case(path, run_env, rel)
        duration = (time.time() - begin) * 1000
        records.append({
            "name": case.name,
            "rel": label,
            "ok": case.ok,
            "passed": case.passed,
            "failed": case.failed,
            "duration_ms": duration,
            "results": case.results,
        })
        mark = "PASS" if case.ok else "FAIL"
        print(f"  {mark}  {case.name}  ({case.passed}/{case.passed + case.failed}, {duration:.0f} ms)")
        for item in case.results:
            if not item["ok"]:
                print(f"        失败：{item['title']}  {item['detail']}")

    report = write_report(records, run_env.base_url, started_at)
    total = sum(r["passed"] + r["failed"] for r in records)
    passed = sum(r["passed"] for r in records)
    failed = total - passed
    print(f"\n===== 汇总：用例 {len(records)} 个，断言 {total} 条，PASS={passed} FAIL={failed} =====")
    print(f"报告：{os.path.relpath(report, env.ROOT)}")
    return 0 if failed == 0 else 1


if __name__ == "__main__":
    sys.exit(main())
