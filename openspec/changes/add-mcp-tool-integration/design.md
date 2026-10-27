# Design: add-mcp-tool-integration

## Context

第 5 课已经有了「智能体 + 技能 + 会话 + SSE」这条主链路，技能文件里也有一个 `工具` 字段——但它只是白名单值 `knowledge_search`，检索由服务端固定执行，模型全程不参与「要不要用工具」这个决定。

本课要把它变成真正的工具调用：一个能暴露工具的服务器（`mcp-server/`），加上宿主里「发现 → 声明 → 校验 → 执行 → 回传 → 收敛」这段编排。

已确认的外部前提（动手前实测）：

- 课程对话网关 `course-chat` **支持 OpenAI 风格的函数调用**：非流式返回 `finish_reason: "tool_calls"` 与结构化的 `arguments`；流式响应里 `tool_calls` 以分片形式出现（`delta.tool_calls[{index, function:{name?, arguments}}]`），需要按 `index` 累积拼接。
- 流式分片里还有 `reasoning_content`，**不属于正文**，必须忽略，只取 `content`。

## Goals / Non-Goals

- Goal：一个可单独验收的 MCP 工具服务（三个方法、两个工具、令牌）。
- Goal：宿主侧登记/连接/发现/断开与令牌边界。
- Goal：引用格式 `工具名@服务器名` 与保存前校验。
- Goal：有上限的调用循环，超限优雅降级。
- Goal：调用记录（只增不改）与 SSE 中的 `tool_calls`。
- Non-Goal：resources/prompts、stdio/SSE 传输、指标与追踪、人工确认、说明变更检测、并发与缓存、学生管理、记录分页导出、输出侧扫描（见 proposal）。

## Decision 1：`mcp-server` 独立成模块，只依赖标准库

- 目录 `mcp-server/`，自带 `go.mod`（module `campusclaw/mcp-server`），**不引入任何第三方依赖**：JSON-RPC 报文用 `encoding/json`，HTTP 用 `net/http`。
- 好处：Dockerfile 不需要 `go mod vendor`，`GOPROXY=off` 直接可构建；也不会和主应用的依赖版本纠缠。
- 服务名 `mcp-demo`，容器内监听 `:8090`，**不映射宿主机端口**（沿用「只有 web 对外」的约定）。

## Decision 2：协议实现

传输：`POST /mcp` 单入口，`Content-Type: application/json`，请求体是一个 JSON-RPC 2.0 对象（不做批量数组）。

方法：

| 方法 | 请求 params | 结果 |
| --- | --- | --- |
| `initialize` | `{protocolVersion, capabilities, clientInfo}` | `{protocolVersion, capabilities:{tools:{}}, serverInfo:{name,version}}` |
| `tools/list` | 无 | `{tools:[{name, description, inputSchema}]}` |
| `tools/call` | `{name, arguments}` | `{content:[{type:"text", text:"..."}], isError:false}` |

错误码沿用 JSON-RPC 标准：`-32700` 解析错误、`-32600` 请求非法、`-32601` 方法不存在、`-32602` 参数非法、`-32603` 内部错误。错误一律是「HTTP 200 + error 对象」，**除了鉴权失败**：

- 令牌缺失或与 `MCP_AUTH_TOKEN` 不一致 → **HTTP 401** + `error.code = -32001`。
- 用 401 而不是 200，是为了让宿主「连接失败」的判定简单可靠：非 200 即失败，不必解析错误体。

`GET /health` 不需要令牌，返回 `{"status":"ok","service":"campusclaw-mcp-demo","tools":2}`。

## Decision 3：两个工具与它们的说明

工具只给提示、不给最终答案，与第 5 课「解题引导」的口径一致。

### `formula_hint`

```json
{
  "name": "formula_hint",
  "description": "按主题给出一条数学公式提示（例如求根公式、勾股定理、扇形面积）。只给公式与下一步该检查什么，不给最终答案、不代做题。适用：学生在解题中途卡住、需要回忆公式时。不适用：需要具体数值计算结果、或与数学无关的问题。",
  "inputSchema": {
    "type": "object",
    "properties": {
      "topic": {"type": "string", "description": "公式主题，中文或英文均可，如 求根公式 / 勾股定理 / 扇形面积"},
      "grade": {"type": "string", "enum": ["小学", "初中", "高中"], "description": "学段，用于调整提示深度，缺省按初中"}
    },
    "required": ["topic"],
    "additionalProperties": false
  }
}
```

