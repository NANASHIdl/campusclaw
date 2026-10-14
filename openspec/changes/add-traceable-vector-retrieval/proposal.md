# Change: add-traceable-vector-retrieval

## Why

第 3 课已实现登录、角色校验、按班级隔离与材料上传入库，材料正文已落在 `knowledge_entries.body_text`。但材料只能按标题浏览，教师与学生都无法用一句自然语言定位到具体片段，知识库的价值无法释放。

教研材料的特点决定了检索必须**可回溯**：回答与检索结果都要能指回「哪份材料的哪一个切片、第几个字符到第几个字符」，否则无法核对、也无法用于教学。因此本变更在同一套系统上增加**可追溯的知识库检索**：以自然语言在本班材料中定位相关切片，标注出处；资料中没有依据时明确返回未找到，**不得编造出处**。

本变更对应课件第 4 课，change 名由课件指定。

## What Changes

- **切分（chunking）**：上传入库后把正文切为切片写入 `knowledge_chunks`。支持三种策略：`auto`（默认，约 800 字、重叠约 10%，优先在空行/换行/句号断开）、`custom`（最大长度 100–2000、重叠 0%–50%，可选预处理）、`hierarchy`（按 Markdown `#`/`##`/`###` 分章）。原始文件与 `body_text` **保持原样**，切分不改写原文。
- **嵌入（embedding）**：切片与问句均需转换为固定维度向量。仅在服务端调用嵌入网关；**未配置网关时使用本地确定性嵌入**（字符 n-gram 特征哈希 → 2048 维、L2 归一化），保证离线可运行与可验收。网关密钥不下发到浏览器。
- **向量库（Qdrant）**：作为第四个容器加入 Compose，**不映射宿主机端口**。集合 `campusclaw_chunks`，余弦距离；payload 仅含 `class_id`/`material_id`/`knowledge_entry_id`/`chunk_id`/`chunk_index`，**不含正文**。向量主键 = `knowledge_chunks.id`。
- **三种检索模式**：`keyword`（仅 MySQL 全文索引，ngram 分词）、`vector`（问句嵌入 → Qdrant 按 `class_id` 过滤 → 余弦低于 0.35 丢弃 → 回 MySQL 取正文）、`hybrid`（默认，两路各自排名后用 **RRF（k=60）** 融合名次）。
- **可追溯出处**：每条命中至少给出材料标题、切片序号、字符区间与一段摘录；摘录一律取自 MySQL 的 `chunk_text`，不取自向量库。
- **问答与引用标注**：`POST /api/ask` 先以混合模式取本班前 4 条切片；**有切片才调用对话网关**，回答中以 `[1]`、`[2]` 标注出处且顺序与出处列表一致；**无切片时不调用模型**，直接返回「资料中未找到相关内容」且 `citations` 为空。
- **检索侧的班级隔离**：`class_id` 仅取自登录会话，请求头/查询参数/正文中自带的班级编号一律丢弃；关键字与向量**两条路径都须**携带班级条件，回表时再次核对。跨班检索对外表现为 **200 + 空命中列表**（不以 403/404 暗示资源属于其他班级）；按材料标识打开详情仍沿用第 3 课规则——跨班与不存在均返回同构 404。
- **重建索引**：教师可按当前请求指定的策略重建某份材料的切片与向量（先删旧切片与旧向量，再写入新的）。切换策略不会自动重切已有材料。
- **失败隔离**：嵌入失败时材料记录仍然保留，对应切片标记 `index_status=failed`，不写入不完整的向量数据。Qdrant 不可用时关键字检索仍可返回结果；向量与混合模式返回 503，**不编造相似度分数**。
- **接口新增**：`POST /api/search`、`POST /api/ask`、`GET /api/materials/{id}/vectors`、`POST /api/materials/{id}/reindex`。
- **数据模型**：新增 `knowledge_chunks` 表（租户列 `class_id` 非空且带索引，`chunk_text` 建 ngram 全文索引）。

## Capabilities

### New Capabilities

- `knowledge-retrieval`：切片策略与可追溯切片、嵌入与向量库关联、关键字/向量/混合三种检索、RRF 融合、阈值过滤、引用标注与无依据处理、检索侧班级隔离、重建索引与失败隔离等可验收行为。

### Modified Capabilities

（无——`openspec/specs/` 尚无主规格；第 3 课的 change 仍位于 `changes/` 下未归档。）

## Impact

- 新增 `api/internal/chunk`、`api/internal/embed`、`api/internal/vector`、`api/internal/retrieval`、`api/internal/qa` 五个包。
- `api/internal/db` 新增 `knowledge_chunks` 表与 ngram 全文索引；启动时为缺少切片的种子材料补齐索引。
- `api/internal/materials` 新增向量查看与重建索引两个接口；上传流程在事务提交后追加切分与嵌入。
- `api/internal/config` 新增 Qdrant 地址/集合、嵌入网关与对话网关（地址、密钥、模型）、向量维度、阈值、RRF 常数、检索返回条数等配置项。
- `docker-compose.yml` 新增 `qdrant` 服务与 `qdrant-data` 命名卷；`.env.example` 与 `README.md` 同步。
- 前端 `web/html` 新增知识检索页与知识问答页，材料详情增加切片与向量视图。
- 不新增 Go 模块依赖：Qdrant 与两类网关均通过标准库 `net/http` 调用。

## Non-goals（非目标）

本 change **明确不做**以下内容（避免范围膨胀）：

- **流式长对话**：不做流式输出、多轮会话持久化、会话历史管理（课件注明属第 5 课）。
- **重排序**：不引入交叉编码器重排序。
- **编排框架**：不引入 LangChain / LlamaIndex / Dify 一类框架，切分、嵌入与检索由 Go 服务自行完成。
- **非文本材料**：不做 PDF / Word / 图片解析、OCR、多模态嵌入。
- **生产运维**：不做分布式 Qdrant 集群、多副本、跨副本共享限流；Qdrant 以单节点容器运行。
- **真实模型能力**：本地回退嵌入是**确定性词面近似**，不具备语义泛化能力；其存在意义是让链路离线可运行、可验收，生产环境应配置课程网关。
- **管理后台**：不做切片的手工增删改、批量重建全部材料的图形化入口。
- **前端替代服务端安全**：不以隐藏检索入口或按钮作为班级隔离手段。
