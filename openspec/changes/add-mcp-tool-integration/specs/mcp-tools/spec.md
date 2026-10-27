# Spec: mcp-tools

## Purpose

宿主侧把外部工具接进对话：管理角色登记服务器并完成握手与发现，技能用 `工具名@服务器名` 引用工具，提问时按最小权限把候选工具交给模型，由模型提出调用、宿主校验并执行，结果清洗后回传，全过程留一条可追溯的调用记录。令牌只在服务端出现，工具的返回与说明都按不可信文本处理。

## ADDED Requirements

### Requirement: 服务器登记与令牌边界

管理角色（教师）MUST 能登记本班 MCP 服务器，字段为名称、Base URL 与令牌。令牌 MUST 只保存在服务端：任何接口响应 MUST NOT 含令牌原文，MUST 只返回 `has_auth` 布尔值。名称 MUST 班内唯一，同名 MUST 返回 400。登记后状态 MUST 为未连接、工具数为 0。修改时令牌留空 MUST 表示不修改令牌。

写操作（登记、修改、删除、连接、断开）MUST 仅教师可用，学生 MUST 返回 403。按编号访问他班或不存在的服务器 MUST 返回与「记录不存在」完全一致的 404。

#### Scenario: 登记成功且响应不含令牌

- **WHEN** 教师提交 `{"name":"campus-tools","base_url":"http://mcp-demo:8090","auth_token":"secret"}`
- **THEN** MUST 返回 HTTP 201
- **AND** 响应 MUST 含 `id`、`name`、`base_url`、`connected:false`、`has_auth:true`、`tool_count:0`
- **AND** 响应正文 MUST NOT 含 `secret`

#### Scenario: 列表不回传令牌

- **WHEN** 教师请求服务器列表
- **THEN** 每一项 MUST 含 `has_auth` 布尔值
- **AND** 整份响应正文 MUST NOT 出现令牌字符串
- **AND** MUST NOT 出现 `auth_token` 字段名

#### Scenario: 同名被拒绝

- **WHEN** 教师用已存在的名称再次登记
- **THEN** MUST 返回 HTTP 400
- **AND** MUST NOT 新增记录

#### Scenario: 非管理角色被拒绝

- **WHEN** 学生请求登记、修改、删除、连接或断开服务器
- **THEN** MUST 返回 HTTP 403

#### Scenario: 他班与不存在的服务器同构

- **WHEN** 教师按编号访问他班服务器或一个不存在的编号
- **THEN** 两种情形 MUST 返回 HTTP 404
- **AND** 响应正文 MUST 逐字节一致

#### Scenario: 令牌留空表示不改

- **WHEN** 教师修改服务器时 `auth_token` 为空串
- **THEN** MUST 返回成功
- **AND** `has_auth` MUST 仍为 `true`

### Requirement: 连接与发现

「连接」MUST 由**握手**（`initialize`）与**发现**（`tools/list`）两步组成，两步都 MUST 带服务器自己的令牌。两步都成功时，系统 MUST 用本次发现结果**整体替换**该服务器的工具清单并标记为已连接；任一步失败时 MUST 停在未连接、MUST NOT 写入本次发现结果、并 MUST 记录可读的失败原因。连续连接两次，工具清单条数 MUST NOT 翻倍。

连接失败 MUST NOT 影响已有的检索与流式对话主路径。

#### Scenario: 连接成功后可见工具

- **WHEN** 教师连接一台可用的服务器
- **THEN** MUST 返回 HTTP 200，`connected` MUST 为 `true`
- **AND** 响应中的 `tools` MUST 含该服务器暴露的工具名（如 `formula_hint`、`unit_convert`）
- **AND** 服务器列表里该项 `tool_count` MUST 大于 0

#### Scenario: 连续连接两次清单不翻倍

- **WHEN** 对同一台服务器连续执行两次连接
- **THEN** 第二次连接后工具清单条数 MUST 与第一次相同
- **AND** MUST NOT 出现重复的工具名

