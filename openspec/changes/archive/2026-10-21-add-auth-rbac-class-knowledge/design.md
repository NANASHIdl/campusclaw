# Design: add-auth-rbac-class-knowledge

## Context

仓库根已有 README 三行说明与 OpenSpec change 四件套。第 2 课基线化的是"规约"；本课（第 3 课）在**同一 change** 上完成 Apply：实现可登录、可按班级隔离、可上传入库的系统。可验收行为见 `specs/auth-upload/spec.md`；问答与检索属于第 4 课，不在本 change 范围。

## Goals / Non-Goals

**Goals:**

- 三容器分层：`web`（Nginx，唯一对外端口）→ `api`（Go，信任边界）→ `db`（MySQL，不映射端口）。
- 服务端会话：Cookie 仅存随机会话标识，服务端 `sessions` 表存标识摘要；登录成功换发新标识。
- 认证、授权、班级隔离、上传校验全部在 Go 服务端完成。
- 失败响应统一：账号不存在 / 口令错误 / 限流对外同构。
- 跨班访问与记录不存在对外同构 404。
- 上传：白名单 + 大小上限 → 落盘 → 两表同一事务 → 失败回滚并清理文件。
- `docker compose up` 一键启动；`docker compose down` 后 `up` 数据仍在。

**Non-Goals:**

- 见 `proposal.md` Non-goals（检索问答、对话、作业、SSO、生产 HA 等）。
- 本课不涉及 PDF/Word/图片解析、在线编辑、切分与向量入库。

---

## 技术选型（Decision 1）

| 层次 | 选择 | 理由 |
| --- | --- | --- |
| 入口 `web` | Nginx（静态 SPA + 反代 `/api`、`/health`） | 唯一映射端口；不查会话、不访问 DB、不暴露上传目录 |
| 服务 `api` | **Go**（标准库 `net/http`，Go 1.22+ 路由模式） | 认证/授权/隔离/上传校验全部在此；单二进制、无框架依赖 |
| 数据库 `db` | **MySQL 8** | 持久化五张表；不映射宿主机端口 |
| MySQL 驱动 | `github.com/go-sql-driver/mysql` | 唯一外部依赖 |
| 口令哈希 | 标准库 `crypto/pbkdf2`（PBKDF2-HMAC-SHA256） | Go 1.24+ 进入标准库，无需引入 bcrypt |
| 会话标识 | `crypto/rand` 32 字节 → base64url；库中存 SHA-256 摘要 | 库泄露也拿不到可用标识 |

> 第 2 课的 Flask + SQLite 单容器实现已被本课架构**替换**（同一 change 的 Apply 产物以本 design 为准）。

---

## 系统架构与信任边界

```
浏览器（不可信）
   │  只访问本站 :8080
   ▼
web · Nginx ── 静态 SPA；/api、/health 反向代理 ──┐
                                                  │ 信任边界：进入 Go 后全部校验
                                                  ▼
                                          api · Go（认证 · 授权 · 隔离 · 上传校验）
                                                  │  SQL
                                                  ▼
                                          db · MySQL（不映射宿主机端口）
                                          uploads 卷（仅挂载到 api）
```

- 浏览器只与 `web` 通信；`/api` 同源，Cookie 无跨域问题。
- 上传目录仅挂载到 `api`，Nginx **不得**将其配置为静态资源目录。
- 数据库不向宿主机映射端口，客户端无法直连。

### 模块职责边界

| 模块 | 职责 | 不在职责内 |
| --- | --- | --- |
| 浏览器 | 渲染页面、登录后自动带 Cookie、提交文件到 `/api` | 不判定身份与班级；隐藏入口不构成访问拒绝 |
| Nginx | 返回静态文件；`/api`、`/health` 原样反代至 Go | 不查会话、不访问 MySQL、不暴露上传目录 |
| 认证模块 | 登录比对口令哈希并换发会话；此后每次请求由 Cookie 解析用户/角色/班级 | 不接受请求中自行声明的角色与班级 |
| 材料模块 | 按会话班级提供列表、详情、下载；教师上传写文件并更新两表 | 不执行切分、向量嵌入或问答 |
| MySQL | 持久化班级、用户、会话、材料与知识库正文 | 不映射宿主机端口 |

