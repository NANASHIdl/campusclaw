"""环境与路径：读取 .env，定位仓库根目录。

所有用例都从这里拿配置，避免各脚本各写一份读取逻辑。
"""

import os

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))


def load_env(path=None):
    """读取仓库根目录的 .env（KEY=VALUE），返回字典。"""
    path = path or os.path.join(ROOT, ".env")
    out = {}
    if not os.path.exists(path):
        return out
    with open(path, encoding="utf-8") as fh:
        for line in fh:
            line = line.strip()
            if not line or line.startswith("#") or "=" not in line:
                continue
            key, value = line.split("=", 1)
            out[key.strip()] = value.strip()
    return out


ENV = load_env()

# 默认走 web 端口；端口被占用时 .env 里会用 WEB_PORT 覆盖。
BASE_URL = os.environ.get("BASE_URL") or "http://127.0.0.1:" + ENV.get("WEB_PORT", "8080")

TEACHER = ("teacher_a", "teach123")
STUDENT = ("student_a1", "stu123")
STUDENT_B = ("student_b1", "stu123")