#### Scenario: 地址不可达时停在未连接

- **WHEN** 服务器地址指向一个不可达的地址后执行连接
- **THEN** MUST 返回失败（4xx/5xx）
- **AND** 该服务器 `connected` MUST 仍为 `false`
- **AND** 响应或列表中 MUST 有可读的失败原因（提到地址或令牌）

#### Scenario: 令牌错误时停在未连接

- **WHEN** 把服务器令牌改成与工具服务不一致后执行连接
- **THEN** MUST 返回失败
- **AND** 该服务器 `connected` MUST 仍为 `false`
- **AND** 失败原因 MUST 可读

#### Scenario: 连接失败不阻断主路径

- **WHEN** 一台服务器连接失败后，学生在已有会话中提问
- **THEN** 提问 MUST 正常返回流式回答
- **AND** MUST NOT 因工具不可用而返回错误状态码

### Requirement: 断开

断开 MUST 清空该服务器的工具清单并置为未连接，重复断开 MUST 幂等成功。断开后，引用其工具的调用 MUST 记为 `denied`，MUST NOT 静默成功。

#### Scenario: 断开后清单清空

- **WHEN** 教师断开一台已连接的服务器
- **THEN** MUST 返回 HTTP 200 且 `connected` 为 `false`
- **AND** 该服务器的工具清单 MUST 为空

#### Scenario: 断开后调用被拒

- **WHEN** 用引用了该服务器工具的技能提问
- **THEN** `meta.tool_calls` 中该引用 MUST 存在且 `status` 为 `denied`
- **AND** 调用记录中 MUST 新增一条 `denied`
- **AND** 回答 MUST 仍正常返回

#### Scenario: 重复断开幂等

- **WHEN** 对同一台服务器连续断开两次
- **THEN** 两次 MUST 都返回成功
- **AND** MUST NOT 返回 5xx

### Requirement: 引用格式与保存前校验

技能的「工具」项 MUST 支持内置项 `knowledge_search` 与引用 `工具名@服务器名`。引用的工具名段 MUST 以小写字母开头、只含小写字母、数字与下划线（MUST NOT 含连字符）；服务器名段 MUST 允许连字符；两段之间 MUST 恰好一个 `@`。

保存技能时系统 MUST 依次校验四件事：格式是否合法、服务器是否已登记、服务器是否已连接、工具是否在该服务器最近一次发现清单内。任一条不满足 MUST 返回 HTTP 400 并给出可读错误，且 MUST NOT 保存该技能。

#### Scenario: 合法引用可保存

- **WHEN** 教师提交 `工具：knowledge_search,formula_hint@campus-tools`，且该服务器已连接、清单含该工具
- **THEN** MUST 返回 HTTP 201
- **AND** 技能的工具数组 MUST 同时含内置项与引用

#### Scenario: 格式错误被拒绝

- **WHEN** 提交以下任一工具项：`Formula_Hint@campus-tools`（大写）、`formula-hint@campus-tools`（工具名含连字符）、`formula_hint`（缺服务器名）
- **THEN** 每种情形 MUST 返回 HTTP 400
- **AND** 错误文案 MUST 给出可用写法

#### Scenario: 服务器未登记被拒绝

- **WHEN** 提交 `formula_hint@not-exist`
- **THEN** MUST 返回 HTTP 400
- **AND** 错误文案 MUST 指出服务器未登记

#### Scenario: 服务器未连接被拒绝

- **WHEN** 服务器已登记但处于未连接状态，提交引用它工具的技能
- **THEN** MUST 返回 HTTP 400
- **AND** 错误文案 MUST 指出该服务器当前未连接

#### Scenario: 工具不在清单被拒绝

- **WHEN** 服务器已连接，但引用了一个不在其发现清单内的工具名
- **THEN** MUST 返回 HTTP 400
- **AND** 错误文案 MUST 给出可用工具名

#### Scenario: 校验失败不落库

