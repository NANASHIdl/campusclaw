# knowledge-retrieval Specification

## Purpose
让教师与学生用一句自然语言在本班材料中定位相关切片，并让每一条结果都能回溯到「哪份材料、第几个切片、第几个字符到第几个字符」。切片正文存放于 MySQL，向量存放于 Qdrant；无依据时明确返回未找到，不得编造出处。检索与上传共用同一条班级边界，班级只取自登录会话。

## Requirements

### Requirement: 切片与可追溯性

系统 MUST 在材料入库后把知识库正文切分为若干切片，并把切片文本、切片序号及其在待切分文本中的位置写入 `knowledge_chunks`。原始文件与 `knowledge_entries.body_text` MUST 保持上传时的原样，切分 MUST NOT 改写原文。

#### Scenario: 上传后可看到切片

- **WHEN** 教师上传一份合法材料并入库成功
- **THEN** `knowledge_chunks` MUST 存在该材料对应的若干切片记录
- **AND** 每条切片 MUST 含切片序号、起始偏移与结束偏移
- **AND** `knowledge_entries.body_text` MUST 与上传原文一致

#### Scenario: 每条检索结果可回溯

- **WHEN** 任一检索模式返回命中切片
- **THEN** 每条命中 MUST 给出材料标题、切片序号、字符区间与一段摘录
- **AND** 摘录 MUST 取自 MySQL 中的切片正文，MUST NOT 取自向量库

### Requirement: 切分策略

系统 MUST 支持 `auto`、`custom`、`hierarchy` 三种切分策略。未指定策略时 MUST 按 `auto` 处理。请求中为 `auto` 策略另行填写的长度与预处理参数 MUST NOT 生效。

#### Scenario: 自动窗口策略

- **WHEN** 以 `auto` 策略切分一份长正文
- **THEN** 单个切片 MUST NOT 超过 800 字
- **AND** 相邻切片 MUST 存在重叠
- **AND** 切分 MUST 优先在空行、换行或句号处断开

#### Scenario: 自定义策略参数生效

- **WHEN** 以 `custom` 策略指定分段标识符、最大长度与重叠比例
- **THEN** 切片长度 MUST NOT 超过指定的最大长度
- **AND** 当最大长度不在 100 至 2000 之间或重叠比例不在 0% 至 50% 之间时，请求 MUST 被拒绝（HTTP 400）
- **AND** 该拒绝 MUST 发生在文件落盘与数据写入之前，MUST NOT 留下文件或记录

#### Scenario: 按标题层级分章

- **WHEN** 以 `hierarchy` 策略切分一份含 Markdown 标题的正文
- **THEN** 切片 MUST 按 `#`、`##`、`###` 的层级划分
- **AND** 章节标题 MUST 保留在该章切片内

#### Scenario: 分章后超长章节按窗口再切

- **WHEN** 以 `hierarchy` 策略指定最大长度与重叠比例，且某一章超过该长度
- **THEN** 该章 MUST 再按最大长度与重叠比例切分
- **AND** 当最大长度不在 100 至 2000 之间或重叠比例不在 0% 至 50% 之间时，请求 MUST 被拒绝（HTTP 400）

#### Scenario: 预处理不改写原文

- **WHEN** 以 `custom` 策略启用移除链接或折叠空白
- **THEN** `knowledge_entries.body_text` MUST 仍为上传原文
- **AND** 切片偏移量 MUST 相对于预处理后的待切分文本计算

### Requirement: 向量索引与两库关联

系统 MUST 为每条切片生成一条向量并写入 Qdrant 集合 `campusclaw_chunks`（余弦距离）。向量主键 MUST 等于 `knowledge_chunks.id`。Qdrant 的 payload MUST 仅包含 `class_id`、`material_id`、`knowledge_entry_id`、`chunk_id`、`chunk_index`，MUST NOT 包含切片正文。

#### Scenario: 向量主键与切片主键一致

- **WHEN** 查看某份材料的向量记录
- **THEN** 每条向量的主键 MUST 等于对应 `knowledge_chunks.id`
- **AND** payload 中的 `chunk_id` MUST 与之一致
- **AND** payload MUST NOT 含正文文本

#### Scenario: 向量维度与集合一致

- **WHEN** 检查嵌入结果与集合配置
- **THEN** 每条向量的维度 MUST 等于集合声明的维度
- **AND** 嵌入 MUST 仅在服务端完成，MUST NOT 向浏览器下发网关密钥

