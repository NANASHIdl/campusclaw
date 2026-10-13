# Tasks: add-auth-rbac-class-knowledge

> 第 3 课 Apply：实现顺序 数据 → 认证 → 隔离 → 上传 → 下载 → 编排 → 验证。
> 每条任务附 `verify:`，完成后勾选。

## 1. 仓库与文档（T1）

- [ ] 1.1 移除第 2 课 Flask 实现（`app/`、`scripts/`、`run.py`、`requirements.txt`、根 `Dockerfile`） — verify: 仓库不再含 Python 应用代码
- [ ] 1.2 建立 `api/`（Go 模块）与 `web/`（Nginx + 静态 SPA）目录骨架 — verify: `cd api && go vet ./...` 无报错
- [ ] 1.3 更新 `.env.example`：`MYSQL_ROOT_PASSWORD`、`MYSQL_DATABASE`、`DB_USER`、`DB_PASSWORD`、`SESSION_SECRET`、`SEED_TEACHER_PASSWORD`、`SEED_STUDENT_PASSWORD` — verify: 列出全部必需变量名

## 2. 数据模型与种子（T2）

- [ ] 2.1 建五张表 `classes`/`users`/`sessions`/`materials`/`knowledge_entries`，`materials` 与 `knowledge_entries` 的 `class_id` 为 NOT NULL 且建索引 — verify: 启动后 `SHOW CREATE TABLE` 含 `class_id` 索引
- [ ] 2.2 种子：班级 A/B；教师 A、学生 A1/B1；A/B 班各一条可区分标题的材料；口令明文取自环境变量后计算哈希写入 — verify: `SELECT username, role, class_id FROM users` 含三账号；口令字段非明文
- [ ] 2.3 api 启动时等待 db 就绪，表已存在则跳过种子 — verify: 重复 `docker compose up` 不重复写入

## 3. 认证与会话（T3）

- [ ] 3.1 `POST /api/login`：查 `users`、比对口令哈希、签发新会话标识、写 `sessions`、`Set-Cookie`（HttpOnly/SameSite=Lax/Path=/） — verify: 登录返回 200 且响应体含 id/username/role/class，不含口令与哈希
- [ ] 3.2 登录成功换发标识：丢弃请求携带的旧标识，原标识立即作废 — verify: 持旧标识的后续请求返回 401
- [ ] 3.3 认证中间件：由 Cookie 解析会话 → 读 `users` 得角色与班级，注入请求上下文；无会话 → 401 — verify: 无 Cookie 访问 `/api/materials`、`/api/me` 均 401
- [ ] 3.4 `POST /api/logout`：删除 `sessions` 行并清 Cookie — verify: 登出后持原标识请求 401
- [ ] 3.5 `GET /api/me`：返回当前用户 id/username/role/class — verify: 仅影响前端入口渲染，不作为授权依据

## 4. 统一失败响应与限流（T4）

- [ ] 4.1 账号不存在 / 口令错误：同状态码、同响应体、耗时接近（不存在时比对固定假哈希） — verify: 两次响应体逐字节相同
- [ ] 4.2 简易限流（username + IP 滑动窗口），触发时仍返回同一失败响应（非 429） — verify: 超阈值后响应体与前述失败一致
- [ ] 4.3 真实原因仅写服务端日志 — verify: 日志含 `user_not_found`/`bad_password`/`rate_limited`，响应体不含

## 5. 按班级隔离（T5）

- [ ] 5.1 列表 `GET /api/materials` 仅用会话班级过滤；空集返回 200 — verify: 学生 A1 列表无 B 班标题
- [ ] 5.2 详情 `GET /api/materials/{id}`：先取行再校验班级，跨班与不存在返回同构 404 — verify: 教师 A 取 B 班材料 id 与取不存在 id 的响应完全一致
- [ ] 5.3 客户端自报的 `role`/`class_id`（头/查询/正文）被忽略 — verify: 篡改后结果不变

## 6. 上传与下载（T6）

- [ ] 6.1 `POST /api/materials/upload`：学生 → 403（未落盘未写库） — verify: 学生上传后 `materials` 行数不变、uploads 无新文件
- [ ] 6.2 教师上传：白名单 `.txt`/`.md` + 大小上限 → 服务端生成存储名落盘 → 两表同一事务 → 201 — verify: 两表各增 1 行且 `class_id` 为 A
- [ ] 6.3 失败无孤儿：解析失败或事务失败 → 回滚 + 删除已写文件 — verify: 上传非法扩展名/损坏文件后无孤立文件与孤立记录
- [ ] 6.4 `GET /api/materials/{id}/file`：认证 → 班级校验 → 才读文件；跨班/不存在同构 404 — verify: 未认证 401；跨班 404 且不返回文件内容

## 7. 容器编排与数据卷（T7）

- [ ] 7.1 `docker-compose.yml` 三服务；仅 `web` 映射 `8080:80`；`db` 与 `api` 不映射端口 — verify: `docker compose config` 中仅 web 有 ports
- [ ] 7.2 命名卷 `db-data`（挂 `/var/lib/mysql`）与 `uploads`（仅挂 api） — verify: Nginx 未把 uploads 配为静态目录
- [ ] 7.3 `web` 反代 `/api`、`/health` 至 api；healthcheck 走 `/health` — verify: `curl localhost:8080/health` 返回 200
- [ ] 7.4 `docker compose down` 后 `up`（不加 `-v`）数据仍在；`down -v` 才清空 — verify: 上传一条后 down/up，列表仍含该条

## 8. 验证与归档（T8）

- [ ] 8.1 Go 单元测试：会话摘要、口令哈希校验、上传扩展名白名单、统一失败响应构造 — verify: `go test ./...` 通过
- [ ] 8.2 逐条执行 spec 场景（登录换发、统一失败、401、同构 404、学生 403、两表入库、下载鉴权、/health） — verify: 记录每条通过/不通过
- [ ] 8.3 `openspec validate add-auth-rbac-class-knowledge --strict` — verify: exit 0
- [ ] 8.4 两层均通过后归档：`openspec archive add-auth-rbac-class-knowledge` — verify: change 移入 archive，规约并入 `openspec/specs/`
