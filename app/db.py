"""数据访问层。

关键约定：所有材料 / 知识库查询都强制带 `class_id` 过滤，
class_id 只能来自会话，绝不接受客户端传入的值。
"""

import os
import sqlite3

from flask import current_app, g


def get_db():
    if "db" not in g:
        path = current_app.config["DATABASE_PATH"]
        parent = os.path.dirname(os.path.abspath(path))
        if parent:
            os.makedirs(parent, exist_ok=True)
        conn = sqlite3.connect(path)
        conn.row_factory = sqlite3.Row
        conn.execute("PRAGMA foreign_keys = ON")
        g.db = conn
    return g.db


def close_db(exc=None):
    db = g.pop("db", None)
    if db is not None:
        db.close()


def init_app(app):
    app.teardown_appcontext(close_db)


# --- 用户 ---------------------------------------------------------------

def get_user_by_username(username):
    return get_db().execute(
        "SELECT id, username, password_hash, role, class_id "
        "FROM users WHERE username = ?",
        (username,),
    ).fetchone()


# --- 材料（均按班级过滤） ------------------------------------------------

def list_materials(class_id):
    return get_db().execute(
        "SELECT m.id, m.title, m.created_at, u.username AS uploaded_by "
        "FROM materials m LEFT JOIN users u ON u.id = m.uploaded_by "
        "WHERE m.class_id = ? "
        "ORDER BY m.created_at DESC, m.id DESC",
        (class_id,),
    ).fetchall()


def get_material(material_id):
    """按 ID 取单条（不带班级条件，交由调用方判定越权，以便区分 404/403）。"""
    return get_db().execute(
        "SELECT id, title, class_id, file_path, uploaded_by, created_at "
        "FROM materials WHERE id = ?",
        (material_id,),
    ).fetchone()


def insert_material_with_knowledge(title, class_id, file_path, uploaded_by, body_text):
    """在单个事务内写入 materials 与 knowledge_entries，失败则整体回滚。"""
    db = get_db()
    try:
        cur = db.execute(
            "INSERT INTO materials (title, class_id, file_path, uploaded_by) "
            "VALUES (?, ?, ?, ?)",
            (title, class_id, file_path, uploaded_by),
        )
        material_id = cur.lastrowid
        db.execute(
            "INSERT INTO knowledge_entries (material_id, class_id, body_text) "
            "VALUES (?, ?, ?)",
            (material_id, class_id, body_text),
        )
        db.commit()
    except Exception:
        db.rollback()
        raise
    return material_id
