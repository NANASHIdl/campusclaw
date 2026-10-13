"""材料列表、单条访问与教师上传。

班级隔离在服务端强制实施：
- 列表只查 session["class_id"]；
- 按 ID 访问时校验记录归属，跨班返回 403；
- 上传写入的 class_id 只取自 session，忽略任何客户端传入值。
"""

import os
import uuid

from flask import (
    Blueprint,
    current_app,
    jsonify,
    render_template,
    request,
    session,
)
from werkzeug.utils import secure_filename

from .auth import login_required
from .db import (
    get_material,
    insert_material_with_knowledge,
    list_materials,
)
from .knowledge import parse_file

bp = Blueprint("materials", __name__)

ALLOWED_EXT = {".txt", ".md"}


@bp.get("/materials")
@login_required
def list_page():
    rows = list_materials(session["class_id"])
    return render_template(
        "materials.html",
        materials=rows,
        role=session.get("role"),
        username=session.get("username"),
        class_id=session.get("class_id"),
    )


@bp.get("/api/materials")
@login_required
def list_api():
    rows = list_materials(session["class_id"])
    return jsonify(materials=[dict(r) for r in rows])


@bp.get("/api/materials/<int:material_id>")
@login_required
def detail_api(material_id):
    row = get_material(material_id)
    if row is None:
        return jsonify(error="not found"), 404
    if row["class_id"] != session["class_id"]:
        # 跨班：统一 403，且不泄露他班任何字段
        return jsonify(error="forbidden"), 403
    return jsonify(material=dict(row))


@bp.post("/api/materials/upload")
@login_required
def upload():
    if session.get("role") != "teacher":
        return jsonify(error="forbidden"), 403

    file = request.files.get("file")
    if file is None or not file.filename:
        return jsonify(error="no file provided"), 400

    original = file.filename
    ext = os.path.splitext(original)[1].lower()
    if ext not in ALLOWED_EXT:
        return jsonify(error="unsupported file type"), 400

    raw = file.read()
    if not raw:
        return jsonify(error="empty file"), 400

    # 先解析后落盘：解析失败时磁盘上不会留下任何文件
    text = parse_file(original, raw)
    if text is None:
        return jsonify(error="parse failed"), 400

    title = os.path.splitext(os.path.basename(original))[0].strip() or "未命名材料"

    class_id = session["class_id"]  # 只取自会话
    safe = secure_filename(original) or f"upload{ext}"
    dest_dir = os.path.join(current_app.config["UPLOAD_DIR"], str(class_id))
    os.makedirs(dest_dir, exist_ok=True)
    dest_path = os.path.join(dest_dir, f"{uuid.uuid4().hex}_{safe}")

    with open(dest_path, "wb") as fh:
        fh.write(raw)

    try:
        material_id = insert_material_with_knowledge(
            title=title,
            class_id=class_id,
            file_path=dest_path,
            uploaded_by=session["user_id"],
            body_text=text,
        )
    except Exception:
        # 入库失败：清理已落盘文件，避免孤立文件
        try:
            os.remove(dest_path)
        except OSError:
            pass
        return jsonify(error="storage error"), 500

    return jsonify(material_id=material_id, title=title), 201
