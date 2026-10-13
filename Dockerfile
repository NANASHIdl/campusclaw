FROM python:3.12-slim

ENV PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1

WORKDIR /app

# healthcheck 需要 curl
RUN apt-get update \
    && apt-get install -y --no-install-recommends curl \
    && rm -rf /var/lib/apt/lists/*

COPY requirements.txt ./
RUN pip install --no-cache-dir -r requirements.txt

COPY app/ ./app/
COPY scripts/ ./scripts/
COPY run.py ./
COPY openspec/ ./openspec/

EXPOSE 8080

# 启动时若库为空则写入种子数据（init_db.py 幂等），再用 gunicorn 提供服务
CMD ["sh", "-c", "python scripts/init_db.py && gunicorn --bind 0.0.0.0:8080 --workers 2 --access-logfile - run:app"]
