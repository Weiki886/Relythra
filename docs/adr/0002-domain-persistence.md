# ADR 0002：领域标识、状态机与 PostgreSQL 持久化

- 状态：Accepted
- 日期：2026-11-04
- 关联：Issue #3、Proposal #1、ADR 0001

## 背景

发布诊断需要把实际部署、告警、代码上下文、指标、日志、运行状态、工具调用、证据、根因假设和报告关联起来。同一个外部事件可能重复投递，诊断可能多次重试，人工和 Agent 也可能并发更新同一个调查。仅依靠应用层校验或单个 `JSON` 文档，无法稳定保证引用完整性、幂等性和历史可追溯性。

本 ADR 固定领域模型的持久化边界。连接器、Agent 编排、身份权限和通知投递不在本决策范围内。

## 决策

### 标识与时间

- 所有核心实体使用 PostgreSQL `uuid` 主键，默认由 `gen_random_uuid()` 生成。内部主键与外部系统 ID、幂等键分开保存。
- 面向 API 的 ID 使用小写、规范的 RFC 4122 UUID v4。外部 ID 不能替代内部 ID，也不能作为公开资源路由的唯一依据。
- 持久化时间使用 `timestamptz`，Go 写入前统一转为 UTC。来自 GitHub、Grafana 或其他来源的原始时间文本和时区另存，避免归一化后丢失核验信息。
- `DeploymentEvent` 表示实际发生的部署，是发布关联的唯一事实源。`Release`、GitHub Release 或最新 Commit 只提供代码上下文。

### 迁移与引用完整性

- 领域 schema 使用独立的 `000002_domain_model.sql` goose 迁移，包含可执行的 `Up` 与 `Down`。已合入的迁移视为不可变，后续修正通过新迁移完成。
- PostgreSQL 外键负责实体引用完整性；服务、环境、发布时间窗、外部指纹和幂等键使用唯一约束与复合索引保护。
- 跨记录归属使用复合外键约束：Evidence 的 ToolCall 必须属于同一 Investigation；DeploymentCandidate 显式携带 Project，且 Incident 与 DeploymentEvent 必须同属该项目。合法关联必须先通过正向夹具，拒绝测试不能仅依赖字段缺失造成的 SQL 错误。
- 历史对象采用 `ON DELETE RESTRICT`。Project、Service、Release 和 DataSourceMapping 等配置对象可以软删除；被报告、证据或工具调用引用的记录不得物理删除后留下悬空引用。
- `Down` 会删除领域表和其中的数据，只允许用于隔离的本地或测试环境。生产回滚优先使用兼容的前向迁移；执行 `down` 或删除数据必须获得明确授权。

### Investigation 状态机

- 状态顺序固定为 `RECEIVED → MATCHED → COLLECTING → HYPOTHESIZING → VERIFYING → COMPLETED | INSUFFICIENT_EVIDENCE | FAILED`。不得跳过阶段、回退或从终态继续转换。
- Go 领域层负责状态规则和稳定错误语义；数据库触发器作为第二道完整性防线。调用方必须提交期望版本，仓储使用行锁和条件版本推进实现乐观并发控制。
- 每次创建和合法转换都写入 `investigation_transition_audits`。非法转换或过期版本必须回滚，不产生部分状态或审计写入。审计记录的 `ON DELETE RESTRICT` 外键同时阻止删除 Investigation，避免先删除状态历史再删除调查对象。
- 重试不能覆盖原 Investigation，应使用新的 `attempt` 和独立记录保留历史。

### 不可变诊断历史与事务边界

- Evidence、Hypothesis、Report、Hypothesis-Evidence 关系和转换审计记录创建后不可更新或删除。ToolCall 进入成功或失败终态后不可原地修改。
- 证据通过 `investigation_id` 与调查绑定；ToolCall 引用、Hypothesis 和 Evidence 的关联都带同一调查约束，禁止跨调查拼接证据。
- 幂等写入在事务中完成。唯一冲突时读取并返回已存在对象，调用方不得创建第二个业务对象。
- Deployment、Alert 等带时间窗的写入入口在开启数据库事务前拒绝空开始时间和倒置区间，返回稳定的输入错误。
- Report 读取使用只读、可重复读事务，一次读取报告、其 Evidence 和关联 DeploymentEvent，保证返回同一数据库快照。

### HTTP 读取契约

- API 只暴露经过字段选择的只读 DTO，成功响应为 `{"data":...,"request_id":"..."}`，错误响应为 `{"error":{"code","message"},"request_id":"..."}`。
- 每个请求复用合法的 `X-Request-ID`，否则生成 UUID。路径 ID 必须是规范 UUID，非法 ID 在访问仓储前返回 `400`。
- 未找到、版本冲突和依赖错误使用稳定错误码；内部错误只写脱敏日志，不向浏览器返回连接串、内部地址、堆栈或 SecretRef 明文。
- 分页参数必须显式设置默认值和上限。当前首个交付只提供按 ID 读取 Report、Evidence 和 DeploymentEvent，列表与筛选能力后续按独立 Issue 增加。

## 被否决的方案

- 使用单个 `JSONB` 聚合保存全部诊断数据：查询、引用完整性、不可变约束和并发状态更新都难以可靠实现。
- 直接使用 GitHub SHA、Grafana 指纹或外部幂等键作为主键：不同数据源和重投递会产生冲突，也无法稳定支持内部关联。
- 只在 Go 中实现状态机：绕过 API 的迁移、脚本或后续 Worker 仍可能写入非法状态。
- 通过原地更新 Evidence 或 Report 修正诊断：会丢失当时决策依据，无法审计 Agent 和人工判断的变化。

## 后果

核心关系、幂等性和终态保护由 PostgreSQL 强制，Go 单元测试、真实 PostgreSQL 集成测试和 Compose 冒烟可以分别验证各层。代价是迁移和事务测试更重，新增实体或状态时必须同步领域代码、数据库约束、API DTO 和文档。

当前只读端点用于验证可追溯读取契约，不表示连接器、Agent、工作台和通知已经交付。身份认证、多租户和保留期自动清理仍待后续设计，不能把当前 schema 直接视为生产级权限或数据生命周期方案。
