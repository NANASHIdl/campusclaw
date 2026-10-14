# Design: add-traceable-vector-retrieval

## Context

第 3 课已完成登录、服务端会话、按班级隔离与材料上传入库（`add-auth-rbac-class-knowledge`，已 Apply 未归档）。材料正文在 `knowledge_entries.body_text`，但尚无检索能力。

本课在同一套三容器架构上追加检索链路：`web`(Nginx) + `api`(Go) + `db`(MySQL) + **新增 `qdrant`（向量库）**。可验收行为见 `specs/knowledge-retrieval/spec.md`。流式长对话属第 5 课，不在本 change 范围。

## Goals / Non-Goals

**Goals:**

- 上传后把正文切为可追溯的切片，并建立向量索引。
- 三种检索模式（keyword / vector / hybrid），默认 hybrid。
- 每条结果可回溯至「材料 + 切片序号 + 字符区间 + 摘录」。
- 有依据才生成回答，无依据明确返回未找到且不调用生成模型。
- 检索与上传共用同一条班级边界；跨班检索表现为无命中。
- 不新增 Go 模块依赖（Qdrant 与网关均走标准库 `net/http`）。

**Non-Goals:**

- 见 `proposal.md` Non-goals（流式对话、重排序、编排框架、多模态、生产 HA 等）。

---

## 系统架构与新增模块

```
浏览器（不可信）
   │  只访问本站 :8080，不持有网关密钥
   ▼
web · Nginx ── 静态 SPA；/api、/health 反向代理
   │  信任边界
   ▼
api · Go
   ├─ 认证模块（请求先经此处，解析会话 → role / class_id）
   ├─ 材料模块（文件、正文、切片落库）
   ├─ 检索模块（切分 · 检索 · 回答编排）
   │      ├─→ db · MySQL（会话、正文、切片文本）
   │      ├─→ qdrant（仅向量与编号，不含正文；不映射宿主机端口）
   │      ├─→ 嵌入网关（文本 → 向量）
   │      └─→ 对话网关（有切片时才调用）
   └─ 对话模块（依据切片撰写简短回答）
```

- 客户端无法直连 Qdrant，也无法直连两类模型网关；它们只接受 Go 中检索模块发起的调用。
- 班级编号仅从登录会话读取。

### 模块职责边界

| 模块 | 职责 | 不在职责内 |
| --- | --- | --- |
| 切分模块 | 把 `body_text` 按策略切为切片；输出文本、序号与偏移 | 不改写原文；不调用嵌入；不写 Qdrant |
| 嵌入模块 | 文本 → 一条向量（入库与检索共用） | 关键字检索不调用它 |
| 检索模块 | 三类检索、过滤、RRF 融合、组装上下文 | 不向客户端暴露向量分量 |
| 对话模块 | 依据给定切片撰写带 `[1]` 标注的简短回答 | 不自行访问数据库或向量库；不检索其他班级 |

---

## 技术选型（Decision 1）

| 层次 | 选择 | 理由 |
| --- | --- | --- |
| 向量库 | **Qdrant**（Compose 第四容器，单节点） | 课件选型；集合 `campusclaw_chunks`，余弦距离 |
| Qdrant 客户端 | **标准库 `net/http` 调 REST** | 不引入 `qdrant-go` 依赖，vendor 保持最小 |
| 关键字检索 | MySQL `FULLTEXT ... WITH PARSER ngram`（token=2） | 中文必须 ngram 或分词器，否则全文索引无法切词 |
| 嵌入 | 可配置网关（兼容 OpenAI `/embeddings`） | 课件选型；密钥仅服务端持有 |
| 嵌入回退 | **本地确定性特征哈希 → 2048 维，L2 归一化** | 未配置网关时链路仍可离线运行与验收 |
| 对话 | 可配置网关（兼容 OpenAI `/chat/completions`） | 课件选型 |
| 对话回退 | **抽取式回答**（从命中切片中摘取最相关句并标注出处） | 无网关时仍可验收「有依据才生成」 |
| 融合 | RRF（Reciprocal Rank Fusion），`k = 60` | 课件选型；融合名次而非分数 |

> 回退实现的意义是**可运行、可验收**，不是替代模型能力。本地嵌入是词面近似，不具备语义泛化；生产环境应配置课程网关。这一取舍已写入 Non-goals。

---

## 切分（Decision 2）

切片是检索的最小单位。三种策略作用于同一份正文：

