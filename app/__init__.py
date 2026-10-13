"""CampusClaw 应用工厂。

技术栈：Flask + SQLite（标准库 sqlite3）+ Jinja2 + werkzeug.security。
会话密钥 SECRET_KEY 必须来自环境变量；缺失时启动即失败（不静默使用默认值）。
"""

import os

from flask import Flask, jsonify, redirect, session, url_for


def create_app():
    app = Flask(__name__)

    secret = os.environ.get("SECRET_KEY")
    if not secret:
        raise RuntimeError(
            "SECRET_KEY 环境变量未设置。请参考 .env.example 配置会话签名密钥后重试。"
        )

    app.config.update(
        SECRET_KEY=secret,
        DATABASE_PATH=os.environ.get("DATABASE_PATH", "data/app.db"),
        UPLOAD_DIR=os.environ.get("UPLOAD_DIR", "uploads"),
        MAX_CONTENT_LENGTH=5 * 1024 * 1024,  # 单文件上限 5MB
        SESSION_COOKIE_HTTPONLY=True,
        SESSION_COOKIE_SAMESITE="Lax",
    )

    from . import auth, db, materials

    db.init_app(app)
    app.register_blueprint(auth.bp)
    app.register_blueprint(materials.bp)

    @app.get("/")
    def index():
        if session.get("user_id"):
            return redirect(url_for("materials.list_page"))
        return redirect(url_for("auth.login"))

    @app.get("/health")
    def health():
        return jsonify(status="ok")

    return app
