# AGENTS.md —— 项目规则

> 本文件是给 AI 编码助手（Cursor / Trae / Claude Code / Codex / WorkBuddy 等）的项目级规则。

## 铁律

1. **无规约不写代码。** 任何代码变更前，必须先在 `openspec/changes/<change>/` 下完成四件套规约（`proposal.md`、`design.md`、`specs/<capability>/spec.md`、`tasks.md`）。
2. **一次变更 = 一个 change。** 规划产物只放在 `openspec/changes/<change>/` 下；归档后并入 `openspec/specs/`。
3. **每条 Scenario 必须可判定**——只凭 `spec.md` 就能判断通过或不通过。
4. **Non-goals 必须写清**——不声明"不做什么"，就禁止自行扩张范围。
5. **规约是契约**：规约未覆盖的功能不得顺带追加；范围不一致时先改规约再改代码。

## 安全底线（第 3 课）

1. **认证、授权、隔离是三件事**，且都必须在服务端完成：
   - 认证回答「你是谁」；授权回答「你能做什么」；隔离回答「你能访问哪一班的数据」。
2. **隐藏入口不等于访问控制**：页面上不渲染上传按钮，不能替代服务端的角色校验。
3. **班级只取自会话**：请求头 / 查询参数 / 请求体中自行声明的 `role`、`class_id` 一律丢弃。
4. **同构响应**：
   - 登录失败（账号不存在 / 口令错误 / 限流）对外使用相同状态码与文案，真实原因仅写日志；
   - 跨班访问与记录不存在对外返回**完全一致**的 404，真实原因仅写日志。
5. **口令**必须哈希存储（`crypto/pbkdf2`），禁止明文；**数据库连接口令与会话密钥**只放环境变量，禁止硬编码或提交到版本库。
6. **登录成功必须换发新的会话标识**（防会话固定）；会话标识在库中只存 HMAC 摘要。
7. **上传是事务性操作**：白名单校验 → 落盘 → 两表同一事务；任一步失败都要回滚并清理文件，不允许孤儿文件或孤儿记录。
8. **上传目录不是静态资源**：不得在 Nginx 中把 uploads 配置为静态目录，下载必须走受保护接口。

## 技术约定

- 架构：`web`(Nginx) + `api`(Go) + `db`(MySQL)，仅 `web` 映射宿主机端口。
- Go 服务：标准库 `net/http`（Go 1.22+ 路由模式）+ `github.com/go-sql-driver/mysql`。
- 数据模型：`classes`、`users`、`sessions`、`materials`、`knowledge_entries` 五张表；`materials` 与 `knowledge_entries` 的 `class_id` 非空且带索引。
- 启动方式：`docker compose up --build`；`down` 后 `up` 数据仍在（命名卷）。
