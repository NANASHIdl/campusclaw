# Tasks: add-agent-streaming-chat

> 第 5 课 Apply：实现顺序 依赖 → 认证 → 存储 → 接口 → 编排 → 流式 → 文档 → 前端 → 测试 → 验证。
> 每条任务附 `verify:`，勾选发生在 Apply 阶段。
> 勾选依据见文末「验收记录（13.2）」；未勾选项都写明了原因与人工步骤。

## 1. 依赖与构建（T1）

- [x] 1.1 引入 `github.com/swaggo/swag`（构建期）与 `github.com/swaggo/http-swagger`（运行时） — verify: `go.mod` 含两项；`go mod vendor` 成功；`go build ./...` 通过
- [x] 1.2 镜像仍离线构建：Dockerfile 保持 `GOPROXY=off`，依赖来自 `vendor/` — verify: 断网/清空模块缓存后 `docker compose build api` 成功
- [x] 1.3 生成脚本：`go run github.com/swaggo/swag/cmd/swag init -g cmd/server/main.go -o docs --parseDependency --parseInternal`，产物入库 — verify: `api/docs/{docs.go,swagger.json,swagger.yaml}` 存在且可重复生成

## 2. 认证迁移为 JWT Bearer（T2）

- [x] 2.1 `internal/jwtauth`：HS256 签发与校验；拒绝非 HS256 的 `alg`（含 `none`）；`hmac.Equal` 常量时间比较 — verify: 单元测试覆盖签名错误、alg 篡改、过期
- [x] 2.2 载荷只含 `sub`/`jti`/`iat`/`exp`；角色与班级按 `sub` 回查 `users` — verify: 解码令牌断言无角色与 `class_id`；改库中角色后下次请求立即生效
- [x] 2.3 `revoked_tokens` 表 + 登出写 `jti` + 校验时查吊销 — verify: 登出后同一令牌 401；重复登出 401
- [x] 2.4 携带未过期旧 Bearer 再次登录：先吊销旧 `jti` 再签发新令牌 — verify: 旧令牌立即失效，新令牌可用
- [x] 2.5 `RequireAuth` 改读 `Authorization: Bearer`；Cookie 不再作为凭据；新增 `RequireTeacher` — verify: 仅带 Cookie 的请求 401；学生访问教师接口 403
- [x] 2.6 白名单 4 个入口免鉴权 — verify: 无令牌访问 `/health`、`/api/login`、`/openapi.json`、`/swagger/index.html` 均非 401
- [x] 2.7 `sessions` 表退役：迁移中 `DROP TABLE IF EXISTS sessions` — verify: `SHOW TABLES` 无 `sessions`

## 3. 数据模型与种子（T3）

- [x] 3.1 幂等创建 7 张表：`tutor_agents`、`skills`、`tutor_agent_skills`、`tutor_agent_materials`、`conversations`、`messages`、`revoked_tokens` — verify: `SHOW TABLES` 返回预期数量；重复启动不报错
- [x] 3.2 唯一约束：智能体与技能各 `(class_id, name)`；预置技能 `(class_id, seed_key)` — verify: 同名插入 1062；`seed_key` 为 NULL 的多行不冲突
- [x] 3.3 级联：删技能/材料/会话时绑定与消息一并清除 — verify: 删技能后 `tutor_agent_skills` 无残留
- [x] 3.4 种子：两份技能（数学/语文解题引导，**逐字取自课件样板**，`seed_key` 为 `math-tutor`/`chinese-tutor`）、一个已启用示例智能体（绑定两份技能与 A 班全部材料） — verify: 重复 `up` 不重复插入；学生可列出该智能体；两份技能的 `source_text` 与课件样板逐字一致

## 4. 智能体接口（T4）

- [x] 4.1 `GET /api/agents`：教师见全部、学生见已启用；响应不含 `class_id` — verify: 两角色返回数量不同；响应无 `class_id` 字段
- [x] 4.2 `POST /api/agents`：名称去空白 1–64、提示词 ≤2000 **且允许为空**、`skill_ids`/`material_ids` 属本班否则 404、空绑定合法、同名 400、返回 201 — verify: 六种情形状态码正确
- [x] 4.3 `GET /api/agents/{id}`：学生读未启用/他班/不存在均同构 404 — verify: 三者响应逐字节一致
- [x] 4.4 `PUT /api/agents/{id}`：仅教师；提示词允许为空；跨班 404；与他人同名 400 — verify: 清空提示词返回 200
- [x] 4.5 提示词回退：`prompt` 为空时使用内置默认角色设定，不拒绝请求 — verify: 单元测试覆盖「有 prompt」与「prompt 为空」两种情形
- [x] 4.6 绑定不随会话固化：每轮提问重新读取当前绑定 — verify: 解绑技能后在同一会话重放同一提问，路由结果改变

