# Spec: agent-chat（增量）

> 本文件是 `agent-chat` 的 delta 规约：第 6 课把技能文件里的「工具」项从单一白名单扩展为「内置项 + `工具名@服务器名` 引用」，
> 并让流式响应的 `meta` 带上本次的工具调用。其余 Requirement 不变。

## MODIFIED Requirements

### Requirement: 技能文件解析

技能 MUST 以单一文本文件原文提交（请求体为 `{"source": "..."}`），整份文件 MUST 不超过 8000 字符。空行以上为元数据（名称、适用条件、资料、工具、启用），空行以下为说明。名称、适用条件、启用 MUST 为必填；同一字段重复出现、出现未定义字段、或缺少分隔空行时，系统 MUST 返回 400 并给出可读错误。

「工具」项 MUST 接受内置项 `knowledge_search`，也 MUST 接受引用 `工具名@服务器名`（格式、保存前校验与运行期语义见 `mcp-tools` 规约）；既不是内置项也不符合引用格式的值 MUST 被拒绝。启用 MUST 只接受「是」或「否」。名称 MUST 不超过 32 字符，适用条件不超过 120，说明不超过 2000。元数据区中以 `#` 开头的行与空行 MUST 被忽略。解析成功后 MUST 保留文件原文供取回再编辑。

#### Scenario: 合法技能文件被接受

- **WHEN** 教师提交一份五项齐全且空行以下有说明的文件
- **THEN** 系统 MUST 返回 HTTP 201
- **AND** 响应 MUST 含名称、适用条件、资料、工具数组、启用状态与文件原文

#### Scenario: 空文件被拒绝

- **WHEN** 提交的 `source` 为空
- **THEN** 系统 MUST 返回 HTTP 400
- **AND** 错误文案 MUST 指出技能文件为空

#### Scenario: 缺少分隔空行被拒绝

- **WHEN** 提交的文件没有分隔元数据与说明的空行
- **THEN** 系统 MUST 返回 HTTP 400
- **AND** 错误文案 MUST 说明需要一个空行并在其下写说明

#### Scenario: 重复字段与未定义字段被拒绝

- **WHEN** 提交的文件中出现同一字段两次，或出现五项之外的字段
- **THEN** 系统 MUST 返回 HTTP 400 并给出可读错误

#### Scenario: 工具项接受内置项与引用

- **WHEN** 提交 `工具：knowledge_search,formula_hint@campus-tools`，且该引用通过保存前校验
- **THEN** 系统 MUST 返回 HTTP 201
- **AND** 技能的工具数组 MUST 含 `knowledge_search` 与 `formula_hint@campus-tools` 两项

#### Scenario: 非法工具名被拒绝

- **WHEN** 提交的工具项是 `web_search`（既不是内置项 `knowledge_search`，也不含 `@服务器名`）
- **THEN** 系统 MUST 在解析阶段返回 HTTP 400
- **AND** 错误文案 MUST 给出内置项与引用的可用写法

#### Scenario: 超长文件被拒绝

- **WHEN** 提交的文件超过 8000 字符
- **THEN** 系统 MUST 返回 HTTP 400

### Requirement: 流式输出

`POST /api/conversations/{id}/messages` MUST 以 `text/event-stream` 逐段推送，响应头 MUST 含 `Content-Type: text/event-stream`、`Cache-Control: no-cache` 与 `Connection: keep-alive`，并 MUST 含 `X-Accel-Buffering: no`。每帧 MUST 以 `event:` 与 `data:` 两行加一个空行构成，且 MUST 逐帧 flush。

事件分四类：`meta`（状态、命中技能、引用列表，最先到达）、`delta`（本次新增文本）、`done`（助手消息编号）、`error`（流开始后的失败）。本次提问确有工具调用时，`meta` MUST 追加 `tool_calls` 数组（每项含 `ref`、`status`、`text`，见 `mcp-tools` 规约）；没有调用时 MUST NOT 出现该字段。

各次 `delta` 的文本按顺序拼接 MUST 等于最终回答，单条 `delta` MUST NOT 是此前片段的累计全文。提问 `content` 去空白后 MUST 为 2 至 500 字符，越界 MUST 在流开始前返回 400。

#### Scenario: 四类事件的顺序

- **WHEN** 发起一轮有依据的提问
- **THEN** 第一个事件 MUST 是 `meta` 且 MUST 含引用列表
- **AND** 其后 MUST 有至少一条 `delta`
- **AND** 最后一个事件 MUST 是 `done` 且 MUST 含助手消息编号

#### Scenario: delta 是增量而非累计

- **WHEN** 检查多次 `delta`
- **THEN** 每次 `data` 中的 `text` MUST 是本次新增的片段
- **AND** 各次 `text` 顺序拼接 MUST 等于落库的回答全文

#### Scenario: 提问长度越界

- **WHEN** 提交的 `content` 去空白后不足 2 个字符或超过 500 个字符
- **THEN** 系统 MUST 在开始流之前返回 HTTP 400

#### Scenario: 引用列表先于正文到达

- **WHEN** 客户端收到第一个事件
- **THEN** 该事件 MUST 已包含完整的引用列表
- **AND** 客户端 MUST 能在此刻显示出处，无需等待正文

#### Scenario: 工具调用随 meta 先于正文到达

- **WHEN** 本轮真实发生了工具调用
- **THEN** `meta` MUST 含非空 `tool_calls`，且每项含 `ref`、`status`、`text`
- **AND** 该 `meta` MUST 早于第一条 `delta`
- **AND** 没有发生调用时 `meta` MUST NOT 含 `tool_calls`
