# Tasks: add-mcp-tool-integration

> 第 6 课 Apply：实现顺序 工具服务 → 数据与仓储 → 管理接口 → 引用校验 → 调用循环 → 可见性与记录 → 文档 → 前端 → 测试 → 验证。
> 每条任务附 `verify:`，勾选发生在 Apply 阶段，初稿全为 `[ ]`。

## 1. 工具服务 `mcp-server/`（T1）

- [x] 1.1 新建独立 Go 模块 `mcp-server/`（仅标准库），实现 `POST /mcp` 单入口与 `GET /health` — verify: `cd mcp-server && go build ./...` 通过；`curl /health` 返回 ok
- [x] 1.2 JSON-RPC 2.0 三个方法：`initialize`（返回 `protocolVersion`/`capabilities.tools`/`serverInfo`）、`tools/list`、`tools/call` — verify: 三个方法各有成功响应
- [x] 1.3 错误码：`-32700` 解析错误、`-32600` 请求非法、`-32601` 方法不存在、`-32602` 参数非法，错误文案可读 — verify: 四种非法输入各返回对应 code 且 HTTP 200
- [x] 1.4 令牌校验：`MCP_AUTH_TOKEN` 环境变量；无令牌/错令牌 HTTP 401 + JSON-RPC 错误；`/health` 免令牌 — verify: 三种情形状态码正确；响应与日志不含令牌
- [x] 1.5 工具 `formula_hint`：名称、说明（含适用/不适用）、`inputSchema`（`topic` 必填、`grade` 枚举） — verify: `tools/list` 中三项齐全
- [x] 1.6 工具 `unit_convert`：一步换算，支持长度/质量/时间/温度；不同物理量互转返回 `isError` — verify: 3 千米→3000 米；米→千克报错
- [x] 1.7 工具返回 `content` 数组 + `isError`；未知主题/单位给可读原因，不编造数值；返回文本不含指令式句式 — verify: 三类异常返回可读错误
- [x] 1.8 Dockerfile（多阶段、`GOPROXY=off`）+ compose 增加 `mcp-demo` 服务，不映射宿主机端口 — verify: `docker compose up -d mcp-demo` 后 `api` 容器内可访问 `http://mcp-demo:8090/health`

## 2. 数据模型与仓储（T2）

- [x] 2.1 建表 `mcp_servers`（`UNIQUE(class_id,name)`）、`mcp_tools`（`UNIQUE(server_id,name)`、级联删除）、`mcp_tool_calls`（无外键，保留历史） — verify: `SHOW TABLES` 三张表齐备；重复启动不报错
- [x] 2.2 仓储：服务器 CRUD（按 `class_id` 过滤）、工具清单整体替换（事务内删旧写新）、调用记录插入与按班倒序查询（默认 50 条） — verify: 单元测试覆盖替换语义与条数上限
- [x] 2.3 响应结构体不含 `auth_token` 字段，只暴露 `has_auth` — verify: 单元测试断言序列化结果无令牌字段

## 3. 登记、连接、发现、断开（T3）

- [x] 3.1 `GET/POST /api/mcp/servers`：教师专属；名称 1–32 去空白、`base_url` 非空且以 `http://`/`https://` 开头；同名 400；登记后 `connected=false`、`tool_count=0` — verify: 201/400/403 状态码正确，响应无令牌
- [x] 3.2 `PUT/DELETE /api/mcp/servers/{id}`：令牌留空表示不改；改地址或令牌后清空连接状态与工具清单；删除返回 204，重复删除 404 — verify: 四种情形状态码正确
- [x] 3.3 `POST /api/mcp/servers/{id}/connect`：先 `initialize` 再 `tools/list`，两步都成功才在事务内替换清单并置 `connected=true`；失败停在未连接并写 `last_error` — verify: 成功/地址错/令牌错三种情形
- [x] 3.4 连续连接两次清单条数不翻倍 — verify: `tool_count` 两次相同，工具名无重复
- [x] 3.5 `POST /api/mcp/servers/{id}/disconnect`：清空清单、置未连接；重复断开幂等 — verify: 两次均 200，清单为空
- [x] 3.6 `GET /api/mcp/servers/{id}/tools`：返回最近一次发现清单；未连接时为空数组 — verify: 连接前空、连接后非空
- [x] 3.7 他班/不存在服务器同构 404（列表、改、删、连接、断开、工具六处） — verify: 响应逐字节一致
- [x] 3.8 连接失败不影响检索与对话主路径 — verify: 地址写错后仍能正常提问