## 5. 技能解析与接口（T5）

- [x] 5.1 解析器：`source` 按首个空行切分元数据与说明；`#` 开头行与空行忽略；五项白名单 — verify: 合法文件解析出 5 个字段 + 说明
- [x] 5.2 校验：空 `source`、缺分隔空行、缺必填、重复字段、未定义字段、非法工具名、非「是/否」的启用、超 8000 字符 → 400 且文案可读 — verify: 八类错误各有明确提示
- [x] 5.3 长度校验：名称 ≤32、适用条件 ≤120、资料 ≤32、说明 ≤2000 — verify: 越界 400
- [x] 5.4 `POST /api/skills` 201；`PUT /api/skills/{id}` 200；`DELETE` 204 无响应体、重复删除 404 — verify: 四种状态码正确，204 响应体为空
- [x] 5.5 权限与边界：写操作仅教师 403；同名 400；他班/不存在 404；删除同时解绑 — verify: 学生 403；删后绑定表无残留
- [x] 5.6 `GET /api/skills`、`GET /api/skills/{id}` 登录可读，返回 `source` 原文 — verify: 原文与提交逐字一致

## 6. 技能路由（T6）

- [x] 6.1 候选集只含绑定且已启用的技能 — verify: 未绑定/已停用技能即使命中关键词也不被选中
- [x] 6.2 切词：中英文逗号、顿号、分号、竖线 — verify: 四种分隔符混写可正确切分
- [x] 6.3 计分：命中一个计 1 分取最高；零分不选；同分取 id 小者；大小写折叠 — verify: 单元测试覆盖 0/1/2 分与并列
- [x] 6.4 单元测试 — verify: `go test ./internal/skill/...` 全部 ok

## 7. 检索范围与消息组装（T7）

- [x] 7.1 `retrieval` 新增 `SearchScoped`：关键字 SQL 与向量回表均追加 `material_id IN (...)` — verify: 绑定外材料不出现在结果中
- [x] 7.2 `subject_text` 过滤：只保留标题含该词的材料 — verify: 绑数学+语文、技能资料为「数学」时引用只含数学材料
- [x] 7.3 编号：先剔除空正文再连续编号 — verify: `[n]` 与引用列表第 n 项对应，无跳号
- [x] 7.4 系统消息五段固定顺序；资料不进系统消息；每条资料截断 800 字 — verify: 单元测试断言五段齐全、系统消息不含切片正文
- [x] 7.5 技能名含「解题引导」时追加硬性要求；未命中技能时用「请直接回答」 — verify: 两种分支分别包含对应句子
- [x] 7.6 边界句始终存在 — verify: 任意一轮系统消息均含边界句
- [x] 7.7 上下文窗口：末尾 6 条、每条 500 字、跳过空正文与非对话角色 — verify: 第 11 条后最早消息不再进入上下文

## 8. 会话与消息接口（T8）

- [x] 8.1 `GET /api/conversations` 只返回自己的、按 `updated_at` 倒序 — verify: 他人会话不出现
- [x] 8.2 `POST /api/conversations`：`agent_id` 必填且属本班已启用，否则 404；空正文或 `agent_id<1` 400；本班无可用 400；成功 201 — verify: 五种情形状态码正确
- [x] 8.3 `GET /api/conversations/{id}`：`user_id` + `class_id` 双过滤，他人/跨班/不存在同构 404 — verify: 三者响应完全一致
- [x] 8.4 标题：新建「新的提问」，首轮后改前 24 字；此后只刷新 `updated_at` — verify: 首轮标题变化，次轮不变而 `updated_at` 变化
- [x] 8.5 会话锁定智能体：请求体中的 `agent_id` 不参与选择 — verify: 传其它 `agent_id` 后会话行仍为原值

## 9. 流式接口（T9）

