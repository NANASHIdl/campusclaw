"""建表并写入种子数据。

幂等：表用 CREATE TABLE IF NOT EXISTS；仅当 users 表为空时才写入种子，
因此可在容器每次启动时安全执行（不会覆盖已上传数据）。

用法：python scripts/init_db.py [--force]
  --force  先删除既有库文件再重建（本地调试用，会清空数据）
"""

import argparse
import os
import sqlite3
import sys

from werkzeug.security import generate_password_hash

BASE_DIR = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, BASE_DIR)

from app.env import load_env  # noqa: E402

load_env(os.path.join(BASE_DIR, ".env"))


def resolve(path):
    return path if os.path.isabs(path) else os.path.join(BASE_DIR, path)


SCHEMA = """
CREATE TABLE IF NOT EXISTS classes (
    id   INTEGER PRIMARY KEY,
    name TEXT NOT NULL UNIQUE
);

CREATE TABLE IF NOT EXISTS users (
    id            INTEGER PRIMARY KEY,
    username      TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    role          TEXT NOT NULL CHECK (role IN ('teacher', 'student')),
    class_id      INTEGER NOT NULL REFERENCES classes(id)
);

CREATE TABLE IF NOT EXISTS lectures (
    id       INTEGER PRIMARY KEY,
    class_id INTEGER NOT NULL REFERENCES classes(id),
    title    TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS assignments (
    id       INTEGER PRIMARY KEY,
    class_id INTEGER NOT NULL REFERENCES classes(id),
    title    TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS assistants (
    id       INTEGER PRIMARY KEY,
    class_id INTEGER NOT NULL REFERENCES classes(id),
    name     TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS skills (
    id       INTEGER PRIMARY KEY,
    class_id INTEGER NOT NULL REFERENCES classes(id),
    name     TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS materials (
    id          INTEGER PRIMARY KEY,
    title       TEXT NOT NULL,
    class_id    INTEGER NOT NULL REFERENCES classes(id),
    file_path   TEXT,
    uploaded_by INTEGER REFERENCES users(id),
    created_at  TEXT NOT NULL DEFAULT (datetime('now', 'localtime'))
);

CREATE TABLE IF NOT EXISTS knowledge_entries (
    id          INTEGER PRIMARY KEY,
    material_id INTEGER NOT NULL REFERENCES materials(id),
    class_id    INTEGER NOT NULL REFERENCES classes(id),
    body_text   TEXT NOT NULL,
    created_at  TEXT NOT NULL DEFAULT (datetime('now', 'localtime'))
);
"""


def seed(conn):
    cur = conn.cursor()

    cur.execute("INSERT INTO classes (id, name) VALUES (1, '班级A'), (2, '班级B')")

    users = [
        ("teacher_a", "teach123", "teacher", 1),
        ("student_a1", "stu123", "student", 1),
        ("student_b1", "stu123", "student", 2),
    ]
    for username, password, role, class_id in users:
        cur.execute(
            "INSERT INTO users (username, password_hash, role, class_id) "
            "VALUES (?, ?, ?, ?)",
            (username, generate_password_hash(password), role, class_id),
        )

    # 六类核心结构中的讲义 / 作业 / 助手 / 技能：各写入占位行
    cur.execute("INSERT INTO lectures (class_id, title) VALUES (1, 'A 班 · 一元二次方程讲义')")
    cur.execute("INSERT INTO assignments (class_id, title) VALUES (1, 'A 班 · 课后练习一')")
    cur.execute("INSERT INTO assistants (class_id, name) VALUES (1, 'A 班备课助手')")
    cur.execute("INSERT INTO skills (class_id, name) VALUES (1, 'A 班学情分析技能')")

    # 两班各一条可区分标题的材料 + 对应知识库条目
    samples = [
        (1, "A 班 · 一元二次方程讲义", "本讲义覆盖一元二次方程的定义、求根公式与判别式。"),
        (2, "B 班 · 光合作用讲义", "本讲义覆盖光合作用的光反应与暗反应阶段。"),
    ]
    for class_id, title, body in samples:
        cur.execute(
            "INSERT INTO materials (title, class_id, file_path, uploaded_by) "
            "VALUES (?, ?, NULL, NULL)",
            (title, class_id),
        )
        material_id = cur.lastrowid
        cur.execute(
            "INSERT INTO knowledge_entries (material_id, class_id, body_text) "
            "VALUES (?, ?, ?)",
            (material_id, class_id, body),
        )

    conn.commit()


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--force", action="store_true", help="删除既有库文件后重建")
    args = parser.parse_args()

    db_path = resolve(os.environ.get("DATABASE_PATH", "data/app.db"))
    upload_dir = resolve(os.environ.get("UPLOAD_DIR", "uploads"))

    os.makedirs(os.path.dirname(db_path), exist_ok=True)
    os.makedirs(upload_dir, exist_ok=True)

    if args.force and os.path.exists(db_path):
        os.remove(db_path)
        print(f"[init_db] 已删除旧库：{db_path}")

    conn = sqlite3.connect(db_path)
    conn.execute("PRAGMA foreign_keys = ON")
    try:
        conn.executescript(SCHEMA)
        count = conn.execute("SELECT COUNT(*) FROM users").fetchone()[0]
        if count == 0:
            seed(conn)
            print(f"[init_db] 建表完成并写入种子数据：{db_path}")
        else:
            print(f"[init_db] 库已存在 {count} 个用户，跳过种子写入：{db_path}")
    finally:
        conn.close()

    print(f"[init_db] 上传目录：{upload_dir}")


if __name__ == "__main__":
    main()
