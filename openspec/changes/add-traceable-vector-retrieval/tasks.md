# Tasks: add-traceable-vector-retrieval

> 第 4 课 Apply：实现顺序 数据 → 切分 → 嵌入 → 向量库 → 检索 → 问答 → 前端 → 验证。
> 每条任务附 `verify:`，完成后勾选。

## 1. 数据模型与配置（T1）

- [x] 1.1 新增 `knowledge_chunks` 表：租户列 `class_id` 非空且带索引，`chunk_text` 建 `FULLTEXT ... WITH PARSER ngram`，含 `index_status`、`chunk_strategy` — verify: `SHOW TABLES` 返回 6 张表；`SHOW INDEX` 含 `idx_chunks_class` 与全文索引
- [x] 1.2 `config` 新增 Qdrant 地址/集合/维度、嵌入网关与对话网关（地址、密钥、模型）、相似度阈值、RRF 常数、检索条数 — verify: `.env.example` 列出全部新增变量名
- [x] 1.3 启动时为「有 `knowledge_entries` 但无 `knowledge_chunks`」的材料补齐索引（`auto` 策略） — verify: 重启后第 3 课上传的材料也可被检索到

## 2. 切分模块（T2）

- [x] 2.1 实现 `auto` 策略：最大 800 字、重叠 80 字，优先空行/换行/句号断开 — verify: 长正文切片均 ≤ 800 字且相邻重叠
- [x] 2.2 实现 `custom` 策略：长度 100–2000、重叠 0%–50%，越界请求被拒绝；可选移除 URL/邮箱与折叠空白 — verify: 越界参数返回 4xx；启用折叠后原文 `body_text` 不变
- [x] 2.3 实现 `hierarchy` 策略：按 `#`/`##`/`###` 分章，标题保留在章内，超长章再按窗口切 — verify: 含标题正文的切片以标题开头
- [x] 2.4 单元测试覆盖三策略与预处理 — verify: `go test ./internal/chunk/...` 全部 ok

## 3. 嵌入与向量库（T3）

- [x] 3.1 嵌入模块：优先调用配置的网关；未配置时使用本地确定性嵌入（字符 n-gram 特征哈希 → 2048 维，L2 归一化） — verify: 同一文本两次嵌入结果逐位相同；维度为 2048
- [x] 3.2 Qdrant REST 客户端（标准库 `net/http`）：确保集合存在、upsert、按 `class_id` 过滤检索、按条件删除、按主键取点 — verify: 不新增 Go 模块依赖（`go.mod` 无新增）
- [x] 3.3 入库：切分后逐切片嵌入并 upsert，payload 仅含 5 个编号字段 — verify: 查看向量时 payload 不含正文；向量主键 = `knowledge_chunks.id`
- [x] 3.4 失败隔离：嵌入失败时切片标记 `failed`、材料保留、不写入不完整向量 — verify: 人为使网关失败后材料仍可访问

## 4. 检索模块（T4）

- [x] 4.1 `keyword`：MySQL `MATCH ... AGAINST`（ngram），仅取 `index_status='ready'` 且带会话班级条件 — verify: 原词命中；命中项无向量分数字段
- [x] 4.2 `vector`：问句嵌入 → Qdrant 按班级过滤 → 余弦 < 0.35 丢弃 → 回表取正文（再次带班级条件） — verify: 所有命中余弦 ≥ 0.35；摘录来自 MySQL
- [x] 4.3 `hybrid`：两路各自排名后 RRF（k=60）融合；缺席一路不计分 — verify: 两路都命中的合成分 = 两路 `1/(60+rank)` 之和
- [x] 4.4 `POST /api/search`：字段 `q` + `mode`；空查询（<2 字符）返回 400 — verify: 三种模式各返回 200 且响应结构一致
- [x] 4.5 检索侧班级隔离：自报 `class_id` 被丢弃；跨班检索返回 200 + 空列表 — verify: A 班检索仅存在于 B 班的词，hits 为空且状态码为 200