### `unit_convert`

```json
{
  "name": "unit_convert",
  "description": "在同一物理量的单位之间换算（长度、质量、时间、温度）。只做一步换算；需要多步时请连续调用。不适用：不同物理量之间的换算（如米换千克）、货币与汇率。",
  "inputSchema": {
    "type": "object",
    "properties": {
      "value": {"type": "number", "description": "待换算的数值"},
      "from": {"type": "string", "description": "原单位，如 米 / 千米 / 厘米 / 克 / 千克 / 秒 / 分钟 / 摄氏度"},
      "to": {"type": "string", "description": "目标单位"}
    },
    "required": ["value", "from", "to"],
    "additionalProperties": false
  }
}
```

说明里刻意写了**适用 / 不适用**与**单位**，因为说明会原样进入提示词，模型判断「要不要调、参数怎么填」全靠它；写宽了会被当成答案源（课件「说明是写给模型看的」）。`unit_convert` 只做一步，正是为了能稳定触发**两轮调用**（先换米、再换厘米）。

返回文本形如：

```
公式：x = (-b ± √(b²-4ac)) / (2a)
下一步：先确认方程已经化成 ax²+bx+c=0 的标准形式，再核对 a、b、c 的符号。
```

```
3 千米 = 3000 米（长度）
```

工具内部对未知主题/单位返回 `isError: true` 与可读原因，而不是编一个数。

## Decision 4：数据模型（3 张表）

```sql
mcp_servers(
  id, class_id, name, base_url, auth_token, connected, connected_at, last_error,
  created_at, updated_at,
  UNIQUE (class_id, name), INDEX (class_id)
)

mcp_tools(
  id, server_id, name, description, input_schema JSON, discovered_at,
  UNIQUE (server_id, name),
  FOREIGN KEY (server_id) REFERENCES mcp_servers(id) ON DELETE CASCADE
)

mcp_tool_calls(
  id, class_id, server_id NULL, server_name, tool_name, tool_ref,
  status ENUM('ok','denied','error'), user_id, conversation_id, round_no,
  detail, created_at,
  INDEX (class_id, created_at), INDEX (conversation_id)
)
```

- `mcp_tool_calls.server_id` **不建外键**：删除服务器时历史记录要保留，`server_name` 存的是当时的名字快照。
- `mcp_tools` 随服务器级联删除：清单是「最近一次成功的发现结果」，服务器没了就没有意义。
- `input_schema` 存 `JSON` 列，发现时原样落库；执行时**按同一份结构校验参数**——这正是「声明必须来自发现结果」的落地方式。

## Decision 5：令牌边界

- 令牌只出现在三处：`.env`、`mcp_servers.auth_token` 列、发往远端的 `Authorization` 头。
- 接口响应一律只回 `has_auth: true|false`；`Server` 的 JSON 标签里根本不含 `auth_token` 字段（结构体不带该字段，避免手滑）。
- 日志只记 `server`、`tool_ref`、`status`、错误原因，不记令牌。
- 前端输入框 `type="password"`，提交后清空，不回显；修改服务器时令牌留空表示不改。
- 每台服务器各自一把令牌，不共用（共用等于取并集）。

## Decision 6：连接＝握手 + 发现，五步且只走一次

```
1 取连接信息（base_url + 令牌）      —— 不在可用范围 → 同构 404
2 拼端点 base_url + "/mcp"
3 initialize 握手（带令牌）          —— 失败：停在未连接 + 可读原因
4 tools/list 取清单                  —— 失败：同上
5 事务内「删旧清单 + 写新清单 + 置 connected」—— 失败：同样停在未连接
```

第 5 步是**替换**而不是追加：本地清单恒等于最近一次成功发现的结果，远端删掉的工具不会残留。
3、4 步失败时清单与状态都不更新——不记住一次失败的发现结果。

`last_error` 存最近一次失败原因，供界面显示；成功后清空。

## Decision 7：引用格式与四类校验

正则：

