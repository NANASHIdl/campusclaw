# CampusClaw

- **价值**：面向中小学的教研智能体，把分散在群文件与网盘中的教研材料收进可检索、可复用、**可追溯**的知识库。
- **场景**：教师登录后上传并管理本班材料，学生只读查看与下载本班材料；教师与学生都能用自然语言检索本班材料并获得带出处的回答；班级是数据边界。
- **不做**：不做流式长对话、重排序、编排框架（LangChain 等）、PDF/Word 解析、注册与找回密码、SSO、生产级高可用。

## 架构（第 5 课）

```
浏览器 ──只访问 :8080──▶ web · Nginx ──/api、/health 反代──▶ api · Go
                         静态 SPA                            认证 · 授权 · 隔离 · 上传校验
                                                             检索模块：切分 · 检索 · 回答编排
                                                                ├──SQL──▶ db · MySQL（用户、智能体、会话与消息、正文、切片）
                                                                ├────────▶ qdrant（仅向量与编号，不含正文）
                                                                ├────────▶ 嵌入网关（文本 → 向量）
                                                                └────────▶ 对话网关（有切片时才调用）
```

| 服务 | 技术 | 对外端口 | 职责 |
| --- | --- | --- | --- |
| `web` | Nginx + 静态 SPA | **8080:80（唯一）** | 返回静态页面；把 `/api`、`/health` 原样反代给 api；**不**参与鉴权，**不**暴露上传目录 |
| `api` | Go（标准库 `net/http`） | 不映射 | 认证、授权、班级隔离、上传校验、下载鉴权、切分、嵌入、检索、问答 |
| `db` | MySQL 8 | 不映射 | 持久化业务表与令牌吊销表 `revoked_tokens`；`chunk_text` 建 ngram 全文索引 |
| `qdrant` | Qdrant v1.12 | 不映射 | 向量库：集合 `campusclaw_chunks`，余弦距离；**只存向量与编号** |

**信任边界在 Go**：进入 api 之后的每一次请求都要重新核对令牌、角色与班级；客户端无法直连 Qdrant 与两类模型网关。

## 快速开始（Docker Compose）

```bash
cp .env.example .env      # 填写 MYSQL_* / DB_* / SESSION_SECRET
docker compose up --build
```

打开 <http://localhost:8080>。健康检查：`curl http://localhost:8080/health`。

> 若 8080 已被占用，在 `.env` 中设置 `WEB_PORT=8081` 后重新 `docker compose up -d`，访问端口随之改变。

### 预置账号

| 用户名 | 口令 | 角色 | 班级 |
| --- | --- | --- | --- |
| `teacher_a` | `teach123` | teacher | A |
| `student_a1` | `stu123` | student | A |
| `student_b1` | `stu123` | student | B |

> 口令明文只出现在 `.env` 中，启动时计算哈希写入数据库；库中不保存明文。

### 数据卷

```bash
docker compose down       # 容器删除，数据卷保留 → 账号、材料、切片与向量仍在
docker compose up -d
docker compose down -v    # 删除数据卷 → 数据清空（仅用于主动重置）
```

## 检索（第 4 课）

### 三种模式

| 模式 | 访问的组件 | 排序 |
| --- | --- | --- |
| `keyword` | 仅 MySQL 全文索引（ngram 分词） | 全文相关度由高到低 |
| `vector` | 问句嵌入 → Qdrant 按 `class_id` 过滤 → 余弦 < 0.35 丢弃 → 回表取正文 | 余弦相似度由高到低 |
| `hybrid`（默认） | 同时执行两条路径 | 两路各自排名后用 **RRF（k=60）** 融合名次 |

单路命中的合成分是 `1/(60+名次)`，两路都命中则是两者之和——**融合的是名次，不是分数**。

### 可追溯

每条结果都给出材料标题、切片序号、字符区间与一段摘录。摘录**始终取自 MySQL 的 `chunk_text`**，向量库中不保存正文。

### 切分策略

| 策略 | 规则 |
| --- | --- |
| `auto`（默认） | 最大 800 字、重叠 80 字；优先在空行、换行、句号处断开 |
| `custom` | 最大长度 100–2000 字、重叠 0%–50%；可选移除 URL/邮箱、折叠空白 |
| `hierarchy` | 按 Markdown `#`/`##`/`###` 分章，标题保留在章内 |

> 预处理只作用于待切分文本，`knowledge_entries.body_text` 保持上传原文。

### 嵌入与对话网关

`EMBEDDING_BASE_URL` 与 `CHAT_BASE_URL` **留空时自动使用本地回退**：

