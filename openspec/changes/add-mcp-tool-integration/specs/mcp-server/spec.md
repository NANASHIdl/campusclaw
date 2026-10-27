# Spec: mcp-server

## Purpose

提供一个只暴露工具的 MCP 服务器：用 JSON-RPC 2.0 在单一 HTTP 入口上实现握手、发现与执行三个方法，用环境变量里的令牌做认证，并把「工具是什么、参数怎么填」写进机器可读的工具声明。它是本课的可独立验收部分——不依赖宿主，带令牌用 `curl` 就能验。

## ADDED Requirements

### Requirement: 协议方法与传输

服务 MUST 以 **JSON-RPC 2.0** 报文实现 `initialize`、`tools/list`、`tools/call` 三个方法，MUST 以 `POST /mcp` 作为唯一入口，并 MUST 提供不需要令牌的 `GET /health`。`initialize` MUST 返回协议版本、`capabilities.tools` 与服务器标识；`tools/list` MUST 返回全部工具的 `name`、`description` 与 `inputSchema`；`tools/call` MUST 按 `name` 与 `arguments` 执行并返回内容数组。

请求体不是合法 JSON、缺少 `jsonrpc` 或 `method`、方法不存在、参数不合法时，服务 MUST 返回带标准错误码的 JSON-RPC 错误对象，且 HTTP 状态 MUST 为 200（鉴权失败除外，见「令牌校验」）。

#### Scenario: 初始化握手返回能力

- **WHEN** 携带有效令牌向 `POST /mcp` 发送 `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"campusclaw","version":"1.0"}}}`
- **THEN** 响应 MUST 为 `result` 含 `protocolVersion`、`capabilities.tools` 与 `serverInfo.name` 的对象
- **AND** 响应 MUST NOT 含 `error` 字段

#### Scenario: 列出工具

- **WHEN** 携带有效令牌发送 `tools/list`
- **THEN** `result.tools` MUST 为数组且长度 ≥ 2
- **AND** 每一项 MUST 同时含 `name`、`description`、`inputSchema` 三个字段

#### Scenario: 调用工具返回文本

- **WHEN** 携带有效令牌发送 `tools/call`，`params` 为 `{"name":"formula_hint","arguments":{"topic":"求根公式"}}`
- **THEN** `result.content` MUST 为非空数组，且至少一项的 `type` 为 `text`、`text` 非空
- **AND** `result.isError` MUST 为 `false`

#### Scenario: 非法报文返回 JSON-RPC 错误

- **WHEN** 发送无法解析的报文、缺少 `method` 的报文，或调用不存在的方法
- **THEN** 响应 MUST 含 `error` 对象且 `error.code` 分别为 `-32700`、`-32600`、`-32601`
- **AND** HTTP 状态 MUST 为 200
- **AND** `error.message` MUST 是可读的中文说明

#### Scenario: 参数不合法被拒绝

- **WHEN** 发送 `tools/call` 但 `arguments` 缺少该工具的必填参数，或参数类型不符
- **THEN** 服务 MUST NOT 执行工具
- **AND** 响应 MUST 给出可读原因（`error.code` 为 `-32602`，或 `result.isError` 为 `true`）

### Requirement: 令牌校验

服务 MUST 从环境变量 `MCP_AUTH_TOKEN` 读取令牌，MUST NOT 把令牌写进代码、镜像层或任何响应正文。请求缺少 `Authorization` 头、方案不是 `Bearer`、或令牌与配置不一致时，服务 MUST 返回 **HTTP 401** 且正文为 JSON-RPC 错误对象。`GET /health` MUST NOT 要求令牌。

#### Scenario: 无令牌被拒绝

- **WHEN** 不带 `Authorization` 头调用 `tools/list`
- **THEN** HTTP 状态 MUST 为 401
- **AND** 正文 MUST 含 `error` 对象
- **AND** `result.tools` MUST NOT 出现

#### Scenario: 令牌错误被拒绝

- **WHEN** 携带 `Authorization: Bearer 错误的令牌` 调用 `tools/list`
- **THEN** HTTP 状态 MUST 为 401

#### Scenario: 令牌正确则通过

- **WHEN** 携带与 `MCP_AUTH_TOKEN` 一致的令牌调用 `tools/list`
- **THEN** HTTP 状态 MUST 为 200 且 `result.tools` 非空

#### Scenario: 健康检查不需要令牌

- **WHEN** 不带令牌请求 `GET /health`
- **THEN** HTTP 状态 MUST 为 200
- **AND** 正文 MUST 含 `"status":"ok"` 与工具数量
- **AND** 正文 MUST NOT 含令牌字符串

### Requirement: 工具声明

服务 MUST 至少暴露两个工具，每个工具 MUST 写全**名称**、**说明**与**输入结构**三项。工具名 MUST 以小写字母开头、只含小写字母、数字与下划线。说明 MUST 写清**适用场景**与**不适用场景**（模型据此判断要不要调用）。输入结构 MUST 是 JSON Schema，声明每个参数的名称、类型与是否必填。

工具 MUST 只给提示或换算结果，MUST NOT 给出最终答案、MUST NOT 代做题。

#### Scenario: 两个工具的声明三项齐全

- **WHEN** 检查 `tools/list` 的返回
- **THEN** MUST 至少含 `formula_hint` 与 `unit_convert` 两个工具
- **AND** 两者的 `name` MUST 匹配 `^[a-z][a-z0-9_]*$`
- **AND** 两者的 `inputSchema` MUST 为 `type: object` 且含非空 `properties` 与 `required`

#### Scenario: 说明写清适用与不适用

- **WHEN** 检查任一工具的 `description`
- **THEN** 说明 MUST 含适用场景的描述
- **AND** 说明 MUST 含不适用场景或边界（例如「只给提示不给最终答案」「不做不同物理量之间的换算」）

#### Scenario: 工具只给提示不给答案

- **WHEN** 调用 `formula_hint`，`topic` 为「求根公式」
- **THEN** 返回文本 MUST 含公式或下一步检查点
- **AND** MUST NOT 给出某一具体方程的最终解

#### Scenario: 换算只做一步

- **WHEN** 调用 `unit_convert`，`arguments` 为 `{"value":3,"from":"千米","to":"米"}`
- **THEN** 返回文本 MUST 含换算结果 `3000`
- **AND** MUST NOT 自动继续换算成第三个单位

### Requirement: 工具执行与返回

`tools/call` 的 `result` MUST 含 `content` 数组与 `isError` 标记。工具无法处理请求时（未知主题、未知单位、不同物理量互转）MUST 返回 `isError: true` 与可读原因，MUST NOT 编造数值。返回文本 MUST NOT 夹带指令式语句。

#### Scenario: 未知主题返回错误标记

- **WHEN** 调用 `formula_hint`，`topic` 为「怎么做红烧肉」
- **THEN** `result.isError` MUST 为 `true`
- **AND** 返回文本 MUST 说明该主题不在能力范围内

#### Scenario: 不同物理量互转被拒绝

- **WHEN** 调用 `unit_convert`，`from` 为「米」、`to` 为「千克」
- **THEN** `result.isError` MUST 为 `true`
- **AND** 返回文本 MUST 说明两者不是同一物理量

#### Scenario: 返回文本不含指令式语句

- **WHEN** 检查任一工具的返回文本
- **THEN** MUST NOT 含「忽略之前的指令」「请调用」「system:」这类指令式句式