### 一次请求的链路（以材料列表为例）

浏览器带 Cookie → Nginx 把 `/api` 转给 Go → 认证中间件由 Cookie 查 `sessions` 得到 `user_id`，再查 `users` 得到 `role` 与 `class_id` → 材料模块只用会话班级查列表 → MySQL 返回本班行。缺少 Cookie 或会话过期时，认证中间件即返回 401，材料模块与数据库不会被访问。

---

## 认证方案：服务端会话（Decision 2）

- 采用**服务端会话**而非 JWT：状态在服务端，登出即删行立即失效；角色变更立即生效；重点防范 CSRF（同源 + SameSite）而非 XSS 读 localStorage。
- Cookie 属性：`HttpOnly`、`SameSite=Lax`、`Path=/`；`Secure` 由部署环境（HTTPS）决定，本课本地不强制。
- **登录成功必须换发会话标识**：丢弃请求携带的旧标识，写入新的 `sessions` 行，再 `Set-Cookie` 下发。原标识立即作废（防会话固定攻击）。
- `sessions` 表主键为**标识的 SHA-256 摘要**，不存原文。

### 登录流程

```
POST /api/login  {username, password}
 ├─ 规范化 username；查 users
 ├─ 无论用户是否存在，都执行一次口令哈希比对（不存在时比对固定假哈希）→ 耗时接近
 ├─ 失败（不存在 / 口令错误 / 限流）→ 统一失败响应，真实原因仅写日志
 ├─ 成功 → 生成新会话标识，DELETE 旧会话（若 Cookie 带旧标识），INSERT 新 sessions 行
 └─ Set-Cookie: cc_session=<随机标识>; HttpOnly; SameSite=Lax; Path=/
```

### 口令存储

- `users.password_hash` 存 `pbkdf2_sha256$<iter>$<salt_b64>$<hash_b64>`；禁止明文列。
- 校验使用常量时间比较（`subtle.ConstantTimeCompare`）。
- 种子口令明文由环境变量注入，启动时计算哈希写入。

---

## 统一失败响应（Decision 3）

- 三种失败原因（账号不存在 / 口令错误 / 触发限流）返回**相同状态码 401、相同 JSON 文案**，响应耗时接近。
- 限流：进程内简易计数器（按 `username` + 客户端 IP，滑动窗口）。触发限流时**不返回 429**，仍返回同一失败响应；真实原因记日志。
- 真实原因（`user_not_found` / `bad_password` / `rate_limited`）只写服务端日志。

---

## 数据模型与租户列（Decision 4）

五张表；`materials` 与 `knowledge_entries` 携带租户列 `class_id`（NOT NULL + 索引）。

```sql
classes(id PK, name UNIQUE)
users(id PK, username UNIQUE, password_hash, role ENUM('teacher','student'), class_id FK→classes)
sessions(id CHAR(64) PK, user_id FK→users, expires_at, created_at, INDEX(user_id))
materials(id PK, title, class_id FK→classes, file_path, uploader_id FK→users, created_at, INDEX(class_id))
knowledge_entries(id PK, material_id FK→materials, class_id FK→classes, body_text, created_at, INDEX(class_id))
```

### 各模块的写入边界

| 模块 | 可写表 |
| --- | --- |
| 认证模块 | 登录：写 `sessions`；请求：读 `sessions`、读 `users` |
| 上传模块 | 教师校验通过后，同一事务写 `materials` + `knowledge_entries`（同一班级值） |

班级**不是**上传表单字段：由认证模块从 `users` 行读出，再由上传模块原样写入两张带租户列的表。

---

## 按班级隔离（Decision 5）

隔离在服务端实现，前端隐藏按钮不算满足。两条路径机制不同：

### 列表（集合查询）

- `SELECT ... FROM materials WHERE class_id = :session_class_id ORDER BY ...`
- 空结果集仍返回 200。

### 详情 / 下载（对象路径）