## 4. 引用格式与保存前校验（T4）

- [x] 4.1 引用解析：`^[a-z][a-z0-9_]*@[a-z0-9][a-z0-9-]*$`；内置项 `knowledge_search` 单独放行 — verify: 单元测试覆盖合法/大写/连字符/缺服务器名四种
- [x] 4.2 保存技能时四类校验：格式 → 服务器已登记 → 已连接 → 工具在清单内；失败 400 且文案可读 — verify: 每种失败各有明确提示
- [x] 4.3 校验失败不落库 — verify: 技能总数不变
- [x] 4.4 `skill.Handler` 通过接口注入校验器，未注入时只允许内置项（避免包循环） — verify: `go build ./...` 通过；未注入时引用被拒

## 5. 调用循环与结果回传（T5）

- [x] 5.1 `qa` 增加带工具的流式请求：请求体含 `tools`，解析 `delta.tool_calls`（按 `index` 累积 `id`/`name`/`arguments`）与 `finish_reason`，忽略 `reasoning_content` — verify: 单元测试用固定分片断言累积结果
- [x] 5.2 候选集：仅取命中技能引用的工具；无引用则不发 `tools` — verify: 两种技能分别发请求，抓请求体断言
- [x] 5.3 参数校验：按 `input_schema` 校验 `required`/`type`/`enum`；不通过不执行并回传原因 — verify: 缺必填、类型错两种情形
- [x] 5.4 执行：带令牌 `tools/call`，超时 `MCP_TIMEOUT_SECONDS`；失败作为工具消息回传 — verify: 超时与 `isError` 两种情形对话继续
- [x] 5.5 结果清洗：命中指令式句式整条丢弃并替换固定说明；超 1200 字截断；加来源标注 — verify: 单元测试覆盖丢弃与截断
- [x] 5.6 循环与上限：`MCP_MAX_ROUNDS`（默认 3）、`MCP_MAX_CALLS`（默认 4）；超限后追加「已到调用上限」说明并再发一次**不带 tools** 的请求 — verify: 调小上限后调用次数受限且回答非空、无 error 事件
- [x] 5.7 工具消息落在用户侧，不进系统消息 — verify: 单元测试断言系统消息不含工具正文

## 6. 流式可见与调用记录（T6）

- [x] 6.1 `meta` 增加 `tool_calls`（`ref`/`status`/`text`），无调用时省略该字段 — verify: 有/无调用两种情形
- [x] 6.2 闸门：候选集为空时 `meta` 在生成前发出（第 5 课行为不变）；有候选集时先跑循环、再发 `meta`、再发正文 — verify: 两种技能的 SSE 事件序列
- [x] 6.3 调用记录：`ok`/`denied`/`error` 三态；含 `tool_ref`、`server_name`、`tool_name`、`user_id`、`conversation_id`、`round_no`、`detail`、`created_at` — verify: 三种情形各留一条
- [x] 6.4 无法归属不记录：服务器被改名或删除后，该引用记 `denied` 且不写记录 — verify: 记录条数不变
- [x] 6.5 `GET /api/mcp/tool-calls`：仅教师；只返回本班；按时间倒序；默认 50 条 — verify: 学生 403；跨班记录不可见
- [x] 6.6 日志：每次调用一行，含引用、状态、轮次与耗时，不含令牌 — verify: `docker compose logs api` 抽查