| 策略 | 规则 |
| --- | --- |
| `auto`（默认） | 最大 800 字、重叠 80 字（10%）；优先在空行、换行、句号处断开。请求中另行填写的长度与预处理参数**不生效** |
| `custom` | 最大长度 100–2000 字，重叠比例 0%–50%；无断点处按最大长度强制截断。可选预处理：移除 URL 与邮箱、把连续空白折叠为单个空格 |
| `hierarchy` | 按 `#`/`##`/`###` 分章，标题保留在该章切片内；某章过长时再按自动窗口规则切分 |

- 上传与重建索引**使用同一组参数**；未指定策略时按 `auto` 处理；种子材料启动时补齐索引同样采用 `auto`。
- **预处理不改写原文**：`knowledge_entries.body_text` 仍为上传原文。此时切片偏移量相对于预处理后文本计算，**不再视作原文件中的字符下标**。
- 切分模块不调用嵌入服务，也不写 Qdrant；这两步在其返回之后由检索模块继续。

---

## 入库流程（Decision 3）

材料模块先完成第 3 课的两表事务（`materials` + `knowledge_entries`），**提交之后**再由检索模块做索引：

```
1 保存原文文件与 knowledge_entries.body_text   ← 第 3 课事务已提交
2 切分：按本次策略写入 knowledge_chunks（先删除旧切片，再写入新记录）
3 嵌入：每个切片转换为一条向量
4 写入 Qdrant：向量主键 = 切片主键
```

- 向量库 payload 仅含 `class_id`、`material_id`、`knowledge_entry_id`、`chunk_id`、`chunk_index`，**不含正文**。
- **失败隔离**：嵌入失败时材料记录仍然保留，对应切片标记 `index_status = failed`，不写入不完整的向量数据。原文仍在，仅这批切片无法用于向量检索。
- 关键字检索只取 `index_status = ready` 的切片。

---

## 三种检索模式（Decision 4）

| 模式 | 访问的组件 | 排序方式 |
| --- | --- | --- |
| `keyword` | 仅 MySQL 全文索引；不调用嵌入，也不访问 Qdrant | 按全文相关度由高到低，名次转 RRF |
| `vector` | 问句先嵌入 → Qdrant 按 `class_id` 过滤 → 余弦 < 0.35 丢弃 → 以向量主键回 MySQL 取正文 | 按余弦相似度由高到低，名次转 RRF |
| `hybrid`（默认） | 同时执行上述两条路径 | 各路先独立过滤，再按名次执行 RRF（`k=60`）；缺席的一路不贡献分数 |

- 三种模式的 `score_final` 统一为 RRF 名次分：单路命中为 `1/(60+rank)`，两路命中为两者之和。
- 摘录**始终取自 MySQL 的 `chunk_text`**；Qdrant 中不保存正文。
- 空查询（去除空白后不足 2 字符）返回 400。
- **Qdrant 不可用时**：关键字检索仍可返回结果；向量与混合模式返回 503，**不编造相似度分数**。

---

## 问答与引用标注（Decision 5）

```
POST /api/ask {q}
 ├─ 以混合模式取本班前 4 条切片
 ├─ 无命中 → 返回 {status:"no_evidence", answer:"资料中未找到相关内容", citations:[]}
 │           此路径不访问对话网关（避免模型凭参数记忆勉强作答）
 └─ 有命中 → 把「材料标题 + 切片序号 + 切片正文」与本轮提问交给对话网关
              → 回答中以 [1]、[2] 标注，顺序与 citations 一致
```

- 传递给对话模块的输入：材料标题、切片序号、切片正文、用户最新一句提问，最多前 4 条。
- **不传递给它的内容**：向量分量与 Qdrant 原始点数据、其他班级的切片、客户端自行构造的 `system` 消息（一律丢弃）。
- 对话模块的输入由检索模块组装；它不得自行访问数据库或向量库检索其他材料。
- 本课不实现流式输出。

---

## 数据模型（Decision 6）

新增第六张表；租户列 `class_id` 非空且带索引：

```sql
knowledge_chunks(
  id                 BIGINT PK AUTO_INCREMENT,
  knowledge_entry_id BIGINT FK→knowledge_entries,
  material_id        BIGINT FK→materials,
  class_id           INT    NOT NULL,
  chunk_index        INT    NOT NULL,
  chunk_text         MEDIUMTEXT NOT NULL,
  start_offset       INT    NOT NULL,
  end_offset         INT    NOT NULL,
  index_status       ENUM('ready','failed') NOT NULL DEFAULT 'ready',
  chunk_strategy     VARCHAR(16) NOT NULL,
  created_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  INDEX idx_chunks_class (class_id),
  INDEX idx_chunks_material (material_id),
  FULLTEXT KEY ft_chunks_text (chunk_text) WITH PARSER ngram
)
```

