# api-docs Specification

## Purpose
让接口文档与代码同源：OpenAPI 描述由处理函数上方的 API 注解生成，浏览器打开 Swagger 页面即可对照文档实际发请求。文档必须写清路径、入参出参、错误码与是否需要登录，并在启动时与代码实际注册的路由做一致性自检，避免文档漂移。

## Requirements

### Requirement: OpenAPI 由注解生成

系统 MUST 由处理函数上方的 API 注解生成 OpenAPI 3.0 描述，并通过 `GET /openapi.json` 提供。该描述 MUST 是接口文档的唯一来源，MUST NOT 另有一份手工维护的文档与之并存。

每个操作 MUST 声明：唯一且稳定的 `operationId`、`summary`、分组标签、是否需要登录、路径参数与查询参数、请求体结构、以及各状态码的响应结构与错误码说明。认证方案 MUST 以 `bearerAuth`（`type: http`、`scheme: bearer`、`bearerFormat: JWT`）声明，需登录的操作 MUST 引用该方案。

#### Scenario: 文档可被机器解析

- **WHEN** 请求 `GET /openapi.json`
- **THEN** 响应 MUST 为可解析的 JSON
- **AND** `openapi` 字段 MUST 为 3.0.x
- **AND** `components.securitySchemes.bearerAuth` MUST 存在且 `scheme` 为 `bearer`

#### Scenario: 每个操作都写清鉴权与错误码

- **WHEN** 检查任一需登录的操作
- **THEN** 该操作 MUST 引用 `bearerAuth`
- **AND** MUST 列出其可能返回的 4xx 与 5xx 状态码及错误文案

#### Scenario: 无需登录的接口不引用安全方案

- **WHEN** 检查 `POST /api/login`、`GET /health`、`GET /openapi.json`、`GET /swagger/index.html`
- **THEN** 这些操作 MUST NOT 要求 `bearerAuth`

### Requirement: 官方 Swagger UI

系统 MUST 在 `GET /swagger/index.html` 提供**官方 Swagger UI** 页面。该页面 MUST 能列出 OpenAPI 描述中的接口，并 MUST 支持在页面内对接口实际发起请求、显示响应。Swagger UI 的静态资源 MUST 随依赖内嵌或随镜像分发，运行期 MUST NOT 依赖外部 CDN。

#### Scenario: 页面可打开且无需令牌

- **WHEN** 不带令牌在浏览器打开 `/swagger/index.html`
- **THEN** 页面 MUST 正常渲染
- **AND** MUST NOT 返回 401

#### Scenario: 页面能试调接口

- **WHEN** 在页面中填入令牌并对某个需登录的接口点击执行
- **THEN** 页面 MUST 实际发出该请求并显示响应状态与正文

#### Scenario: 运行期不依赖外网

- **WHEN** 在断网环境下打开该页面
- **THEN** 页面 MUST 仍能正常加载并渲染接口列表

### Requirement: 文档与路由一致性自检

系统 MUST 在启动时比对「注解中声明的操作」与「代码实际注册的路由」。存在只在代码中、或只在注解中的路由时，启动 MUST 失败并记录缺失项，MUST NOT 静默生成一份不完整的文档。

#### Scenario: 路由与注解一一对应

- **WHEN** 服务正常启动
- **THEN** 注解声明的操作集合 MUST 与注册的路由集合完全相等
- **AND** 每条路由的方法、路径与鉴权级别 MUST 一致

#### Scenario: 注解缺失导致启动失败

- **WHEN** 代码注册了一条路由但注解中没有对应条目
- **THEN** 启动 MUST 失败
- **AND** 日志 MUST 指出缺失的方法与路径
