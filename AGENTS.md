# AGENTS.md —— 项目规则

> 本文件是给 AI 编码助手（Cursor / Trae / Claude Code / Codex / WorkBuddy 等）的项目级规则。

## 铁律

1. **无规约不写代码。** 任何代码变更前，必须先在 `openspec/changes/<change>/` 下完成四件套规约（`proposal.md`、`design.md`、`specs/<capability>/spec.md`、`tasks.md`）。
2. **一次变更 = 一个 change。** 规划产物只放在 `openspec/changes/<change>/` 下；归档后并入 `openspec/specs/`。
3. **每条 Scenario 必须可判定**——只凭 `spec.md` 就能判断通过或不通过。
4. **Non-goals 必须写清**——不声明"不做什么"，就禁止自行扩张范围。
5. **安全底线**：密码必须哈希存储，禁止明文；密钥只放服务端环境变量，禁止硬编码或提交到版本库。
6. **班级是数据边界**：所有业务查询必须在服务端按会话中的班级过滤；前端隐藏按钮不算满足隔离要求。

## 技术约定

- 技术栈：Python + Flask + SQLite（标准库 `sqlite3`），服务端渲染。
- 密码哈希：`werkzeug.security`（PBKDF2-SHA256）。
- 启动方式：本地 `flask run`；标准部署为 Docker Compose，应用提供 `GET /health`。
- 跨班访问统一返回 `403`。