- 嵌入回退：字符 n-gram 特征哈希 → 2048 维、L2 归一化。**确定性**、可离线运行，但只是词面近似，不具备语义泛化能力。
- 对话回退：从命中切片中**抽取**最相关的一句并标注出处，**不调用任何模型**。

配置课程网关后即切换为真实模型（密钥仅服务端持有）：

```bash
EMBEDDING_BASE_URL=https://ai-gateway.devops.hello1023.com/v1
EMBEDDING_MODEL=course-embedding
EMBEDDING_DIM=2048          # 不是请求参数，用于建库与维度核对

CHAT_BASE_URL=https://ai-gateway.devops.hello1023.com/v1
CHAT_MODEL=course-chat
CHAT_TIMEOUT_SECONDS=60
```

Base URL **只写到 `/v1`**，程序自行拼 `/embeddings` 与 `/chat/completions`；向量接口只提交 `model` 与 `input`；单次请求不超过 20MB。

> ⚠️ **切换嵌入实现（本地回退 ⇄ 网关）或更换嵌入模型后，向量空间完全改变，必须重建索引**：
> `docker compose down -v && docker compose up --build -d`，或由教师对每份材料调用 `POST /api/materials/{id}/reindex`。
> 仅切换对话模型**不影响**索引。

## API

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| POST | `/api/login` | 登录；返回 `{"token","user"}`（HS256 JWT，前端存 `sessionStorage`） |
| POST | `/api/logout` | 登出，把当前令牌的 `jti` 写入吊销表 |
| GET | `/api/me` | 当前用户；仅用于前端决定是否渲染上传入口 |
| GET | `/api/materials` | 本班材料列表（班级按令牌 `sub` 回查） |
| GET | `/api/materials/{id}` | 材料详情 |
| GET | `/api/materials/{id}/file` | 下载（读取文件前完成认证与班级校验） |
| POST | `/api/materials/upload` | 教师上传（学生 403）；入库后自动切分、嵌入、写向量库 |
| GET | `/api/materials/{id}/vectors` | 查看某材料的切片与向量（含 MySQL 主键与 payload 编号的对应关系） |
| POST | `/api/materials/{id}/reindex` | 教师按指定策略重建切片与向量 |
| POST | `/api/search` | 检索，请求体 `{"q": "...", "mode": "keyword\|vector\|hybrid"}` |
| POST | `/api/ask` | 问答，请求体 `{"q": "..."}` |
| GET | `/api/agents` | 智能体列表；教师见本班全部，学生只见已启用 |
| POST | `/api/agents` | 教师新建智能体（名称、提示词、启用、绑定技能与材料） |
| GET | `/api/agents/{id}` | 智能体详情；学生读未启用/他班/不存在同构 404 |
| PUT | `/api/agents/{id}` | 教师修改智能体（整份覆盖；提示词可清空，回退默认角色） |
| GET | `/api/skills` | 技能列表（登录可读，含技能文件原文） |
| POST | `/api/skills` | 教师用技能文件新建技能 |
| GET | `/api/skills/{id}` | 技能详情 |
| PUT | `/api/skills/{id}` | 教师用技能文件改写技能 |
| DELETE | `/api/skills/{id}` | 教师删除技能（`204` 无响应体，同时解绑） |
| GET | `/api/conversations` | 我的会话（按 `updated_at` 倒序） |
| POST | `/api/conversations` | 按本班已启用智能体新建会话 |
| GET | `/api/conversations/{id}` | 会话与全部消息 |
| POST | `/api/conversations/{id}/messages` | 提问，SSE 返回 `meta` → `delta`… → `done` |
| GET | `/openapi.json` | OpenAPI 3.0 描述（由处理函数注解生成），无需认证 |
| GET | `/swagger/index.html` | 官方 Swagger UI（读 `/openapi.json`），无需认证 |
| GET | `/health` | 存活探测，无需认证 |

**响应约定**

- 未认证 → `401 {"error":"unauthorized"}`
- 登录失败（账号不存在 / 口令错误 / 限流）→ `401 {"error":"invalid_credentials"}`，三者**同构**
- 角色不足（学生上传、学生重建索引）→ `403 {"error":"forbidden"}`
- 跨班访问与记录不存在 → **同构** `404 {"error":"not_found"}`
- 空查询（不足 2 字符）→ `400 {"error":"查询至少需要 2 个字符"}`
- 提问 `content` 去空白后不足 2 或超过 500 字符 → `400`（在开始 SSE 之前返回）
- 他人会话、跨班会话与不存在的会话 → **同构** `404`
- 向量库不可用且模式为 `vector`/`hybrid` → `503 {"error":"向量库不可用"}`（`keyword` 仍可用）