### Requirement: 关键字检索

`keyword` 模式 MUST 仅查询 MySQL 全文索引，MUST NOT 调用嵌入服务，也 MUST NOT 访问 Qdrant。

#### Scenario: 原词命中

- **WHEN** 以 `keyword` 模式提交一个词，该词出现在本班某切片的正文中
- **THEN** 系统 MUST 返回 HTTP 200 且命中列表 MUST 包含该切片
- **AND** 命中项 MUST 带有全文相关度与关键字名次

#### Scenario: 关键字模式不访问向量库

- **WHEN** 以 `keyword` 模式发起检索
- **THEN** 命中项 MUST NOT 含向量相似度分数
- **AND** 系统 MUST NOT 因向量库不可用而失败

### Requirement: 向量检索

`vector` 模式 MUST 先把问句转换为向量，再在 Qdrant 中按会话班级过滤检索；余弦相似度低于 0.35 的候选 MUST 被丢弃；随后 MUST 以向量主键回 MySQL 取回切片正文。

#### Scenario: 语义相近命中

- **WHEN** 以 `vector` 模式提交一个与原文表述不同但语义相近的问句
- **THEN** 系统 MUST 返回 HTTP 200
- **AND** 每条命中 MUST 带有余弦相似度
- **AND** 每条命中的余弦相似度 MUST NOT 低于 0.35

#### Scenario: 向量主键回表取正文

- **WHEN** 向量检索命中一条记录
- **THEN** 系统 MUST 以该向量主键回 MySQL 取回切片正文
- **AND** 返回的摘录 MUST 来自 MySQL，而非向量库

#### Scenario: 空查询被拒绝

- **WHEN** 提交的问句在去除空白后不足 2 个字符
- **THEN** 系统 MUST 返回 HTTP 400

### Requirement: 混合检索与名次融合

`hybrid` 模式 MUST 同时执行关键字与向量两条路径，各路先独立过滤，再按名次执行 RRF 融合（`k = 60`）。缺席的一路 MUST NOT 贡献分数。

#### Scenario: 两路都命中排更前

- **WHEN** 以 `hybrid` 模式检索，某个切片同时被关键字路径与向量路径命中
- **THEN** 该切片的名次 MUST 排在仅命中一路的切片之前
- **AND** 其合成分 MUST 等于两路 `1/(60+名次)` 之和

#### Scenario: 单路命中仍给出合成分

- **WHEN** 某切片仅被一条路径命中
- **THEN** 其合成分 MUST 等于该路 `1/(60+名次)`
- **AND** 缺席的一路 MUST NOT 贡献分数

#### Scenario: 融合的是名次而非分数

- **WHEN** 检查混合检索的排序依据
- **THEN** 排序 MUST 依据两路名次合成的分数
- **AND** MUST NOT 直接将两路的绝对分数相加

### Requirement: 引用标注与无依据处理

问答接口 MUST 先以混合模式取本班前 4 条切片；**有切片才调用对话网关**。回答中的 `[1]`、`[2]` 标注顺序 MUST 与出处列表一致。无命中切片时 MUST 返回「资料中未找到相关内容」且出处列表为空，并 MUST NOT 调用对话网关。

#### Scenario: 有依据才生成回答

- **WHEN** 提交一个本班材料中有依据的问题
- **THEN** 系统 MUST 返回 HTTP 200 且回答 MUST 非空
- **AND** 回答 MUST 含 `[1]` 形式的标注
- **AND** 出处列表 MUST 非空且顺序与标注一致

#### Scenario: 无依据不调用模型

- **WHEN** 提交一个本班材料中无依据的问题（如询问天气）
- **THEN** 系统 MUST 返回固定文案「资料中未找到相关内容」
- **AND** 出处列表 MUST 为空
- **AND** 系统 MUST NOT 调用对话网关

#### Scenario: 对话模块的输入范围

- **WHEN** 系统组装对话模块的输入
- **THEN** 输入 MUST 仅含材料标题、切片序号、切片正文与用户本轮提问
- **AND** 输入 MUST NOT 含向量分量、其他班级的切片或客户端自行构造的 system 消息

### Requirement: 检索侧的班级隔离

