# ADR 0004：数据源映射与管理边界

- 状态：Accepted
- 日期：2026-11-05
- 关联：Issue #4、Proposal #1、ADR 0002、ADR 0003

## 背景

Relythra 需要把 Project、Service、Environment 映射到 GitHub、Grafana、Prometheus、Loki 和运行时数据源。这些映射同时包含公开配置和凭据引用，并会成为后续查询、Evidence 和 Agent 工具调用的入口。若允许任意 URL、任意查询文本或多个管理员绕过统一约束，数据源配置本身就会形成 SSRF、凭据泄露和不可追溯查询的入口。

本 ADR 固定 v1 的映射授权、查询模板、网络访问和运行时能力边界。它不把当前能力扩大为完整的身份系统、生产连接器或 Kubernetes 诊断。

## 决策

### 单管理员 Token

- v1 使用一个从 `RELYTHRA_ADMIN_TOKEN_REF` 注入的管理 Token，接受 `env:NAME` 或 `file:/absolute/path` SecretRef。
- 管理 Token 和全局数据源 allowlist 必须同时配置；任一缺失时管理 API 返回 `management_unavailable`，不得以匿名或默认 Token 降级运行。
- Token 使用 Bearer 认证和常量时间比较。管理 API 只绑定可信内网或回环地址，不是公网身份系统。
- 该 Token 是单管理员共享能力，不等于 RBAC、SSO、多租户隔离、审批流或逐用户审计。后续需要多角色或多人管理时必须新增 ADR 和迁移方案。

### 映射配置与 SecretRef

- 映射由 `project_id`、`service_id`、`environment` 和 `source_type` 唯一确定；诊断时不得按服务名称模糊兜底。
- 管理 API 保存 SecretRef 定位符，不接收、保存或回显凭据明文。响应只返回 `secret_status`。
- 映射使用整数 `version` 做乐观锁。更新和删除必须提交 `expected_version`；版本过期返回 `409 stale_version`。
- 删除为软删除。活动映射的部分唯一索引只约束 `deleted_at IS NULL` 的记录，因此已删除身份可以重新创建，历史记录保留。
- 缺少 Compose 必需标签、模板或部分运行配置的草稿可以保存，但 `validate`、查询计划和连接测试必须返回结构化问题。

### 固定模板

- API 只接受注册表中的 `template_id`，不接受原始 PromQL、LogQL、SQL、shell、GitHub GraphQL、任意 HTTP method、Header 或模板正文。
- 查询计划只允许 `service`、`start`、`end`、`limit`、`cursor` 和 `max_series` 等已注册参数。时间窗、分页、序列数和游标长度都有上限。
- v1 已实现模板解析与查询计划，不执行真实业务 PromQL 或 LogQL。后续连接器必须沿用同一模板注册表和 Evidence 约束。

### 出站网络策略

- Endpoint 只能是 `http` 或 `https` 根地址，不能包含 userinfo、query、fragment 或非根路径。
- 主机、端口和协议必须同时通过显式 allowlist。私有地址、HTTP 和 TLS 能力分别显式开启，不能由用户请求临时覆盖。
- 每次连接前解析 DNS，并拒绝混合安全与不安全结果、回环、链路本地、未指定、多播、云元数据和其他保留地址。
- HTTP client 将连接锁定到本次校验的 IP，禁止重定向，并限制总超时、响应大小和读取时间，防止 DNS rebinding、无限跳转和响应耗尽。
- 该策略当前覆盖固定路径连接测试。真实业务查询连接器尚未实现，启用前必须复用并测试相同边界。

### 运行时能力

- `runtime_type` 接受 `docker-compose` 和 `kubernetes`，以保留未来契约和可迁移数据。
- v1 仅支持 Docker Compose 的五标签：`project`、`service`、`env`、`host_id`、`container_id`。
- Kubernetes 映射可以保存为草稿或配置，但校验和连接测试明确返回 `unsupported_runtime`。v1 不挂载 Docker Socket、不提供 Kubernetes 写 API，也不执行重启、回滚或扩缩容。

### 审计

- 管理认证拒绝、配置读写、校验、查询计划和连接测试都写入不可变 `audit_events`。
- 审计参数只记录来源类型、模板 ID、稳定错误码和耗时；mapping ID 作为对象 ID。Endpoint、SecretRef、Authorization、响应正文和凭据值不得进入审计。
- 连接测试在执行外部请求前先写 preflight 审计。若 preflight 写入失败，不发起连接；连接结束后再写最终结果。
- CRUD 数据库写与最终审计写不在同一事务中。数据库写入可能已经提交而最终审计写入失败，此时 API 返回 `internal_error`。当前通过稳定错误、请求 ID 和可查询记录暴露该边界，生产使用前需要评估审计补偿或事务 outbox。

## 后果

数据源映射具备持久化、版本控制、固定模板、出站限制和可追溯管理。代价是运维必须同时提供管理 Token 和 allowlist，连接测试的网络路径必须预先配置，私有内网目标必须显式开启私有地址访问。

当前没有多用户 RBAC、组织级 Secret Manager、真实业务查询执行、Kubernetes 诊断或数据库写入与审计写入的事务原子性。管理 API 不得因本 ADR 而暴露到公网。

## 回滚

应用可以回滚到不启用管理路由的版本，并立即撤销旧管理 Token。数据库优先保留 `000004` 的前向兼容 schema；只有隔离测试环境可以执行其 `down`。生产回滚需要停止映射写入、备份映射与审计数据，并由维护者明确授权后执行。
