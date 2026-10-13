# CampusClaw

- **价值**：面向中小学的教研智能体，把分散在群文件与网盘中的教研材料收进可检索、可复用的知识库。
- **场景**：教师登录后上传并管理本班教学材料，学生只读查看与下载本班材料；班级是数据边界。
- **不做**：不做检索问答、AI 对话、作业提交与批改、注册与找回密码、PDF/Word 解析、SSO/生产级高可用。

## 架构（第 3 课）

```
浏览器 ──只访问 :8080──▶ web · Nginx ──/api、/health 反代──▶ api · Go ──SQL──▶ db · MySQL
                         静态 SPA                            认证·授权·隔离·上传校验      不映射宿主机端口
                                                             uploads 卷（仅挂 api）
```

| 服务 | 技术 | 对外端口 | 职责 |
| --- | --- | --- | --- |
| `web` | Nginx + 静态 SPA | **8080:80（唯一）** | 返回静态页面；把 `/api`、`/health` 原样反代给 api；**不**参与鉴权，**不**暴露上传目录 |
| `api` | Go（标准库 `net/http`） | 不映射 | 认证、授权、班级隔离、上传校验、下载鉴权 |
| `db` | MySQL 8 | 不映射 | 持久化五张表；客户端无法直连 |

**信任边界在 Go**：进入 api 之后的每一次请求都要重新核对会话、角色与班级。

## 快速开始（Docker Compose）

```bash
cp .env.example .env      # 填写 MYSQL_* / DB_* / SESSION_SECRET
docker compose up --build
```

打开 <http://localhost:8080>。健康检查：`curl http://localhost:8080/health`。

> 若 8080 已被占用，在 `.env` 中设置 `WEB_PORT=8081` 后重新 `docker compose up -d`，访问端口随之改变。

### 预置账号

| 用户名 | 口令 | 角色 | 班级 |
| --- | --- | --- | --- |
| `teacher_a` | `teach123` | teacher | A |
| `student_a1` | `stu123` | student | A |
| `student_b1` | `stu123` | student | B |

> 口令明文只出现在 `.env` 中，启动时计算哈希写入数据库；库中不保存明文。

### 数据卷

```bash
docker compose down       # 容器删除，数据卷保留 → 账号与材料仍在
docker compose up -d
docker compose down -v    # 删除数据卷 → 数据清空（仅用于主动重置）
```

## API

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| POST | `/api/login` | 登录；成功返回身份并 `Set-Cookie`（仅随机会话标识） |
| POST | `/api/logout` | 登出，删除服务端会话 |
| GET | `/api/me` | 当前用户；仅用于前端决定是否渲染上传入口 |
| GET | `/api/materials` | 本班材料列表（按会话班级过滤） |
| GET | `/api/materials/{id}` | 材料详情 |
| GET | `/api/materials/{id}/file` | 下载（读取文件前完成认证与班级校验） |
| POST | `/api/materials/upload` | 教师上传（学生 403） |
| GET | `/health` | 存活探测，无需认证 |

**响应约定**

- 未认证 → `401 {"error":"unauthorized"}`
- 登录失败（账号不存在 / 口令错误 / 限流）→ `401 {"error":"invalid_credentials"}`，三者**同构**
- 角色不足（学生上传）→ `403 {"error":"forbidden"}`
- 跨班访问与记录不存在 → **同构** `404 {"error":"not_found"}`

## 本地开发（不使用 Docker）

```bash
# 需要本地 MySQL；先导出环境变量（或使用 .env 后 export）
export DB_HOST=127.0.0.1 DB_PORT=3306 DB_NAME=campusclaw DB_USER=... DB_PASSWORD=...
export SESSION_SECRET=... SEED_TEACHER_PASSWORD=teach123 SEED_STUDENT_PASSWORD=stu123
export UPLOAD_DIR=./uploads

cd api && go run ./cmd/server      # 监听 :8080
go test ./...                      # 单元测试
```

## 验收

两层验证都要通过才算实现合规：

```bash
# 第一层：规约结构
openspec validate add-auth-rbac-class-knowledge --strict

# 第二层：逐条执行 spec 场景（需栈已启动）
python scripts/verify_e2e.py                       # 默认 http://127.0.0.1:8080
BASE_URL=http://127.0.0.1:8081 python scripts/verify_e2e.py   # 自定义端口

# 单元测试
cd api && go test ./...
```

`scripts/verify_e2e.py` 覆盖：健康检查、未认证 401、统一失败响应、会话换发（防会话固定）、
班级隔离与同构 404、自报身份无效、学生上传 403、教师上传入库、白名单拒绝与无孤儿、
下载鉴权、登出失效等 45 项断言。

## 目录结构

```
api/                      Go 服务
  cmd/server/main.go      路由装配与启动
  internal/config         环境变量配置（缺失必需项即启动失败）
  internal/db             MySQL 连接、建表、种子
  internal/auth           口令哈希、会话、认证中间件、统一失败响应、限流
  internal/materials      列表、详情、下载、上传（含班级隔离与事务）
  internal/httpx          统一 JSON 响应与错误文案
web/                      Nginx + 静态 SPA
  conf/nginx.conf         静态服务 + /api 反代
  html/                   index.html / app.js / style.css
openspec/                 规约（长期真相源 + 每次变更的四件套）
docker-compose.yml        web / api / db 三服务与命名卷
```

## 规约（SDD）

- `openspec/specs/` —— 长期真相源（归档后并入）
- `openspec/changes/<change>/` —— 每次变更的 proposal / design / spec / tasks 四件套

当前变更：`openspec/changes/add-auth-rbac-class-knowledge/`（capability：`auth-upload`）。

校验：`openspec validate add-auth-rbac-class-knowledge --strict`
归档：两层验证（结构 + 逐条场景）都通过后 `openspec archive add-auth-rbac-class-knowledge`