检索 MUST 与上传共用同一条班级边界。班级标识 MUST 仅取自登录会话；请求头、查询参数或 JSON 正文中另行携带的班级编号 MUST 被丢弃。关键字与向量两条路径 MUST 均携带班级条件，且回表查询时 MUST 再次核对。跨班检索对外 MUST 表现为无命中，MUST NOT 以 403 或 404 暗示资源属于其他班级。

#### Scenario: 跨班检索表现为无命中

- **WHEN** 班级 A 的用户检索一个仅存在于班级 B 正文中的词
- **THEN** 系统 MUST 返回 HTTP 200
- **AND** 命中列表 MUST 为空，或问答返回「资料中未找到相关内容」
- **AND** 响应 MUST NOT 为 403 或 404

#### Scenario: 请求中自带的班级编号无效

- **WHEN** 用户在请求头、查询参数或 JSON 正文中加入 `class_id`
- **THEN** 检索结果 MUST NOT 因此改变
- **AND** 实际过滤条件 MUST 仍为会话中的班级

#### Scenario: 回表时再次核对班级

- **WHEN** 向量检索返回向量主键后回 MySQL 取正文
- **THEN** 该查询 MUST 携带会话班级条件
- **AND** 归属其他班级的记录 MUST 被排除

#### Scenario: 跨班材料详情仍为同构 404

- **WHEN** 班级 A 的用户以班级 B 某条材料的标识请求详情或下载
- **THEN** 系统 MUST 返回与「标识不存在」完全一致的 404

#### Scenario: 学生与教师均可检索

- **WHEN** 以 student 角色的有效会话发起检索或问答
- **THEN** 系统 MUST 允许并返回本班结果
- **AND** 上传接口对学生的拒绝行为 MUST 保持不变

### Requirement: 重建索引

教师 MUST 能按本次请求指定的策略重建某份材料的切片与向量：先删除旧切片与旧向量记录，再写入新记录。已入库材料 MUST NOT 自动重新切分。

#### Scenario: 重建后按新策略生效

- **WHEN** 教师对一份材料以 `custom` 策略重建索引
- **THEN** 该材料的切片 MUST 按新策略重新生成
- **AND** 该材料的向量 MUST 与新切片一一对应
- **AND** 旧切片与旧向量记录 MUST 被删除

#### Scenario: 学生不得重建索引

- **WHEN** 以 student 角色的有效会话请求重建索引
- **THEN** 系统 MUST 拒绝该请求
- **AND** 该材料的切片与向量 MUST NOT 发生变化

#### Scenario: 未重建则保持原策略

- **WHEN** 不触发重建索引
- **THEN** 已有材料的切片 MUST 保持入库时的策略与内容

### Requirement: 索引失败与向量库不可用的降级

嵌入失败时材料记录 MUST 保留，对应切片 MUST 标记为 `failed`，MUST NOT 写入不完整的向量数据。Qdrant 不可用时，关键字检索 MUST 仍可返回结果；向量与混合模式 MUST 返回 503，MUST NOT 编造相似度分数。

#### Scenario: 嵌入失败不丢材料

- **WHEN** 某批切片的嵌入过程失败
- **THEN** 材料记录 MUST 仍然存在且可访问
- **AND** 对应切片 MUST 标记为 `failed`
- **AND** MUST NOT 产生不完整的向量记录

#### Scenario: 向量库不可用时关键字仍可用

- **WHEN** Qdrant 不可用且用户以 `keyword` 模式检索
- **THEN** 系统 MUST 仍能返回命中结果

#### Scenario: 向量库不可用时不编造分数

- **WHEN** Qdrant 不可用且用户以 `vector` 或 `hybrid` 模式检索
- **THEN** 系统 MUST 返回 503
- **AND** MUST NOT 返回任何相似度分数

### Requirement: 检索接口的认证

`POST /api/search`、`POST /api/ask`、`GET /api/materials/{id}/vectors`、`POST /api/materials/{id}/reindex` MUST 要求有效会话；未认证时 MUST 返回 401 且 MUST NOT 返回任何切片正文或材料数据。

#### Scenario: 未认证访问检索接口

- **WHEN** 未携带有效会话 Cookie 的请求访问检索、问答、向量查看或重建索引接口
- **THEN** 系统 MUST 返回 HTTP 401
- **AND** 响应 MUST NOT 包含任何切片正文或材料数据

#### Scenario: 向量查看的班级边界

- **WHEN** 用户请求查看归属其他班级材料的向量记录
- **THEN** 系统 MUST 返回与「材料不存在」完全一致的 404
