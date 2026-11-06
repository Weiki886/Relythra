# ADR 0008：Grafana 告警接入、去重与 Investigation 创建

- 状态：Accepted（Issue #9 实施边界，待 PR 人工评审）
- 日期：2026-11-06
- 关联：Issue #9、Proposal #1、ADR 0002、0003、0004、0007

## 背景

Grafana Webhook 是诊断入口。重试必须可安全复用同一告警；标签缺失时必须避免错误匹配服务；恢复通知不能误开新调查。数据库已有 Alert、Incident、Investigation 和 DeploymentCandidate 基础模型，但缺少每次投递留痕与从告警原子创建这些领域对象的仓储操作。

## 决策

- 接受 Grafana 通用 Webhook JSON envelope 的明确字段，包括标准 `values` 数值映射，启用严格解码并拒绝未知字段、额外 JSON 文档和越界数据。单 body 最大 1 MiB，最多 100 alerts；labels、annotations、values 项数和字符串长度有上限。每条 alert 的 `generatorURL` 必须是无凭据 HTTPS URL。
- 使用独立 `RELYTHRA_GRAFANA_WEBHOOK_SECRET_REF` 和 HMAC-SHA256。签名覆盖 Unix 秒时间戳和原始 body，接受窗口 ±5 分钟。签名有效且所有 alert 均已解析后，才以数据库唯一键 Claim 签名摘要；同一签名第二次到达返回 `409 replayed_signature`。摘要复用部署 Webhook 的短期登记表和过期清理策略。
- `project`、`service`、`env` 标签分别精确解析 Project slug、Service slug 和环境；`commonLabels/commonAnnotations` 与逐条标签合并，逐条值优先。不按 Service 展示名或近似文本猜测。项目 slug 不存在时返回 `422 needs_mapping`，不创建无 Project 归属的领域对象；项目有效但服务/环境未匹配时，保存 Alert、Incident 和投递记录为待映射，不创建 Investigation。
- fingerprint 优先采用 Grafana 提供值；缺失时使用带 `sha256-v1` 版本标记的 SHA-256，输入由 Project/Service/Environment、开始时间和排序后的 labels 组成。开始时间以 UTC 参与匹配，同时保留原始 RFC3339 时间文本和偏移。相同 Project + fingerprint 在开始时间 ±5 分钟内去重；PostgreSQL 事务级 advisory lock 串行化并发查询/写入。
- 一个 envelope 在单个数据库事务内写入全部 Alert、Incident、Investigation、初始 Investigation 状态审计、部署候选和 `alert_deliveries`；任何一条无法落库时整批回滚。每个 alert 项都保存脱敏 envelope、payload SHA-256、request ID、状态、映射结果和接收时间；同一 request ID 下允许多条 delivery，因此该字段不作为 delivery 唯一键。delivery 表只追加。审计 recorder 在业务事务提交后记录 envelope 级摘要，失败时接口报审计不可用但已提交对象仍保留，可用新签名安全重试。
- 同指纹告警复用原 Alert/Incident/Investigation；FIRING 可创建缺失的 Investigation，RESOLVED 更新 Alert 与 Incident 的结束状态但不创建 Investigation。告警登记不是 Agent 启动信号。
- 管理员通过 `POST /api/v1/alerts/{id}/remap` 精确指定 Service 和 Environment；请求要求 Alert `expected_version`。在一个事务中更新 Alert、Incident 并补建 Investigation/部署候选。旧版本返回冲突，动作写入不可变管理审计。
- 部署候选窗口以告警 `startsAt` 为中心前后各 30 分钟，最多保留 100 条；没有部署时 Investigation 仍可创建并显式无部署上下文。当前仅支持人工重映射，不提供通用手工重放 API。

## 明确不做

- 不处理 Grafana 插件私有的非 JSON 数据或任意格式兼容。
- 不执行模糊服务匹配，不因告警接收自动运行 Agent 或发送通知。
- 不改变部署、基础设施或服务配置；不把告警当作根因结论。
- 不提供多租户身份认证、每 Project 独立 Webhook 密钥映射或通用人工重放框架。

## 安全、迁移与回滚

风险级别为 high：该入口接受外部请求并创建诊断核心对象。配置缺失时路由关闭；签名错误、重放、解析错误、超限输入及无效 Project 都不得写业务对象。迁移 `000007` 添加 fingerprint 算法版本、Alert 乐观版本、Alert-Incident 复合归属、delivery 历史表和管理审计动作。`Down` 仅可用于隔离环境，会丢弃投递历史和新字段；生产恢复使用兼容前向迁移。业务事务和通用安全审计事务不跨库绑定，审计写失败后的安全重试由新的 HMAC 时间戳完成，Alert 去重确保不会重复建立 Investigation。

当前映射仅依赖由 HMAC 保护的 `project/service/env` 标签和本地 Project/Service 配置。若未来允许多个独立 Grafana 租户，需要新增显式密钥身份/项目授权映射，不能仅依赖 payload 自报租户名。