1. 按标识取行（不带班级条件，以便区分"不存在"与"越权"用于日志）。
2. 行不存在 **或** `row.class_id != session_class_id` → 返回**同构 404**（状态码与响应体完全一致）。
3. 下载必须**在读取文件字节之前**完成上述校验。
4. 真实原因（`not_found` / `cross_class`）仅写日志。

> 与第 2 课规约的差异：第 2 课曾采用"跨班 403"，本课按课件改为**同构 404**，避免以状态码泄露资源是否存在。spec 已同步更新。

---

## 上传入库流程（Decision 6）

```
POST /api/materials/upload  (multipart/form-data)
 ├─ 认证中间件：无会话 → 401
 ├─ 角色校验：非 teacher → 403（此时未落盘、未写库）
 ├─ 白名单：扩展名 ∈ {.txt,.md}；大小 ≤ MAX_UPLOAD_BYTES
 ├─ 服务端生成存储名：<uuid>_<safe>，写入 uploads/<class_id>/
 ├─ 解析正文（失败 → 删文件、返回 400、无 DB 行）
 ├─ BEGIN；INSERT materials；INSERT knowledge_entries；COMMIT
 └─ 任一步骤失败：ROLLBACK + 删除已写文件 → 无孤儿
```

- 存储名由服务端生成，不使用客户端原始文件名作为路径。
- 返回 201 与 `material_id`。

---

## 容器编排与数据卷（Decision 7）

```
services:
  web : nginx, ports ["${WEB_PORT:-8080}:80"], depends_on api
  api : go 服务, 不映射端口, volumes [uploads:/app/uploads], env_file .env
  db  : mysql:8, 不映射端口, volumes [db-data:/var/lib/mysql]
volumes:
  db-data   # down 保留；down -v 才删除
  uploads   # 仅挂载到 api
```

- 仅 `web` 发布宿主机端口（默认 8080，可用 `WEB_PORT` 覆盖）；`api` 与 `db` 仅存在于容器网络内部。
- `docker compose down` 后再 `up`：容器重建但命名卷保留 → 账号与材料仍在。
- `docker compose down -v` 删除数据卷 → 数据清空（仅用于主动重置）。
- `api` 启动时等待 `db` 就绪，建表并（仅当为空时）写入种子。
- healthcheck：`web` 通过 `GET /health` 探活（Nginx 反代至 api）。

---

## Decisions（摘要）

| # | 决策 | 备选 |
| --- | --- | --- |
| 1 | web(Nginx) + api(Go) + db(MySQL) | 单容器 Flask+SQLite（第 2 课实现，已替换） |
| 2 | 服务端会话（sessions 表 + 标识摘要） | JWT（登出无法立即失效，本课不采用） |
| 3 | 统一失败响应（含限流同构） | 区分 401/429（会泄露账号存在性） |
| 4 | 跨班与不存在同构 404 | 跨班 403（会泄露资源存在性） |
| 5 | 标准库 pbkdf2 口令哈希 | bcrypt / argon2 |
| 6 | 命名卷持久化 | 绑定宿主机目录（`down -v` 不会清空，与课件不符） |
| 7 | 依赖 `go mod vendor` 进仓库，镜像构建离线（`GOPROXY=off`） | 构建时访问 Go 代理（容器内常不可达） |

## Risks / Trade-offs

- [进程内限流不跨副本] → 本课单副本演示环境足够；多副本需引入共享存储，属生产 HA，Non-goal。
- [同构 404 增加排障难度] → 真实原因写入服务端日志。
- [MySQL 首启较慢] → api 启动重试连接并等待就绪。
- [容器内无法访问 Go 模块代理] → 依赖已 vendor 进仓库，构建不再需要网络；新增依赖时需重新执行 `go mod vendor`。

## Migration Plan

第 2 课的 Flask + SQLite 实现被本 design 替换：删除 `app/`、`scripts/`、`run.py`、`requirements.txt` 与根 `Dockerfile`，新增 `api/`（Go）与 `web/`（Nginx）。数据从零由 `api` 启动时建表与种子，无旧数据需要迁移。

## Open Questions

（无。）