## 5. 问答模块（T5）

- [x] 5.1 `POST /api/ask`：混合检索取前 4 条 → 有切片才调用对话网关 → 回答含 `[1]` 标注 — verify: 回答中的标注顺序与 citations 一致
- [x] 5.2 无命中返回 `no_evidence` + 固定文案 + 空 citations，且不调用对话网关 — verify: 问「明天的天气怎么样」返回 `no_evidence`
- [x] 5.3 对话模块输入仅含标题/序号/正文/提问；丢弃客户端注入的 system 消息 — verify: 传入 system 字段后行为不变

## 6. 材料侧新增接口（T6）

- [x] 6.1 `GET /api/materials/{id}/vectors`：返回集合名、距离、维度与每条切片的向量（含 MySQL 主键与 payload 编号的对应关系） — verify: 未认证 401；跨班 404
- [x] 6.2 `POST /api/materials/{id}/reindex`：按请求策略先删旧切片与旧向量再写入 — verify: 重建后切片数与策略按新参数生效
- [x] 6.3 `reindex` 仅教师可用 — verify: 学生请求返回 403 且切片不变

## 7. 编排与前端（T7）

- [x] 7.1 Compose 新增 `qdrant` 服务（不映射宿主机端口）与 `qdrant-data` 命名卷 — verify: `docker compose port qdrant 6333` 无映射；仅 web 有端口
- [x] 7.2 `README.md` 与 `.env.example` 更新 — verify: 说明 Qdrant、两类网关与本地回退
- [x] 7.3 前端新增知识检索页与知识问答页；材料详情展示切片与向量 — verify: 检索页可选三种模式并展示本次路径、名次与分数
- [x] 7.4 Nginx 仅反代 `/api` 与 `/health`，不新增对 Qdrant 的暴露 — verify: `/qdrant` 路径返回 404
- [x] 7.5 上传表单提供三种分段策略的卡片式选择（自动 / 自定义 / 按层级），选中项展开各自的参数区；「按层级分段」带「推荐」标记 — verify: 切换策略时仅显示对应参数区；自定义区含分段标识符、最大长度、重叠度与两条预处理规则
- [x] 7.6 前端整体改为北大红（`#94070A`，北京大学视觉形象识别系统标准色）主题 — verify: 主色、聚焦态、选中态、按钮与徽标统一使用北大红

## 8. 验证与归档（T8）

- [x] 8.1 Go 单元测试：切分三策略、嵌入确定性、RRF 融合、阈值过滤 — verify: `go test ./...` 全部 ok
- [x] 8.2 逐条执行 spec 场景（端到端脚本） — verify: 全部断言 PASS（见下）
- [x] 8.3 `openspec validate add-traceable-vector-retrieval --strict` — verify: exit 0
- [x] 8.4 `down` 后 `up` 数据仍在（含切片与向量） — verify: 重启后检索结果一致
- [ ] 8.5 归档：`openspec archive add-traceable-vector-retrieval` — verify: change 移入 archive，规约并入 `openspec/specs/`。**待课程确认后执行**

## 验收记录（8.2）

环境：Docker Compose 四容器（web 8081→80 / api / db / qdrant，仅 web 映射端口），MySQL 8.0，Qdrant v1.12.4，Go 1.24 静态二进制。

**三种嵌入/对话组合各跑一遍，结果一致：**

| 嵌入实现 | 对话实现 | 结果 |
| --- | --- | --- |
| 本地确定性回退（未配置网关） | 抽取式回退 | 72/72 PASS |
| 课程网关 `course-embedding`（2048 维） | 抽取式回退 | 72/72 PASS |
| **课程网关 `course-embedding`** | **课程网关 `course-chat`** | **72/72 PASS** |

> 切换嵌入实现后向量空间完全改变，必须重建索引（本地回退 → 网关时执行了 `docker compose down -v` 后重启）。仅切换对话实现不影响索引。