- 引用整体：`^[a-z][a-z0-9_]*@[a-z0-9][a-z0-9-]*$`
- 工具名段：小写字母开头，只含小写字母/数字/下划线（不允许连字符）
- 服务器名段：小写字母或数字开头，可含连字符

四类检查在**保存技能时**做（配置期从严）：

1. 格式是否匹配 → 不匹配 `400`，错误里给出可用写法；
2. 服务器是否登记（按 `class_id + name` 查）→ 查不到按「不存在」处理，`400` 可读提示；
3. 服务器是否已连接 → 未连接 `400`，提示「服务器当前未连接，请先连接」；
4. 工具是否在 `mcp_tools` 清单内 → 不在 `400`，提示可用工具名。

运行期只判「服务器还在且已连接」这一条，其余信任保存时的结果——这是课件讲的「配置期从严、运行期从简」，也是 TOCTOU 的自觉：保存后服务器被断开/改名/删除，引用不会跟着变，调用即失败，记 `denied`。

`knowledge_search` 仍作为内置项保留，不需要服务器。

## Decision 8：候选集与最小权限

- 候选集 = **命中技能所引用的工具**（`tools` 字段里除 `knowledge_search` 之外的部分）。
- 技能没命中、或技能只引用 `knowledge_search` → **不发送任何工具声明**，模型看不到任何工具，行为与第 5 课完全一致。
- 这是「先按用途划定上限，再让模型在上限内自主决定」：上限由技能配置决定（配置期），选哪个由模型决定（运行期）。

## Decision 9：调用循环

状态机（一次提问内）：

```
round = 0
history = [system, ...窗口内历史, user(含编号资料)]
loop:
  round++
  result = 模型(history, tools=候选集, stream=true)      # 文本回调先缓冲
  if result.ToolCalls 为空:
      这是最终回答 → 开闸（发 meta 并冲刷缓冲）→ 结束
  if round >= MAX_ROUNDS 或 calls + len(ToolCalls) > MAX_CALLS:
      优雅降级：丢掉本轮工具请求，追加一条「已到调用上限，请基于已有信息作答」的用户侧说明，
      再发一次 **不带 tools** 的请求，取其正文 → 结束
  for call in result.ToolCalls:
      校验参数（按 input_schema）→ 执行 tools/call → 清洗 → 追加工具消息
      calls++
  goto loop
```

- 参数校验失败：不执行，把「参数不合法：…」作为工具消息回传，让模型自己改参数（局部失败，不中断）。
- 工具执行失败（超时/连不上/`isError`）：同样作为工具消息回传，带原因；对话继续。
- 上限默认 `MCP_MAX_ROUNDS=3`、`MCP_MAX_CALLS=4`，都可从环境变量调小（验收要「把上限调小后超限即停止」）。

**为什么工具消息落在用户侧**：与检索资料同一通道。系统提示里的文字更像指令，数据位里的文字更像素材；工具返回是不可信文本，必须离系统提示远一点。

## Decision 10：SSE 与 `meta.tool_calls`

课件要求「调用事件先于正文」且「`meta` 中能看见本次用了哪些工具」，而第 5 课已归档的 `agent-chat` 规约要求「`meta` 必须是第一个事件」。两者合起来只有一种做法：**先跑工具循环，再发 `meta`，再发正文**。

于是引入一个「闸门」（gate）：

- 候选集为空 → 闸门一开始就是开的：`meta` 照第 5 课那样在生成前发出，正文实时流式（第 5 课行为零变化）。
- 候选集非空 → 闸门关着：每轮的文本先缓冲；一旦某一轮没有工具调用（即这轮就是最终回答），**先发 `meta`（带上累积的 `tool_calls`）**，再把缓冲文本按片发出去。

代价：带工具的提问，正文不是逐 token 实时到达，而是「工具跑完后按片发出」。这是为满足「调用先于正文」付的账；不带工具的路径不受影响，第 5 课的 11.5 断言仍然成立。

`meta` 结构（`tool_calls` 仅在有调用时出现，不给空数组）：

```json
{"status":"ok","skill":{"id":1,"name":"数学解题引导"},
 "citations":[…],
 "tool_calls":[{"ref":"formula_hint@campus-tools","status":"ok","text":"公式：x = …"}]}
```

