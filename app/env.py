"""极简 .env 加载器（仅用标准库），避免引入 python-dotenv 依赖。

行为：逐行解析 KEY=VALUE，仅在环境变量尚未设置时写入（不覆盖已存在的值）。
"""

import os


def load_env(path=".env"):
    if not os.path.exists(path):
        return
    with open(path, encoding="utf-8") as fh:
        for line in fh:
            line = line.strip()
            if not line or line.startswith("#") or "=" not in line:
                continue
            key, _, value = line.partition("=")
            key = key.strip()
            value = value.strip().strip('"').strip("'")
            if key:
                os.environ.setdefault(key, value)