脚本：`BASE_URL=http://127.0.0.1:8081 python scripts/verify_retrieval.py`

课程网关下的语义泛化实测（字面零重叠仍能命中）：

| 问句 | 命中章节 | 最高余弦 |
| --- | --- | --- |
| 在楼道里跑来跑去行不行？ | 课间活动 | 0.4817 |
| 着火了要怎么撤离？ | 消防与应急 | 0.5549 |
| 图书馆一次能借几本书？ | 图书馆与自习 | 0.4142 |
| 磁铁为什么能吸铁钉？（本班无依据） | 无命中 | — |

课程对话网关（`course-chat`）下的问答实测：

| 问句 | 状态 | 回答 |
| --- | --- | --- |
| 课间追逐打闹可以吗？ | ok | 不可以，校园内不得追逐打闹[1]。 |
| 着火了要怎么撤离？ | ok | 着火了需按路线撤离，不乘坐电梯，不返回教室取物品；发现烟雾或异味要立即告知最近的教师，并按疏散指示离开教学楼。[1] |
| 图书馆一次能借几本书？ | ok | 资料中未明确说明图书馆一次能借几本书，仅提到一次借阅不超过规定册数。[1] |
| 明天的天气怎么样？ | no_evidence | 资料中未找到相关内容（0.1s 返回，未调用对话网关） |

> 第 3 行说明模型被正确约束在依据之内——**没有编造具体册数**。

| 分组 | 断言数 | 结果 |
| --- | --- | --- |
| 检索接口的认证（未登录 401） | 4 | PASS |
| 上传 → 切分 → 嵌入 → 向量库 | 2 | PASS |
| 向量索引与两库关联（主键一致、payload 无正文） | 8 | PASS |
| 空查询被拒绝（400） | 3 | PASS |
| 三种检索模式（结构、可回溯、关键字无向量分） | 15 | PASS |
| 混合检索与 RRF 融合（合成分、双路优先、降序） | 4 | PASS |
| 检索侧班级隔离（跨班空命中、自报身份无效、同构 404） | 7 | PASS |
| 引用标注与无依据处理 | 10 | PASS |
| 重建索引（权限、策略生效、越界拒绝） | 10 | PASS |
| 降级行为（关键字不依赖向量库） | 1 | PASS |

**合计 72 项端到端断言全部 PASS，0 FAIL。**

补充检查（非脚本内断言，人工执行）：

| 检查 | 结果 |
| --- | --- |
| Qdrant 停机：`keyword` → 200，`vector`/`hybrid` → 503，`ask` → 503 | PASS |
| Qdrant 恢复后三种模式均 → 200 | PASS |
| 第 3 课回归 `scripts/verify_e2e.py`（45 项） | 45/45 PASS |
| `down` → `up` 后材料、切片、向量均保留 | PASS |
| 端口隔离：`docker compose port` 仅 web 有映射 | PASS |
| `/qdrant`、`/collections`、`/uploads/` 均 404 | PASS |
| `go test ./...` | 全部 ok |
| `openspec validate --strict`（两个 change） | 均 exit 0 |

分段参数校验（9/9，人工执行）：

| 用例 | 期望 | 实际 |
| --- | --- | --- |
| `auto` + 附带 `max_chars=50`（应被忽略） | 201 | 201，切片 1 |
| `custom` 300/20 | 201 | 201 |
| `custom` `max_chars=50` / `5000` | 400 | 400「最大长度须在 100 至 2000 之间」 |
| `custom` `overlap_percent=80` | 400 | 400「重叠比例须在 0% 至 50% 之间」 |
| `hierarchy` 200/0 | 201 | 201，切片 4 |
| `hierarchy` `max_chars=50` / `overlap_percent=80` | 400 | 400 |
| 未知策略 `semantic` | 400 | 400「未知切分策略」 |

> 越界参数在**文件落盘与写库之前**即被拒绝，不产生文件或记录。
