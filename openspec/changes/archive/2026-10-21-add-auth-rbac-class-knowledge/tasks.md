# Tasks: add-auth-rbac-class-knowledge

> 第 3 课 Apply：实现顺序 数据 → 认证 → 隔离 → 上传 → 下载 → 编排 → 验证。
> 每条任务附 `verify:`，完成后勾选。

## 1. 仓库与文档（T1）

- [x] 1.1 移除第 2 课 Flask 实现（`app/`、`scripts/`、`run.py`、`requirements.txt`、根 `Dockerfile`） — verify: 仓库不再含 Python 应用代码
- [x] 1.2 建立 `api/`（Go 模块）与 `web/`（Nginx + 静态 SPA）目录骨架 — verify: `cd api && go vet ./...` 无报错
- [x] 1.3 更新 `.env.example`：`MYSQL_ROOT_PASSWORD`、`DB_*`、`SESSION_SECRET`、`SEED_*`、`WEB_PORT` 等 — verify: 列出全部必需变量名

## 2. 数据模型与种子（T2）

- [x] 2.1 建五张表 `classes`/`users`/`sessions`/`materials`/`knowledge_entries`，`materials` 与 `knowledge_entries` 的 `class_id` 为 NOT NULL 且建索引 — verify: `SHOW TABLES` 返回 5 张表；`SHOW INDEX` 含 `idx_materials_class`、`idx_ke_class`
- [x] 2.2 种子：班级 A/B；教师 A、学生 A1/B1；A/B 班各一条可区分标题的材料；口令明文取自环境变量后计算哈希写入 — verify: 教师 A 列表仅含「A 班 · 一元二次方程讲义」；库中口令字段为 `pbkdf2_sha256$...`
- [x] 2.3 api 启动时等待 db 就绪，表已存在则跳过种子 — verify: 重复 `up` 日志输出「库中已有 3 个用户，跳过种子写入」

## 3. 认证与会话（T3）

- [x] 3.1 `POST /api/login`：查 `users`、比对口令哈希、签发新会话标识、写 `sessions`、`Set-Cookie` — verify: 200 且响应体为 `{id,username,role,class_id}`，不含口令与哈希
- [x] 3.2 登录成功换发标识：丢弃请求携带的旧标识，原标识立即作废 — verify: 预置的旧标识请求 `/api/materials` 返回 401
- [x] 3.3 认证中间件：由 Cookie 解析会话 → 读 `users` 得角色与班级；无会话 → 401 — verify: 未登录访问 `/api/materials`、`/api/me`、上传、下载均 401
- [x] 3.4 `POST /api/logout`：删除 `sessions` 行并清 Cookie — verify: 登出后持原标识请求 401
- [x] 3.5 `GET /api/me`：返回当前用户 — verify: 未登录 401；登录后返回 id/username/role/class_id

## 4. 统一失败响应与限流（T4）

- [x] 4.1 账号不存在 / 口令错误：同状态码、同响应体 — verify: 两者均为 `401 {"error":"invalid_credentials"}`
- [x] 4.2 简易限流，触发时仍返回同一失败响应（非 429） — verify: 连续 14 次错误登录的状态码集合与响应体集合均为单元素
- [x] 4.3 真实原因仅写服务端日志 — verify: 日志含 `reason=user_not_found` / `reason=bad_password` / `reason=rate_limited`

## 5. 按班级隔离（T5）

- [x] 5.1 列表仅用会话班级过滤；空集返回 200 — verify: 教师 A 列表无「B 班」标题；学生 B1 列表无「A 班」标题
- [x] 5.2 详情先取行再校验班级，跨班与不存在返回同构 404 — verify: 两者状态码与响应体完全一致
- [x] 5.3 客户端自报的 `role`/`class_id`（头/查询/正文）被忽略 — verify: 附加 `class_id=2&role=teacher` 后列表结果逐字节不变

## 6. 上传与下载（T6）

- [x] 6.1 学生上传 → 403（未落盘未写库） — verify: 403 且材料数不变
- [x] 6.2 教师上传：白名单 + 大小上限 → 服务端生成存储名落盘 → 两表同一事务 → 201 — verify: 返回 `material_id`，两表各增 1 行且 `class_id` 为 1
- [x] 6.3 失败无孤儿 — verify: 上传 `.pdf` 与空 `.md` 均 400；`/app/uploads` 内仅 2 个种子文件 + 1 个成功上传文件
- [x] 6.4 下载鉴权：认证 → 班级校验 → 才读文件；跨班/不存在同构 404 — verify: 未认证 401；跨班与不存在响应一致且不含文件内容

## 7. 容器编排与数据卷（T7）

- [x] 7.1 三服务；仅 `web` 映射端口；`db` 与 `api` 不映射 — verify: `docker compose port web 80` 返回 `0.0.0.0:8081`；api/db 无映射
- [x] 7.2 命名卷 `db-data` 与 `uploads`（仅挂 api） — verify: `docker volume ls` 含两个卷；`/uploads/` 经 Nginx 返回 404
- [x] 7.3 `web` 反代 `/api`、`/health`；healthcheck 走 `/health` — verify: `curl :8081/health` 返回 200 `{"status":"ok"}`
- [x] 7.4 `down` 后 `up`（不加 `-v`）数据仍在 — verify: 两轮 down/up 后「三角函数讲义」仍在本班列表

## 8. 验证与归档（T8）

- [x] 8.1 Go 单元测试：口令哈希、会话摘要、限流、上传解析与存储名清洗 — verify: `go test ./...` 全部 ok
- [x] 8.2 逐条执行 spec 场景 — verify: 端到端脚本 45 项断言全部 PASS（见下）
- [x] 8.3 `openspec validate add-auth-rbac-class-knowledge --strict` — verify: exit 0，输出 `Change ... is valid`
- [ ] 8.4 两层均通过后归档：`openspec archive add-auth-rbac-class-knowledge` — verify: change 移入 archive，规约并入 `openspec/specs/`。**待课程确认后执行**（归档后本 change 不再接受修改）

## 验收记录（8.2）

环境：Docker Compose 三容器（web 8081→80 / api / db，仅 web 映射端口），MySQL 8.0，Go 1.24 静态二进制。

| 分组 | 断言数 | 结果 |
| --- | --- | --- |
| 健康检查与未认证访问（401） | 6 | PASS |
| 统一失败响应（账号不存在 / 口令错误同构） | 2 | PASS |
| 教师登录、会话 Cookie 属性、响应体不含口令 | 8 | PASS |
| 会话固定防御：换发标识 + 旧标识失效 | 2 | PASS |
| 班级隔离：列表过滤、同构 404、自报身份无效 | 6 | PASS |
| 学生上传 403、教师上传 201、两表入库、列表可见 | 8 | PASS |
| 白名单拒绝与无孤儿 | 3 | PASS |
| 下载鉴权（401 / 200 / 同构 404 / 不泄露内容） | 6 | PASS |
| 登出后会话立即失效 | 2 | PASS |
| 限流同构（补充检查） | 3 | PASS |
| 上传目录非静态资源（补充检查） | 3 | PASS |
| 端口隔离、五张表与租户列索引（补充检查） | — | PASS |
| `down` → `up` 数据持久化 | 2 轮 | PASS |

**合计 45 项端到端断言全部 PASS，0 FAIL。**