- **WHEN** 任一校验失败
- **THEN** 技能总数 MUST 保持不变

### Requirement: 候选集与最小权限

一次提问发送给模型的工具声明 MUST 只含**本次命中技能所引用的工具**。技能未命中、或技能只引用内置 `knowledge_search` 时，请求 MUST NOT 携带任何工具声明，模型 MUST 看不到任何工具。

#### Scenario: 命中带工具的技能才带声明

- **WHEN** 提问命中一个引用了 `formula_hint@campus-tools` 的技能
- **THEN** 该次请求 MUST 带 `formula_hint` 的工具声明
- **AND** `meta.tool_calls` MUST 出现该引用

#### Scenario: 只引用内置检索时不带工具

- **WHEN** 提问命中的技能工具项只有 `knowledge_search`
- **THEN** 该次请求 MUST NOT 携带任何工具声明
- **AND** `meta` MUST NOT 含 `tool_calls` 字段
- **AND** 调用记录条数 MUST 不变

#### Scenario: 未连接的服务器上的工具不进入候选集

- **WHEN** 同时登记两台服务器，A 已连接、B 未连接，提问命中引用 A 工具的技能
- **THEN** 该次请求 MUST 只含 A 的工具声明
- **AND** MUST NOT 出现 B 的工具

### Requirement: 调用循环与上限

工具声明 MUST 随请求发给模型。模型提出调用后，宿主 MUST 按该工具的 `inputSchema` 校验参数，通过后才带令牌发起 `tools/call`；参数校验失败 MUST NOT 执行该工具，并 MUST 把可读原因作为工具消息回传。执行结果 MUST 作为工具消息回传给模型，并 MUST 允许再来一轮。

系统 MUST 同时设置**轮次上限**与**单次提问的累计调用次数上限**。达到任一上限时 MUST 优雅降级：停止调用工具、让模型基于已有信息作答，MUST NOT 以错误状态码或 `error` 事件中断。

工具失败（参数不合法、超时、连接失败、`isError`）MUST 作为工具消息回传并带上原因，对话 MUST 继续。

#### Scenario: 两步任务能连续发起两轮

- **WHEN** 提问要求先把千米换成米、再换成厘米
- **THEN** 调用记录中 MUST 出现至少两条针对该引用的记录
- **AND** 两条记录的 `round_no` MUST 递增
- **AND** 回答 MUST 体现最终结果

#### Scenario: 参数不合法不执行工具

- **WHEN** 模型提出的参数缺少必填项或类型不符
- **THEN** 系统 MUST NOT 向远端发起 `tools/call`
- **AND** 该次调用 MUST 记为 `error` 且 `detail` 说明参数不合法
- **AND** 回答 MUST 仍正常返回

#### Scenario: 轮次上限触发后停止调用

- **WHEN** 把轮次上限调小到 1 后提问一个需要多轮工具的问题
- **THEN** 调用记录条数 MUST 不超过上限允许的次数
- **AND** HTTP 状态 MUST 为 200 且 MUST 有非空回答
- **AND** MUST NOT 出现 `error` 事件

#### Scenario: 累计调用次数上限生效

- **WHEN** 把单次提问的累计调用次数上限调小后提问
- **THEN** 该次提问产生的调用记录条数 MUST 不超过上限
- **AND** 回答 MUST 非空

#### Scenario: 工具自报错误仍继续对话

- **WHEN** 工具返回 `isError: true`（例如未知单位）
- **THEN** 该次调用 MUST 记为 `error` 并带原因
- **AND** 模型 MUST 收到该失败信息
- **AND** 回答 MUST 正常返回

### Requirement: 结果清洗与回传

工具结果 MUST 先清洗再进入上下文：命中指令式句式的**整条丢弃**并替换为固定说明；超长 MUST 截断（默认 1200 字）。工具结果 MUST 以工具消息的形式落在用户消息一侧（数据位），MUST NOT 进入系统消息。进入上下文时 MUST 带来源标注（工具引用）。

