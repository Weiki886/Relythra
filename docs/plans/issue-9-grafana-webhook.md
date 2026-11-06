# Issue #9 Grafana Webhook 实施计划

**目标**：接收经过认证的 Grafana 告警通知，严格规范化告警并保留投递历史，精确匹配服务；将未映射告警留给人工处理，匹配成功时事务化创建或复用 Incident/Investigation，并附加全部部署候选。

**需求依据**：Issue #9、Proposal #1、ADR 0002、0003、0004、0007 与仓库 `AGENTS.md`。

## 决策

- 仅支持 Grafana 通用 Webhook JSON envelope 的明确字段（含 `values`）；拒绝未知结构、空/超大数组、超长字符串、非法状态、非法时间范围和超限 labels/annotations/values。每个 envelope 限制 100 个 alert，body 限制 1 MiB。
- 认证沿用独立 Webhook HMAC 模式，使用 Grafana 专用 SecretRef、时间戳与签名头；request 签名被原子 Claim 一次。一个已认证 envelope 的所有 alert 必须先全部解析校验，再写入，避免格式错误造成部分创建。
- `project`、`service`、`env` 是唯一映射标签，值分别精确匹配 Project slug、Service slug 和规范环境名；不使用服务名称模糊猜测。缺少/未知项目无法安全创建任何 Project 归属对象，返回 `needs_mapping` 且不创建业务对象；项目有效但 Service/Environment 缺失或无效时，创建 `service_id/environment` 为空的待映射 Incident 并保存原始脱敏告警。
- Alert 表是规范化告警当前状态；增加追加式 `alert_deliveries` 保存每个告警项的 request_id、接收时间和状态结果。同一 request ID 可以关联同一 alert 的多条 delivery。相同 fingerprint + 去重窗口复用 Alert/Incident/Investigation，不新开 Investigation；RESOLVED 更新原告警与 Incident 结束时间，不重复开启诊断。
- 匹配成功时在同一 PostgreSQL 事务中创建/更新 Alert、Incident、Investigation、初始 Investigation 审计与 deployment_candidates。部署候选窗口以 `startsAt` 为中心前后各 30 分钟，最多 100 条；缺部署时 Investigation 仍可创建并显式无部署上下文。
- 人工重映射通过受管理 Token 的显式动作执行；保持原始脱敏 payload 与投递历史，使用乐观版本并追加审计，不触发 Agent。当前不提供通用手工重放 API。

## 任务与验证

1. 为 Grafana envelope、标准 `values` 字段、原始时区/时间文本、fingerprint 派生版本、标签/文本上限、状态恢复和时间窗先增加失败测试，再实现严格解析及稳定错误。
2. 新增 `000007_grafana_alert_ingestion.sql`，增加 delivery 追加审计及必要关联/去重索引；真实 PostgreSQL 测试覆盖迁移往返、并发重复、同 request 多 delivery、部分失败回滚、恢复更新、状态/租户归属与候选完整性。
3. 新增仓储事务入口，原子完成 Alert/Incident/Investigation/审计/部署候选；API fake 验证签名失败、重放、解析失败零副作用、重复复用、待映射与成功路径。
4. 新增 Grafana 专用 SecretRef 配置、Webhook/管理重映射路由、统一脱敏审计与 README/ADR；只读接入，不实现 Agent/通知。
5. 执行 `make check`、真实 PostgreSQL 全套、gitleaks、`git diff --check`；提交、推送并创建以 PR #40 分支为 base 的 PR。

## 风险与回滚

风险级别 `high`：这是诊断入口且会创建核心领域对象。Webhook 密钥独立注入，认证/签名错误、重放、payload 超限和解析错误必须在任何业务写之前失败。迁移 `Down` 仅用于隔离数据库；生产恢复采用兼容前向迁移。Incident 不提供项目归属时不能创建，返回可操作的待映射错误，要求补充有效 `project` 标签或专用端点配置。

## 开放限制

- 同一共享 Secret 可签名多个 Project 的 Alert；项目归属由签名保护的 `project` slug 标签给出。若后续需要每项目独立密钥/授权，单独设计密钥身份映射。
- fingerprint 缺失时采用版本化 SHA-256 派生：规范化的 project/service/environment、alertname、startsAt 和排序后的稳定 labels；不把 annotations 或易变 value 拼入身份。
- Grafana 单次 envelope 以单个数据库事务落库，保证全有或全无；已接收签名一经 Claim，即使后续数据库临时失败也不能用同一签名重试，发送方应以新时间戳重新签名。