`text` 是结果摘要（截断到 200 字）；`denied`/`error` 时放原因。

## Decision 11：结果清洗

进上下文之前：

1. 命中指令式句式 → **整条丢弃**，换成固定说明「（工具返回的内容包含疑似指令，已丢弃）」；
2. 超过 1200 字截断；
3. 加来源标注：`[工具 formula_hint@campus-tools 返回的数据]` + 正文。

句式表（小写匹配，正则）：

```
忽略(之前|以上|前面)的?(指令|要求|提示)
ignore (all )?(previous|above)
(^|\n)\s*(system|assistant)\s*:
请?(调用|执行|运行)
必须(调用|执行)
把.{0,20}(发送|上传|提交)到
```

这只能降低概率（改写即可绕过），真正拦动作的是授权与配额——与课件「四道防线」的分工一致。

## Decision 12：调用记录

一次调用一条，五个问题一一对应：

| 问题 | 字段 |
| --- | --- |
| 调了什么 | `tool_ref`（`工具@服务器`）+ `server_name` + `tool_name` |
| 成了没有 | `status`（`ok`/`denied`/`error`）+ `detail`（摘要或原因） |
| 谁触发的 | `user_id` |
| 在哪次对话里 | `conversation_id` + `round_no` |
| 什么时候 | `created_at` |

- 只增不改，无更新接口。
- `denied`：服务器存在但未连接，或工具不在清单内（**能归属**到服务器，所以记录）。
- 服务器被改名或删除：`工具@服务器` 解析不到服务器 → 无法归属 → **不写记录**，只体现在本次应答的 `tool_calls` 里（`denied`）。

## Decision 13：接口与文档

新增 8 条路由，全部教师专属（`GET /api/mcp/tool-calls` 也是教师）：

```
GET    /api/mcp/servers
POST   /api/mcp/servers
PUT    /api/mcp/servers/{id}
DELETE /api/mcp/servers/{id}
POST   /api/mcp/servers/{id}/connect
POST   /api/mcp/servers/{id}/disconnect
GET    /api/mcp/servers/{id}/tools
GET    /api/mcp/tool-calls
```

路由表（`cmd/server/routes.go`）加条目 + 处理函数加注解，启动自检自动覆盖——这是第 5 课定下的机制，本课只是把集合扩大。

响应形态（与原型对齐，字段名保持一致便于对照）：

```json
// GET /api/mcp/servers
{"servers":[{"id":1,"name":"campus-tools","base_url":"http://mcp-demo:8090",
             "connected":true,"has_auth":true,"tool_count":2,"last_error":""}]}

// POST /api/mcp/servers/{id}/connect
{"connected":true,"tools":[{"name":"formula_hint","description":"…"}]}

// GET /api/mcp/tool-calls
{"calls":[{"id":1,"tool_ref":"formula_hint@campus-tools","server_name":"campus-tools",
           "tool_name":"formula_hint","status":"ok","user_id":2,
           "conversation_id":7,"round_no":1,"detail":"公式：…","created_at":"2026-10-27 16:00:00"}]}
```

## Decision 14：前端

- 新增页签「工具服务」（仅教师可见）：
  - 登记表单：名称、Base URL、令牌（password，提交后清空）；
  - 列表：名称、地址、是否已配令牌、是否已连接、工具数、最近错误；
  - 行内操作：连接并发现、断开、查看工具、编辑、删除；
  - 下方「最近调用」表：时间、工具引用、状态、用户、轮次。
- 对话页：`meta.tool_calls` 到达时在回答上方渲染工具徽标（`[工具] formula_hint@campus-tools · 成功`），与引用徽标同一视觉层级。

## 风险与取舍

| 风险 | 取舍 |
| --- | --- |
| 带工具的提问正文不再逐 token 到达 | 换「调用先于正文」；无工具路径不变 |
| 句式检查可被绕过 | 明说是「降低概率」，动作靠授权与配额拦 |
| 保存后服务器状态变化导致调用失败 | 这是 TOCTOU，记 `denied` 并给可读原因，不做保存后重校验 |
| 轮次上限太小可能答不完 | 默认 3 轮 / 4 次调用，可按需调；超限是降级不是报错 |
