"""本地运行入口。

本地：python run.py            → http://127.0.0.1:5000
容器：gunicorn --bind 0.0.0.0:8080 run:app
"""

import os

from app.env import load_env

load_env()

from app import create_app  # noqa: E402

app = create_app()

if __name__ == "__main__":
    app.run(
        host=os.environ.get("HOST", "127.0.0.1"),
        port=int(os.environ.get("PORT", "5000")),
        debug=os.environ.get("FLASK_DEBUG", "0") == "1",
    )
