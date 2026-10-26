# Spec: auth-upload（增量）

> 本文件是 `auth-upload` 的 delta 规约：认证方式由「服务端会话 + Cookie」改为「JWT Bearer 令牌」。
> 其余既有 Requirement（统一失败响应、角色权限、按班级隔离、上传入库、下载鉴权、数据模型、口令与密钥、容器编排）不变。

## REMOVED Requirements

### Requirement: 服务端会话与登录

**原因**：认证方式改为 JWT Bearer，服务端会话表退役，Cookie 不再作为登录凭据。替代行为见下文「JWT 访问令牌与登录」。

## ADDED Requirements

### Requirement: JWT 访问令牌与登录

`POST /api/login` MUST 返回 `{"token": "...", "user": {...}}`，其中 `token` 为 **HS256 JWT**，签名密钥 MUST 只取自环境变量 `SESSION_SECRET`。令牌 Payload MUST 只含注册声明 `sub`（用户 ID）、`jti`（令牌标识）、`iat`、`exp`，MUST NOT 含角色或 `class_id`。角色与班级 MUST 在每次请求时按 `sub` 回查 `users` 表。

受保护接口 MUST 从请求头 `Authorization: Bearer <token>` 取令牌；Cookie MUST NOT 被当作登录凭据。令牌有效期 MUST 取 `SESSION_TTL_SECONDS`（默认 86400 秒），系统 MUST NOT 签发 Refresh Token。

`POST /api/logout` MUST 把当前令牌的 `jti` 写入吊销表并返回 200；同一令牌再次调用 MUST 返回 401。请求方携带未过期的旧 Bearer 再次登录时，系统 MUST 先吊销旧 `jti` 再签发新令牌。

令牌缺失、方案不是 `Bearer`、签名为空或含空格、签名或算法不符、缺少 `sub`/`jti`/`exp`、已过期、`jti` 已吊销，或 `sub` 对应用户不存在，对外 MUST 统一返回 HTTP 401 且正文为 `{"error":"unauthorized"}`（沿用本项目既有的机器可读错误码，不改为自然语言文案）。

无需令牌的接口 MUST 仅为：`GET /health`、`POST /api/login`、`GET /openapi.json`、`GET /swagger/index.html`。

#### Scenario: 登录返回令牌且不泄露口令

- **WHEN** 用户使用有效账号与口令请求登录
- **THEN** 响应 MUST 为 HTTP 200 且含 `token` 与 `user`
- **AND** `user` MUST 含用户标识、用户名、角色与所属班级
- **AND** 响应体 MUST NOT 含口令明文或口令哈希
- **AND** 响应 MUST NOT 依赖 `Set-Cookie` 传递凭据

#### Scenario: 令牌载荷不含角色与班级

- **WHEN** 解码登录返回的令牌 Payload
- **THEN** 其字段 MUST 仅为 `sub`、`jti`、`iat`、`exp`
- **AND** MUST NOT 出现角色或 `class_id`
- **AND** 后续请求的角色与班级 MUST 来自按 `sub` 回查的用户行

#### Scenario: 缺少或伪造令牌被拒绝

- **WHEN** 请求受保护接口时不带 `Authorization` 头、方案不是 `Bearer`、签名错误或令牌已过期
- **THEN** 系统 MUST 返回 HTTP 401
- **AND** 正文 MUST 为 `{"error":"unauthorized"}`

#### Scenario: 仅携带 Cookie 不足以通过认证

- **WHEN** 请求只携带登录时可能残留的 Cookie、不带 `Authorization` 头
- **THEN** 系统 MUST 返回 HTTP 401

#### Scenario: 登出后令牌立即失效

- **WHEN** 已登录用户请求登出，随后用同一令牌访问受保护接口
- **THEN** 登出 MUST 返回 200
- **AND** 后续请求 MUST 返回 401

#### Scenario: 再次登录吊销旧令牌

- **WHEN** 请求方携带未过期的旧 Bearer 再次登录且口令校验通过
- **THEN** 系统 MUST 签发新令牌
- **AND** 旧令牌 MUST 立即失效（持旧令牌的后续请求返回 401）

#### Scenario: 白名单接口无需令牌

- **WHEN** 不带令牌访问 `GET /health`、`GET /openapi.json` 或 `GET /swagger/index.html`
- **THEN** 系统 MUST NOT 返回 401