## 7. 接口文档（T7）

- [x] 7.1 8 个新处理函数补齐 swaggo 注解（`@ID`/`@Summary`/`@Tags`/`@Security`/`@Param`/`@Success`/`@Failure`/`@Router`） — verify: `swag init` 后 `paths` 覆盖新路由
- [x] 7.2 路由表加入 8 条；启动自检「注解集合 = 注册路由集合」通过 — verify: 服务正常启动；人为删一条注解启动失败
- [x] 7.3 Swagger 页面能试调登记、连接、工具清单、技能保存 — verify: 页面内执行返回 2xx

## 8. 前端（T8）

- [x] 8.1 新增「工具服务」页签（仅教师可见）：登记表单（令牌为密码框、提交后清空）、服务器列表（名称/地址/是否配令牌/是否连接/工具数/最近错误） — verify: 教师可登记并看到列表；学生看不到该页签
- [x] 8.2 行内操作：连接并发现、断开、查看工具、编辑、删除 — verify: 每个操作后列表状态正确刷新
- [x] 8.3 「最近调用」表：时间、工具引用、状态、用户、轮次 — verify: 三种状态各出现一条
- [ ] 8.4 对话页：`meta.tool_calls` 到达时在回答上方显示工具徽标（含状态），与引用徽标同层级 — verify: 触发工具后回答前即可看到（**未验：当日模型额度用尽，见验收记录**）

## 9. 测试工程（T9）

- [x] 9.1 `apitest/` 新增本课用例：登记、连接、列出工具、断开、非教师被拒 — verify: 各脚本可单独运行
- [x] 9.2 端到端用例：连接自建服务 → 保存引用 `formula_hint@…` 的技能 → 会话提问 → 记录出现一条成功 — verify: 断言含响应内容与记录回查
- [x] 9.3 `run_all.py` 全绿并出报告 — verify: 退出码 0
- [x] 9.4 `scripts/verify_mcp.py`：覆盖 `mcp-server` 与 `mcp-tools` 两个 capability 的 Scenario — verify: 全部断言 PASS

## 10. 验收与归档（T10）

- [x] 10.1 Go 单元测试：引用解析、参数校验、tool_calls 累积、结果清洗、上限 — verify: `go test ./...` 全部 ok
- [x] 10.2 端到端：服务单独可验（health / tools/list / tools/call）+ 宿主全链路 — verify: `verify_mcp.py` 全 PASS
- [ ] 10.3 前四课回归 — verify: `verify_e2e.py`、`verify_retrieval.py`、`verify_agent_chat.py`、`verify_frontend.mjs` 全 PASS（**第 3、4 课已过；第 5 课与前端点验待模型额度恢复后重跑，见验收记录**）
- [x] 10.4 `openspec validate add-mcp-tool-integration --strict` — verify: exit 0
- [x] 10.5 令牌不外泄：前端代码、镜像、日志、接口响应都搜不到令牌 — verify: 四处各查一次
- [ ] 10.6 归档：`openspec archive add-mcp-tool-integration` — verify: 并入 `specs/{mcp-server,mcp-tools}/` 并更新 `specs/agent-chat/`。**待课程确认后执行**

## 验收记录（10.2）

执行环境：Windows + Docker Desktop，`WEB_PORT=8081`，五容器（web/api/db/qdrant/mcp-demo）全部 Up；
对话与嵌入均接课程网关。

### 端到端验收

| 脚本 | 结果 |
| --- | --- |
| `BASE_URL=http://127.0.0.1:8081 python scripts/verify_mcp.py` | **PASS=91 FAIL=0** |
| `python apitest/run_all.py` | **12 个用例、181 条断言全通过** |
| `cd api && go test ./...` | 11 个包全绿（新增 mcp 包与工具相关用例） |
| `cd mcp-server && go test ./...` | 工具服务单测全绿 |
| `openspec validate add-mcp-tool-integration --strict` | valid |

