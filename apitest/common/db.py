"""数据库回查。

数据库端口不对宿主机开放（只有 web 发布端口），所以统一经 `docker compose exec db mysql`
执行 SQL。这样既不用装 MySQL 驱动，也不用为了测试改动端口暴露约定。
"""

import subprocess

from . import env


class DbError(RuntimeError):
    pass


def _run(sql):
    cmd = [
        "docker", "compose", "exec", "-T", "db", "mysql",
        "--default-character-set=utf8mb4",
        "-u" + env.ENV.get("DB_USER", "campusclaw"),
        "-p" + env.ENV.get("DB_PASSWORD", ""),
        "-D", env.ENV.get("DB_NAME", "campusclaw"),
        "-N", "-B", "-e", sql,
    ]
    proc = subprocess.run(cmd, cwd=env.ROOT, capture_output=True, text=True,
                          encoding="utf-8", errors="replace")
    if proc.returncode != 0:
        raise DbError(f"SQL 执行失败: {sql}\n{(proc.stderr or '').strip()}")
    rows = []
    for line in (proc.stdout or "").splitlines():
        if not line or line.startswith("mysql: [Warning]"):
            continue
        rows.append(line.split("\t"))
    return rows


def query(sql):
    """返回二维字符串数组（TSV 切分）。含换行的字段请改用 text()。"""
    return _run(sql)


def one(sql):
    rows = _run(sql)
    return rows[0][0] if rows else None


def text(sql):
    """读取可能含换行的文本字段：用 HEX() 取回再解码，避免按行切分破坏内容。"""
    value = one(sql)
    if value in (None, "", "NULL"):
        return None
    return bytes.fromhex(value).decode("utf-8")


def count(sql):
    value = one(sql)
    try:
        return int(value)
    except (TypeError, ValueError):
        return -1


def ping():
    """确认数据库可回查；失败时给出可读提示。"""
    try:
        return one("SELECT 1") == "1"
    except (DbError, FileNotFoundError, OSError):
        return False