- [x] 9.1 `POST /api/conversations/{id}/messages` 返回 `text/event-stream`，事件 `meta` → 多次 `delta` → `done` — verify: `curl -N` 可见逐段到达
- [x] 9.2 响应头与 flush：`Content-Type`、`Cache-Control`、`Connection`、`X-Accel-Buffering: no`，每帧 `Flush()` — verify: 头齐全；去掉 flush 后退化为一次性到达
- [x] 9.3 `meta` 含 `status`、命中技能与引用列表，且为第一个事件 — verify: 首个事件即可读出出处
- [x] 9.4 `delta` 为增量：拼接等于落库全文 — verify: 与 `messages.content` 逐字一致
- [x] 9.5 `done` 返回助手消息编号；该消息含 `citations_json` 与 `skill_name` — verify: 按编号可查到
- [ ] 9.6 状态码分界线：首事件前 404/503；之后只发 `error`，HTTP 仍 200 — verify: 不存在会话 404 且不建流（**已验证**）；生成失败时状态码 200（**需人工注入故障，见验收记录**）
- [ ] 9.7 中途失败：已产出片段收尾落库 — verify: 人为中断后消息表有部分内容（**需人工注入故障，见验收记录**）
- [x] 9.8 无依据口径：仍调用模型，引用为空，正文不含 `[n]`，不等于固定文案 — verify: 问材料外内容时正文无编号且非空
- [x] 9.9 `content` 长度 2–500，越界在流开始前 400 — verify: 1 字符与 501 字符均 400
- [x] 9.10 Nginx：`proxy_buffering off`、`proxy_cache off` — verify: 去掉后文字一次性到达，加回后逐段到达

## 10. 接口文档（T10）

- [x] 10.1 全部处理函数补齐 swaggo 注解：`@ID`/`@Summary`/`@Tags`/`@Security`/`@Param`/`@Success`/`@Failure`/`@Router` — verify: `swag init` 无告警；`swagger.json` 的 `paths` 覆盖全部路由
- [x] 10.2 `bearerAuth` 安全方案声明；需登录操作引用它，白名单不引用 — verify: 解析 `swagger.json` 断言五个白名单接口无 `security`
- [x] 10.3 `GET /openapi.json` 返回生成的 spec — verify: JSON 可解析，`openapi` 为 3.0.x
- [x] 10.4 `GET /swagger/index.html` 提供官方 UI — verify: 浏览器可打开并试调一个需登录接口
- [x] 10.5 启动一致性自检：注解集合与注册路由集合比对，不等则失败并记日志 — verify: 人为删一条注解，服务启动失败且日志指出缺失路径
- [x] 10.6 运行期不依赖 CDN — verify: 断网后页面仍能加载

## 11. 前端（T11）

> 界面已在 Apply 阶段实现（`web/html/` 三页签：解题对话 / 智能体 / 技能），
> 但本课验收走接口层脚本，未做浏览器人工点验，故以下各项保持未勾选。
> 服务端一侧的权限已由脚本覆盖（学生写智能体/技能 403、学生只见已启用智能体）。

- [ ] 11.1 登录改为保存 `token` 到 `sessionStorage`，所有请求加 `Authorization: Bearer` — verify: 刷新页面仍在线；清空 `sessionStorage` 后回到登录页
- [ ] 11.2 提示词页：教师可编辑本班提示词，学生只读 — verify: 学生登录看不到保存按钮，直接调接口仍 403
- [ ] 11.3 智能体页：列表、新建/编辑、启停、绑定技能与材料（仅教师可见写入口） — verify: 学生看不到写按钮
- [ ] 11.4 技能页：粘贴/上传技能文件、编辑、启停、删除 — verify: 非法文件显示可读错误
- [ ] 11.5 对话页：选智能体新建会话 → 连续追问 → `fetch` 解帧逐段渲染 → 展示引用 — verify: 文字逐段出现；结束后可打开引用材料
- [ ] 11.6 北大红主题一致 — verify: 新页面主色与既有页面一致

## 12. 自动化测试工程（T12）