**检索侧的班级隔离**：班级只取自令牌（按 `sub` 回查 `users`），请求中自带的 `class_id` 一律丢弃；关键字与向量两条路径都带班级条件，回表时再次核对。**跨班检索表现为 `200` + 空命中列表**，不以 403/404 暗示资源归属。

## 本地开发（不使用 Docker）

```bash
# 需要本地 MySQL 与 Qdrant；先导出环境变量（或使用 .env 后 export）
export DB_HOST=127.0.0.1 DB_PORT=3306 DB_NAME=campusclaw DB_USER=... DB_PASSWORD=...
export SESSION_SECRET=... SEED_TEACHER_PASSWORD=teach123 SEED_STUDENT_PASSWORD=stu123
export UPLOAD_DIR=./uploads
export QDRANT_URL=http://127.0.0.1:6333

cd api && go run ./cmd/server      # 监听 :8080
go test ./...                      # 单元测试
```

## 验收

两层验证都要通过才算实现合规：

```bash
# 第一层：规约结构（当前 4 个 capability）
openspec validate --all --strict

# 第二层：逐条执行 spec 场景（需栈已启动）
python scripts/verify_e2e.py                                       # 第 3 课：45 项
BASE_URL=http://127.0.0.1:8081 python scripts/verify_retrieval.py  # 第 4 课：72 项
BASE_URL=http://127.0.0.1:8081 python scripts/verify_agent_chat.py # 第 5 课：208 项
node scripts/verify_frontend.mjs                                   # 前端点验：42 项（先起 Chrome 调试端口）

# 第三层：接口测试工程（一接口一脚本，出 HTML 报告）
python apitest/run_all.py

# 单元测试
cd api && go test ./...
```

## 目录结构

```
api/                      Go 服务
  cmd/server/main.go      路由装配与启动
  internal/config         环境变量配置（缺失必需项即启动失败）
  internal/db             MySQL 连接、建表、种子
  cmd/server/routes.go    路由表：注册与启动自检读同一份数据
  internal/auth           口令哈希、认证中间件、统一失败响应、限流
  internal/jwtauth        HS256 令牌签发/校验与吊销表
  internal/agent          智能体与两份绑定（技能、材料）
  internal/skill          技能文件解析、存取与路由计分
  internal/conversation   会话与消息
  internal/chat           一次回答的编排与 SSE 推送
  internal/openapi        注解产物 → OpenAPI 3.0 投影
  docs/                   swag 生成的文档（入库，供离线构建）
  internal/materials      列表、详情、下载、上传、向量查看、重建索引
  internal/chunk          三种切分策略与预处理
  internal/embed          嵌入网关 + 本地确定性回退
  internal/vector         Qdrant REST 客户端（仅标准库）
  internal/index          切分 → 嵌入 → 写向量库的索引流水线
  internal/retrieval      关键字/向量/混合检索与 RRF 融合
  internal/qa             先检索再生成的问答与引用标注
  internal/httpx          统一 JSON 响应与错误文案
web/                      Nginx + 静态 SPA
  conf/nginx.conf         静态服务 + /api 反代
  html/                   index.html / app.js / style.css
openspec/                 规约（长期真相源 + 每次变更的四件套）
scripts/                  verify_e2e.py（第 3 课）、verify_retrieval.py（第 4 课）、
                          verify_agent_chat.py（第 5 课）、verify_frontend.mjs + cdp.mjs（前端点验）
apitest/                 接口测试工程：cases/api/**（一接口一脚本）、run_all.py、report/（忽略）
docker-compose.yml        web / api / db / qdrant 四服务与命名卷
```

## 规约（SDD）

- `openspec/specs/` —— 长期真相源（归档后并入）
- `openspec/changes/<change>/` —— 每次变更的 proposal / design / spec / tasks 四件套

当前变更：

| change | capability | 状态 |
| --- | --- | --- |
| `add-auth-rbac-class-knowledge` | `auth-upload` | 已归档（2026-10-21） |
| `add-traceable-vector-retrieval` | `knowledge-retrieval` | 已归档（2026-10-21） |
| `add-agent-streaming-chat` | `agent-chat`、`api-docs`、`auth-upload`（MODIFIED） | 已归档（2026-10-27） |

当前主规约：`openspec list --specs` → 4 个 capability（`agent-chat` 12 条、`api-docs` 3 条、
`auth-upload` 9 条、`knowledge-retrieval` 11 条 Requirement）。

归档：两层验证（结构 + 逐条场景）都通过后执行 `openspec archive <change>`。