- **关联方式**：`knowledge_chunks.id` = Qdrant 向量主键 = `payload.chunk_id`。检索时向量库先返回向量主键，再用它回 MySQL 取 `chunk_text`。
- 不把整段原文重复存入 Qdrant。

---

## 检索侧的班级隔离（Decision 7）

隔离在服务端实现，前端隐藏入口不算满足。两条路径机制不同，**仅在单侧过滤并不充分**：

| 路径 | 怎么限定 |
| --- | --- |
| 关键字 | MySQL 查询条件包含 `class_id = 会话班级`，且仅检索 `index_status = ready` |
| 向量 | Qdrant 查询须带 `class_id` 过滤；返回向量主键后，**再用同一班级条件回 MySQL 取回正文** |

- 即便向量库漏加过滤条件，回表时的班级约束仍应将其他班级的记录排除在外。
- 请求头 / 查询参数 / JSON 正文中另行携带的班级编号，解析后一律丢弃。
- **跨班检索对外表现为无命中**：HTTP 200 + 空命中列表（或问答的「资料中未找到相关内容」）。不以 403 或 404 暗示该资料属于其他班级。
- 按材料标识打开详情仍沿用第 3 课规则：跨班与不存在均返回**同构 404**。

---

## 容器编排与数据卷（Decision 8）

```
services:
  web    : nginx, ports ["${WEB_PORT:-8080}:80"], depends_on api
  api    : go 服务, 不映射端口, volumes [uploads:/app/uploads], env_file .env
  db     : mysql:8, 不映射端口, volumes [db-data:/var/lib/mysql]
  qdrant : qdrant/qdrant, 不映射端口, volumes [qdrant-data:/qdrant/storage]
volumes:
  db-data, uploads, qdrant-data    # down 保留；down -v 才删除
```

- **仅 `web` 发布宿主机端口**；`api`、`db`、`qdrant` 仅存在于容器网络内部。
- `api` 启动时等待 `db` 就绪并重试连接 Qdrant；Qdrant 未就绪不阻塞关键字检索。

---

## Decisions（摘要）

| # | 决策 | 备选 |
| --- | --- | --- |
| 1 | Qdrant + 标准库 REST 客户端 | qdrant-go（引入依赖，vendor 变大） |
| 2 | 嵌入网关可配置 + 本地确定性回退 | 仅网关（无凭据时链路不可运行） |
| 3 | 对话网关可配置 + 抽取式回退 | 仅网关（同上） |
| 4 | `knowledge_chunks` + MySQL ngram 全文索引 | 仅向量库（无法做纯关键字检索） |
| 5 | RRF `k=60` 融合名次 | 分数加权求和（两路分数量纲不可比） |
| 6 | 向量阈值 0.35 硬过滤 | 全量返回（低相关切片凑数） |
| 7 | 索引失败标记 `failed`，材料保留 | 回滚材料（与课件「原文仍然存在」不符） |
| 8 | 跨班检索 = 200 空命中 | 403/404（泄露资源是否存在） |
| 9 | 上传后同步建立索引 | 异步队列（本课单副本，无需引入队列） |
| 10 | `EMBEDDING_DIM` 作为 `VECTOR_DIM` 的别名 | 只认单一变量名（网关文档惯用前者） |

## Risks / Trade-offs

- [本地回退嵌入无语义泛化] → 词面近似的向量检索会偏向字面重叠；已写入 Non-goals，并在页面与 README 注明「配置网关后生效」。
- [切换嵌入实现后索引失效] → 本地回退与课程网关的向量空间**完全不同**，切换后旧向量不可用。必须重建索引（清空 `qdrant-data` 卷后重启，或逐份材料 `reindex`）。已在 `.env.example` 与 README 显式警示。
- [网关不可达时的上传行为] → 索引失败会把切片标记 `failed` 并记日志，材料本身仍保留可访问（与课件「原文仍然存在」一致）。
- [同步索引拖慢上传] → 上传接口在事务提交后同步切分与嵌入；材料已入库，索引失败不影响材料可用性。
- [MySQL ngram 需服务端参数] → `ngram_token_size` 默认即为 2，与课件要求一致；建表时显式声明 `WITH PARSER ngram`。
- [Qdrant 首启较慢] → api 启动重试连接；不可用时降级为关键字检索。
- [同构响应增加排障难度] → 真实原因（`cross_class` / `not_found`）仅写服务端日志。

## Migration Plan

- 新增 `knowledge_chunks` 表由 `Migrate` 幂等创建，无需迁移旧数据。
- 启动时检测「有 `knowledge_entries` 但无 `knowledge_chunks`」的材料并补齐索引（采用 `auto` 策略），使第 3 课已上传的材料同样可被检索。
- 第 3 课的三容器与接口行为保持不变。

## Open Questions

（无。）
