# CampusClaw

- **价值**：面向中小学的教研智能体，把分散在群文件与网盘中的教研材料收进可检索、可复用的知识库。
- **场景**：教师登录后上传并管理本班教学材料，学生只读查看本班材料；班级是数据边界。
- **不做**：不做检索问答、AI 对话、作业提交与批改、注册与找回密码、SSO/生产级高可用。

## 技术栈（最轻量方案）

- **Web**：Flask 3.x（Jinja2 模板做服务端渲染，werkzeug 提供密码哈希与会话签名）
- **数据库**：SQLite（Python 标准库 `sqlite3`，零额外驱动）
- **密码**：`werkzeug.security`（哈希存储，随 Flask 自带）
- **运行**：本地 `flask run`；生产/演示用 Docker Compose + gunicorn

> 运行时依赖只有 Flask 一个（gunicorn 仅用于容器内生产入口）。选它是因为课件 design.md 的首选即 Flask + SQLite，备选 Node + Express 依赖更多。

## 本地运行

```bash
python -m venv .venv
. .venv/bin/activate          # Windows: .venv\Scripts\activate
pip install -r requirements.txt

cp .env.example .env          # 填入 SECRET_KEY
python scripts/init_db.py     # 建表 + 写入种子数据
python run.py                 # 或 flask --app run run
```

打开 <http://127.0.0.1:5000/login>。

### 预置账号

| 用户名 | 密码 | 角色 | 班级 |
|---|---|---|---|
| `teacher_a` | `teach123` | teacher | A |
| `student_a1` | `stu123` | student | A |
| `student_b1` | `stu123` | student | B |

## Docker Compose 运行

```bash
cp .env.example .env          # 填 SECRET_KEY
docker compose up --build
```

打开 <http://localhost:8080/login>。健康检查：`curl http://localhost:8080/health`。

## 路由一览

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/` | 未登录跳 `/login`，已登录跳 `/materials` |
| GET/POST | `/login` | 登录页与登录处理 |
| GET/POST | `/logout` | 登出 |
| GET | `/materials` | 本班材料列表（页面，需登录） |
| GET | `/api/materials` | 本班材料列表（JSON，未登录 401） |
| GET | `/api/materials/<id>` | 单条材料（跨班 403） |
| POST | `/api/materials/upload` | 教师上传（学生 403） |
| GET | `/health` | 健康检查，无需登录，返回 `{"status":"ok"}` |

> **约定**：跨班访问统一返回 **403**（不使用 404 混淆），详见 `openspec/changes/add-auth-rbac-class-knowledge/design.md`。

## 规约（SDD）

本项目遵循规约驱动开发，规约在 `openspec/` 下：

- `openspec/specs/` —— 长期真相源（归档后并入）
- `openspec/changes/<change>/` —— 每次变更的 proposal / design / spec / tasks 四件套

当前变更：`openspec/changes/add-auth-rbac-class-knowledge/`。