- [x] 12.1 `apitest/` 目录骨架 + `common/{client,db,assertx}.py` — verify: 能加载 `.env` 并连通 MySQL
- [x] 12.2 至少覆盖：登录、检索、提示词（GET/PUT）、技能（GET/POST）、创建会话、发送一条消息 — verify: 六个脚本均存在且可单独运行
- [x] 12.3 断言三类：状态码、响应内容、数据库回查 — verify: 每个脚本至少一条 DB 断言
- [x] 12.4 `run_all.py` 一键执行并生成 `report/index.html` — verify: 报告含每条用例结果与耗时
- [x] 12.5 报告与中间产物不入库 — verify: `.gitignore` 含 `apitest/report/`；`git status` 干净
- [x] 12.6 全部用例通过 — verify: `python apitest/run_all.py` 退出码 0

## 13. 验证与归档（T13）

- [x] 13.1 Go 单元测试：JWT、技能解析、路由计分、消息组装、截断与窗口 — verify: `go test ./...` 全部 ok
- [x] 13.2 端到端脚本 `scripts/verify_agent_chat.py` 覆盖本 change 三个 capability 的 Scenario — verify: 全部断言 PASS
- [x] 13.3 第 3、4 课回归（需同步改为 Bearer） — verify: `verify_e2e.py` 与 `verify_retrieval.py` 全 PASS
- [x] 13.4 `openspec validate add-agent-streaming-chat --strict` — verify: exit 0
- [x] 13.5 `down` 后 `up` 数据仍在（会话、消息、智能体、技能） — verify: 重启后历史会话可读
- [ ] 13.6 归档：`openspec archive add-agent-streaming-chat` — verify: 并入 `specs/{agent-chat,api-docs}/` 并更新 `specs/auth-upload/`。**待课程确认后执行**

## 验收记录（13.2）

执行环境：Windows + Docker Desktop，`WEB_PORT=8081`，四容器栈（web/api/db/qdrant）全部 Up；
嵌入与对话均接课程网关（`course-embedding` / `course-chat`）。

### 三份验收脚本

| 脚本 | 命令 | 结果 |
| --- | --- | --- |
| 第 3 课回归 | `BASE_URL=http://127.0.0.1:8081 python scripts/verify_e2e.py` | **PASS=45 FAIL=0** |
| 第 4 课回归 | `BASE_URL=http://127.0.0.1:8081 python scripts/verify_retrieval.py` | **PASS=72 FAIL=0** |
| 第 5 课新增 | `BASE_URL=http://127.0.0.1:8081 python scripts/verify_agent_chat.py` | **PASS=208 FAIL=0** |

`verify_agent_chat.py` 分 15 节，逐条对应三个 capability 的 Scenario：

| 节 | 覆盖 |
| --- | --- |
| 1 | 认证迁移：载荷仅注册声明、伪造/过期/alg=none、仅 Cookie 不足、改库角色立即生效、登出与再次登录吊销、白名单、`sessions` 退役 |
| 2 | 接口文档：OpenAPI 3.0.3、`bearerAuth` 为 http/bearer/JWT、路由无缺失无漂移、`operationId` 唯一、白名单无 `security`、4xx/5xx 齐备、Swagger UI 与本地资源 |
| 3 | 数据模型与种子：7 张表、级联规则、预置技能 `source_text` 与 `seed.go` 样板逐字比对 |
| 4–5 | 技能解析八类错误、长度上限、204 空响应体、重复删除 404、删除解绑 |
| 6 | 智能体六种情形、同构 404（未启用/他班/不存在逐字节一致）、提示词为空与清空、名称去空白、同名 400、跨班绑定 404 |
| 7–8 | 会话创建五种情形、本班无可用 400、归属隔离、列表倒序 |
| 9 | 技能路由：命中最高分、同分取小 id、零分不选、停用与未绑定不参与、大小写折叠、解绑后重放改变路由 |
| 10 | 检索范围：引用只含绑定材料、学科过滤、编号连续可回溯 |
| 11 | 流式：四个响应头、事件顺序、`delta` 逐段到达、`delta` 拼接等于落库全文、长度越界、404 不建流、会话锁定智能体 |
| 12 | 无依据口径：引用为空、正文无编号、不等于固定文案 |
| 13–14 | 消息回溯（正文/引用/技能名）、标题改写与 `updated_at`、`role` 枚举、多轮后消息全保留 |

### 自动化测试工程（T12）

`python apitest/run_all.py` → 8 个用例、**132 条断言全部通过**，生成 `apitest/report/index.html`（已忽略入库）：

