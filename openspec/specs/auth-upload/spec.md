# auth-upload Specification

## Purpose
让教师与学生以各自角色登录 CampusClaw，以班级作为数据边界隔离教学材料；教师上传的内容解析后写入知识库，本班成员可通过受保护接口查看与下载；越权上传、跨班访问与未登录访问一律在服务端被拒绝，且对外响应不泄露资源是否存在。

## Requirements

### Requirement: 服务端会话与登录

系统 MUST 采用服务端会话：浏览器 Cookie 中仅保存随机会话标识，用户、角色与班级保存在服务端 `sessions` 表与 `users` 表；Cookie MUST 设置 HttpOnly 与 SameSite。登录成功 MUST 换发新的会话标识并作废旧标识；登出 MUST 删除对应会话记录使其立即失效。

#### Scenario: 教师与学生登录成功

- **WHEN** 用户使用有效账号与密码请求登录（预置账号含教师 A、学生 A1、学生 B1）
- **THEN** 登录 MUST 成功并返回 HTTP 200
- **AND** 响应体 MUST 含用户标识、用户名、角色与所属班级，且 MUST NOT 含口令明文或口令哈希
- **AND** 响应头 MUST 含 `Set-Cookie`，其值仅为随机会话标识，MUST NOT 含角色或班级

#### Scenario: 登录成功换发会话标识（防会话固定）

- **WHEN** 请求方在登录请求中已携带一个（可能被预置的）旧会话标识，且账号口令校验通过
- **THEN** 系统 MUST 签发一个新的会话标识并写入 `sessions` 表
- **AND** 原会话标识 MUST 立即失效（持旧标识的后续请求 MUST 返回 401）

#### Scenario: 未认证访问受保护资源

- **WHEN** 未携带有效会话 Cookie 的请求访问受保护接口（材料列表、详情、下载、上传、`/api/me`）
- **THEN** 系统 MUST 返回 HTTP 401
- **AND** 响应 MUST NOT 包含任何材料标题、正文或存储路径

#### Scenario: 登出后会话立即失效

- **WHEN** 已登录用户请求登出
- **THEN** 系统 MUST 删除对应 `sessions` 记录并清除 Cookie
- **AND** 持原标识的后续请求 MUST 返回 401

### Requirement: 统一失败响应

登录失败时，系统 MUST 使「账号不存在」「口令错误」「触发限流」三种原因对外不可区分：相同的 HTTP 状态码、相同的响应体文案，且响应耗时接近。真实原因 MUST 仅写入服务端日志。

#### Scenario: 账号不存在与口令错误对外同构

- **WHEN** 分别以「不存在的用户名」与「存在用户名但错误口令」各发起一次登录
- **THEN** 两次响应 MUST 具有相同的状态码与相同的响应体
- **AND** 响应 MUST NOT 泄露账号是否存在

#### Scenario: 触发限流对外同构

- **WHEN** 同一账号或来源在短时间内连续发起超过阈值的登录尝试
- **THEN** 系统 MUST 继续返回与前述失败相同的状态码与响应体（MUST NOT 返回可区分的 429）
- **AND** 真实原因（限流）MUST 仅记录于服务端日志

### Requirement: 角色权限

系统 MUST 按会话解析出的角色授权：教师可上传与管理本班材料；学生对本班材料只读；学生调用上传接口 MUST 被拒绝，且拒绝 MUST 发生在文件落盘与数据写入之前。

#### Scenario: 学生上传被拒绝

- **WHEN** 以 student 角色（如学生 A1）的有效会话向材料上传接口提交文件
- **THEN** 系统 MUST 返回 HTTP 403
- **AND** 上传目录 MUST 无因该请求产生的新文件
- **AND** `materials` 与 `knowledge_entries` MUST 无新增记录

#### Scenario: 教师上传被允许

- **WHEN** 以 teacher 角色（如教师 A）的有效会话提交白名单内的文件
- **THEN** 系统 MUST 接受请求并执行入库流程
- **AND** 返回 MUST 表示成功（HTTP 201）并含新材料标识

### Requirement: 按班级隔离

班级是数据边界。班级 MUST 取自会话（由认证模块从 `users` 行读出），请求头、查询参数或请求体中自行声明的角色与班级 MUST 被丢弃而不参与判定。跨班访问与记录不存在两种情况对外 MUST 返回同构的 404 响应。

#### Scenario: 跨班按标识访问与不存在同构

- **WHEN** 班级 A 的用户以 B 班某条材料的标识请求详情或下载
- **THEN** 系统 MUST 返回 HTTP 404
- **AND** 该响应 MUST 与「标识不存在」的响应完全一致（同状态码、同响应体）
- **AND** 响应 MUST NOT 返回 B 班材料的标题、正文或存储路径

#### Scenario: 列表仅含本班记录

- **WHEN** 已登录用户请求材料列表
- **THEN** 返回集合中每一条记录 MUST 归属于会话班级
- **AND** 查询 MUST 使用会话中的班级作为过滤条件
- **AND** 空结果集 MUST 仍返回 200

#### Scenario: 客户端自报身份不改变判定

- **WHEN** 用户在请求头、查询参数或 JSON 正文中加入 `role` 或 `class_id` 字段
- **THEN** 列表、详情与上传的结果 MUST NOT 因此改变
- **AND** 实际使用的角色与班级 MUST 仍来自服务端会话