`verify_mcp.py` 分 8 节：

| 节 | 覆盖 |
| --- | --- |
| 1 | 工具服务单独可验：health 免令牌、initialize 能力协商、tools/list 两项声明、tools/call 成功与两类 `isError`、无令牌/错令牌 401、四种 JSON-RPC 错误码 |
| 2 | 登记与令牌边界：201/400/403/401、响应无令牌与无 `auth_token` 字段、同名拒绝 |
| 3 | 连接与发现：连接后可列出 `formula_hint`/`unit_convert`、连续连接清单不翻倍、库中条数一致 |
| 4 | 引用校验：大写/连字符/缺服务器名/未登记/不在清单五种拒绝，失败不落库 |
| 5 | 调用循环：meta 带 `tool_calls` 且先于正文、成功记录含五个问题的答案、两步换算两轮调用且轮次递增 |
| 6 | 断开与无法归属：断开后调用记 `denied`、删除服务器后记 `denied` 但不写记录 |
| 7 | 记录查询：学生 403、教师可见、倒序、≤50 条、三种状态齐全 |
| 8 | 令牌不外泄：接口响应、前端 JS/HTML、调用记录里都搜不到令牌 |

### 前端点验（真实 Chrome + CDP，11 条断言全通过）

教师登录 → 「工具服务」页签可见 → 列表显示已登记服务器 → 行内「连接并发现」提示
「已连接，发现 2 个工具」→ 工具数列变 2 → 「查看工具」列出 `formula_hint`/`unit_convert`
→ 「断开」后状态列变「未连接」→ 最近调用表有 9 条记录；学生登录看不到该页签。

### 两处实现细节（值得记一笔）

1. **`inputSchema` 是驼峰**：MCP 线上字段叫 `inputSchema`，库里的列叫 `input_schema`。
   最初直接把 `[]Tool` 反序列化到线上结构，schema 全空，工具声明退化成「无参数」——
   实测发现后加了独立的线上结构体。
2. **`meta` 与「调用先于正文」的冲突**：第 5 课规约要求 `meta` 是第一个事件，本课要求
   `meta` 里能看到工具调用，而调用发生在生成过程中。解法是「闸门」：没有候选工具时
   `meta` 照旧在生成前发出（第 5 课行为零变化）；有候选工具时先跑循环、把正文缓冲，
   等这一轮确定为最终回答，再发带 `tool_calls` 的 `meta` 并冲刷缓冲。代价是带工具的提问
   正文按片发出而非逐 token 实时。

### 未勾选项

- **8.4（对话页工具徽标）**：需要真实模型触发工具才能看到徽标。当日课程网关返回
  `429 今日 token 额度已用完`，模型相关链路无法再跑。代码路径已通过 `verify_mcp.py`
  第 5 节（`meta.tool_calls` 结构与顺序）验证，缺的是浏览器里的视觉确认。
- **10.3（前四课回归）**：第 3 课 45/45、第 4 课 72/72 已通过；第 5 课因额度用尽
  出现 2 条与模型相关的失败（非回归），`verify_frontend.mjs` 未重跑。额度恢复后执行：
  ```bash
  BASE_URL=http://127.0.0.1:8081 python scripts/verify_agent_chat.py
  node scripts/verify_frontend.mjs
  ```
  另：第 5 课脚本里的「期望路由集合」已同步加入本课 8 条路由，`/openapi.json` 与注册路由
  已单独核对一致（35 = 35）。
- **10.6（归档）**：规约写明待课程确认后执行。

### 已知副作用

- 验收会往库里新增工具服务（名称带时间戳）与技能/智能体；服务器没有删除以外的清理手段，
  重复运行会累积。`verify_mcp.py` 结束时会删掉自己登记的服务器，但技能与智能体保留。