| 用例 | 断言 |
| --- | --- |
| `POST /api/login` | 16 |
| `POST /api/search` | 14 |
| `GET /api/agents` | 12 |
| `PUT /api/agents/{id}`（提示词） | 14 |
| `GET /api/skills` | 15 |
| `POST /api/skills` | 23 |
| `POST /api/conversations` | 15 |
| `POST /api/conversations/{id}/messages` | 23 |

### 其他校验

- `cd api && go test ./...` → 10 个包全部 ok（新增 `openapi` 转换与 `cmd/server` 路由自检、`retrieval.Scope` 空范围、`chat.buildHistory` 窗口共 15 个用例）。
- `openspec validate add-agent-streaming-chat --strict` → valid。
- `docker compose down` → `up -d` 后历史会话仍可读（12 条消息、标题不变），13.5 通过。

### Apply 阶段修掉的两个规约偏差

1. **`/openapi.json` 当时是 Swagger 2.0**（`swagger: "2.0"` + `securityDefinitions.apiKey`），不满足 api-docs 规约要求的 OpenAPI 3.0 与 `bearerAuth`（http/bearer/JWT）。改为「注解仍是唯一来源，输出前做一次格式投影」：新增 `api/internal/openapi`，并把 swag 的 `apiKey` 声明翻译成标准 `http/bearer` 方案。同时补齐 `/openapi.json`、`/swagger/index.html` 两个操作与全部 5xx 说明。
2. **空绑定会越界检索**：`Scope{MaterialIDs: nil}` 在关键字与向量两条路径上都被当作「不限定材料」，导致未绑定任何材料的智能体能检索到全班内容。为 `Scope` 增加 `Scoped` 标记，`chat` 侧显式传 `Scoped: true`，空范围直接返回空结果（`retrieval` 单测覆盖）。

配套把 `main.go` 的内联注册改为路由表（`cmd/server/routes.go`），注册与启动自检读同一份数据，`checkRoutes` 在启动时比对「注解操作集合 = 注册路由集合」，不等即 `log.Fatalf` 并指出缺失项（`routes_test.go` 覆盖缺注解/多注解/`docPath` 映射三种情形）。

### 证据口径说明（10.4 / 10.5 / 10.6）

这三条的 verify 写法偏「人工」，实际用等价手段验证，记录口径如下：

- **10.4**：`/swagger/index.html` 返回 200，页面引用的 `swagger-ui.css`、`swagger-ui-bundle.js`、
  `doc.json` 全部本地可取；「页面内试调」发出的就是带 `Authorization: Bearer` 的同一套请求，
  已由 `verify_agent_chat.py` 与 `apitest` 覆盖，未做人工点击。
- **10.5**：未真的删注解重启，而是对 `checkRoutes`（`main.go` 启动时调用的同一个函数）
  写了三条单测：缺注解报错并指出 `DELETE /api/skills/{id}`、多注解报错并指出 `POST /api/ghost`、
  `docPath` 映射生效。
- **10.6**：静态确认页面无 jsdelivr/unpkg/cdnjs 引用，且全部资源从本项目 200 取回；
  未做真实断网。

### 未勾选项与人工步骤

- **9.6 / 9.7（流开始后的失败语义）**：需要人为制造生成故障，脚本无法稳定复现。步骤：
  1. 把 `.env` 的 `CHAT_BASE_URL` 临时指向不可达地址（如 `http://127.0.0.1:9/v1`），`docker compose up -d api`；
  2. 向已有会话提问：期望 HTTP **200** + 一条 `error` 事件（9.6 后半）；
  3. 若已推送部分 `delta` 再失败，`messages` 表应留下部分正文（9.7）；
  4. 复原 `.env` 并重启 api。
- **11.1–11.6（前端界面）**：需要浏览器人工点验。本课自动化验收只覆盖接口层，
  服务端权限已由脚本验证（学生写智能体/技能 403、学生只见已启用智能体、SSE 逐段到达）。
- **13.6（归档）**：规约写明「待课程确认后执行」，本课不归档。

### 已知副作用

- 智能体没有删除接口（规约未定义），验收脚本每次新建的智能体带时间戳名称，会留在库中；
  验收脚本上传的材料同样保留（与第 3、4 课脚本的做法一致）。为不破坏第 4 课「类囊体只在 B 班」的断言，
  第 5 课脚本改用「光系统Ⅱ」作为未绑定材料的专属词。