### Requirement: 材料上传与知识库入库

教师上传教学材料后，系统 MUST 先校验扩展名白名单（`.txt`、`.md`）与大小上限，通过后由服务端生成存储名并落盘，再将材料行与知识库正文行置于同一事务提交；任一步骤失败 MUST 回滚两张表并清理已写入文件。

#### Scenario: 上传后两表与列表一致

- **WHEN** 教师 A 上传一份合法文件且解析成功
- **THEN** `materials` MUST 新增一条记录且其 `class_id` 为班级 A
- **AND** `knowledge_entries` MUST 在同一事务中新增对应正文记录且 `class_id` 为班级 A
- **AND** 教师 A 刷新本班列表 MUST 看到该条新记录

#### Scenario: 白名单外扩展名被拒绝

- **WHEN** 教师提交 `.pdf`、`.docx` 或其他白名单之外的扩展名（含仅修改文件名后缀的情形）
- **THEN** 系统 MUST 在落盘之前返回 4xx
- **AND** MUST NOT 产生文件，也 MUST NOT 产生材料或知识库记录

#### Scenario: 上传失败不留孤儿

- **WHEN** 上传过程中任一后续步骤（如事务写入）失败
- **THEN** 两张表 MUST 整体回滚
- **AND** 已写入的文件 MUST 被清理，MUST NOT 出现「有文件无记录」的残留状态

### Requirement: 材料下载鉴权

系统 MUST 通过受保护接口 `GET /api/materials/{id}/file` 提供下载；上传目录 MUST NOT 被配置为静态资源目录。下载 MUST 在读取文件字节之前完成认证与班级归属校验。

#### Scenario: 未认证下载被拒绝

- **WHEN** 未携带有效会话 Cookie 的请求访问下载接口
- **THEN** 系统 MUST 返回 401 且 MUST NOT 读取文件

#### Scenario: 本班材料可下载

- **WHEN** 已认证且材料归属于会话班级的用户访问下载接口
- **THEN** 系统 MUST 返回 200 并回写文件字节

#### Scenario: 跨班下载与不存在同构

- **WHEN** 用户将下载路径中的标识替换为其他班级的材料标识
- **THEN** 系统 MUST 返回与「记录不存在」完全一致的 404
- **AND** MUST NOT 读取或返回该文件内容

### Requirement: 数据模型与租户列

系统 MUST 建立 `classes`、`users`、`sessions`、`materials`、`knowledge_entries` 五张表；`materials` 与 `knowledge_entries` MUST 携带非空且带索引的租户列 `class_id`。种子数据 MUST 含两个可区分班级，且各含教师与学生账号。

#### Scenario: 租户列非空且带索引

- **WHEN** 检查 `materials` 与 `knowledge_entries` 的表结构
- **THEN** 两表的 `class_id` MUST 为 NOT NULL
- **AND** 两表的 `class_id` MUST 建立索引

#### Scenario: 种子数据双班可区分

- **WHEN** 服务首次启动执行初始化
- **THEN** MUST 存在班级 A 与班级 B
- **AND** MUST 存在教师 A（归属 A）、学生 A1（归属 A）、学生 B1（归属 B）
- **AND** MUST 存在至少一条明确归属 A 班的材料标题与至少一条明确归属 B 班的材料标题

### Requirement: 口令与会话密钥

口令 MUST 以单向哈希形式存储，MUST NOT 保存明文；数据库连接口令与会话密钥 MUST 通过环境变量注入，MUST NOT 写入代码仓库。

#### Scenario: 库中无明文口令

- **WHEN** 直接查询 `users` 表的口令字段
- **THEN** 字段值 MUST NOT 等于任何已知预置账号的明文口令
- **AND** MUST 可识别为哈希格式

#### Scenario: 缺少必需环境变量时启动失败

- **WHEN** 未提供数据库连接口令或会话密钥等必需环境变量
- **THEN** 服务启动 MUST 失败，MUST NOT 使用不安全的默认值
- **AND** `.env.example` MUST 列出所需变量名

### Requirement: 容器编排与数据卷

系统 MUST 以 Docker Compose 作为标准启动方式，一次 `docker compose up` 拉起 `web`、`api`、`db` 三个服务；MUST 仅 `web` 向宿主机映射端口，`api` 与 `db` MUST NOT 映射宿主机端口。数据库数据与上传目录 MUST 存放于数据卷，`docker compose down` 后 `up` 数据仍在。

#### Scenario: 仅 web 对外映射端口

- **WHEN** 检查 Compose 的端口发布配置
- **THEN** 仅 `web` MUST 向宿主机发布端口
- **AND** `api` 与 `db` MUST NOT 发布宿主机端口

#### Scenario: down 再 up 数据仍在

- **WHEN** 在存在预置账号与已上传材料的情况下执行 `docker compose down` 后再 `up`（不附加 `-v`）
- **THEN** 预置账号 MUST 仍可登录
- **AND** 已上传材料与知识库正文 MUST 仍可查询到

#### Scenario: 健康检查无需认证

- **WHEN** 未携带会话 Cookie 的请求访问 `GET /health`
- **THEN** MUST 返回 HTTP 200
- **AND** 响应 MUST NOT 包含任何材料数据
