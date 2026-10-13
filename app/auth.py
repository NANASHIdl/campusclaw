"""认证与授权：登录、登出、login_required。"""

from functools import wraps

from flask import (
    Blueprint,
    jsonify,
    redirect,
    render_template,
    request,
    session,
    url_for,
)
from werkzeug.security import check_password_hash

from .db import get_user_by_username

bp = Blueprint("auth", __name__)


def login_required(view):
    """未登录：API 返回 401 JSON，页面重定向到登录页。"""

    @wraps(view)
    def wrapped(*args, **kwargs):
        if not session.get("user_id"):
            if request.path.startswith("/api/"):
                return jsonify(error="unauthorized"), 401
            return redirect(url_for("auth.login", next=request.path))
        return view(*args, **kwargs)

    return wrapped


@bp.route("/login", methods=["GET", "POST"])
def login():
    if request.method == "POST":
        username = (request.form.get("username") or "").strip()
        password = request.form.get("password") or ""
        user = get_user_by_username(username)
        if user is None or not check_password_hash(user["password_hash"], password):
            return render_template("login.html", error="用户名或密码错误"), 401

        session.clear()
        session["user_id"] = user["id"]
        session["username"] = user["username"]
        session["role"] = user["role"]
        session["class_id"] = user["class_id"]
        return redirect(url_for("materials.list_page"))

    if session.get("user_id"):
        return redirect(url_for("materials.list_page"))
    return render_template("login.html")


@bp.route("/logout", methods=["GET", "POST"])
def logout():
    session.clear()
    return redirect(url_for("auth.login"))