#### Scenario: 结果进入上下文并带来源标注

- **WHEN** 工具返回含「判别式」的提示，随后模型组织回答
- **THEN** 回答 MUST 体现该提示的内容
- **AND** 送给模型的工具消息 MUST 含该工具引用作为来源标注

#### Scenario: 指令式返回被整条丢弃

- **WHEN** 工具返回的文本命中指令式句式（如含「忽略之前的指令」）
- **THEN** 该条结果 MUST NOT 进入上下文
- **AND** 替换为固定说明
- **AND** 回答 MUST NOT 复述该指令

#### Scenario: 系统消息不含工具结果

- **WHEN** 检查任意一轮送给模型的系统消息
- **THEN** 系统消息 MUST NOT 含工具返回的正文

### Requirement: 流式调用可见

`meta` MUST 仍是第一个事件。本次确有工具调用时 `meta` MUST 含 `tool_calls` 数组，每项含 `ref`、`status` 与 `text`；本次没有调用时 MUST NOT 出现 `tool_calls` 字段（不给空数组）。`tool_calls` MUST 在第一条 `delta` 之前到达。

#### Scenario: 有调用时 meta 带 tool_calls 且先于正文

- **WHEN** 提问命中带工具的技能并真实发生调用
- **THEN** 第一个事件 MUST 是 `meta`
- **AND** `meta.tool_calls` MUST 为非空数组且每项含 `ref`、`status`、`text`
- **AND** 该 `meta` MUST 早于第一条 `delta`

#### Scenario: 无调用时不给空数组

- **WHEN** 提问没有触发任何工具调用
- **THEN** `meta` MUST NOT 含 `tool_calls` 字段

#### Scenario: 被拒时状态可见

- **WHEN** 服务器已断开，提问仍引用其工具
- **THEN** `meta.tool_calls` 中该引用 MUST 存在
- **AND** `status` MUST 为 `denied`
- **AND** `text` MUST 给出可读原因

### Requirement: 调用记录

每一次**能归属到某台服务器**的工具调用 MUST 写一条记录，回答五个问题：`tool_ref`（调了什么）、`status`（成了没有，取 `ok`/`denied`/`error`）、`user_id`（谁触发的）、`conversation_id` 与 `round_no`（在哪次对话的第几轮）、`created_at`（什么时候），另有 `server_name`、`tool_name` 与 `detail`（结果摘要或失败原因）。记录 MUST 只增不改。

`GET /api/mcp/tool-calls` MUST 仅教师可用，MUST 只返回本班记录，按时间倒序，默认最多 50 条。服务器被改名或删除后，该引用 MUST 记为 `denied`，且该次调用 MUST NOT 写记录（无法归属）。

#### Scenario: 成功调用留一条记录

- **WHEN** 一次调用执行成功
- **THEN** MUST 新增一条 `status` 为 `ok` 的记录
- **AND** 记录 MUST 含 `tool_ref`、`server_name`、`tool_name`、`user_id`、`conversation_id`、`round_no`、`created_at`、`detail`
- **AND** `detail` MUST 非空

#### Scenario: 三种结果各能查到

- **WHEN** 分别制造一次成功、一次被拒（未连接）与一次出错（工具 `isError`）的调用
- **THEN** 记录列表 MUST 同时含 `ok`、`denied`、`error` 三种状态

#### Scenario: 非管理角色不能读记录

- **WHEN** 学生请求调用记录
- **THEN** MUST 返回 HTTP 403

#### Scenario: 只看本班记录

- **WHEN** 教师读取调用记录
- **THEN** 返回的每一条 MUST 属于本班
- **AND** MUST NOT 出现其他班级的记录

#### Scenario: 服务器删除后调用不写记录

- **WHEN** 删除一台已连接的服务器后，用仍引用其工具的技能提问
- **THEN** `meta.tool_calls` 中该引用 MUST 为 `denied`
- **AND** 调用记录条数 MUST 与删除前一致
