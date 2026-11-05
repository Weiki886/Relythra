# ADR 0007：DeploymentEvent 接入与签名重放防护

- 状态：Accepted（Issue #8 实施边界，待 PR 人工评审）
- 日期：2026-11-05
- 关联：Issue #8、Proposal #1、ADR 0002、ADR 0003

## 背景

发布诊断必须以实际部署作为发布事实源。GitHub Release、Commit 或最新分支状态都不能证明某个 Service/Environment 已部署。CI/CD 回调还需要抵御伪造、重放和重复投递，同时保留人工补录及修正的可追溯历史。

## 决策

- 只允许经过部署专用 SecretRef HMAC-SHA256 验证的回调创建来源为 CI/CD 的 DeploymentEvent。签名覆盖 Unix 秒时间戳与原始请求体，接受窗口为当前服务端时间前后 5 分钟，比较使用常量时间函数，body 限制为 256 KiB。
- 回调签名在 JSON 请求校验通过后登记不可逆 SHA-256 摘要。PostgreSQL 唯一键提供并发原子 Claim；已 Claim 的同一签名返回 `409 replayed_signature`，不触及部署事件写入。仅保存到该时间戳的最大有效期结束，不存签名头或密钥明文。幂等部署 ID 与签名重放是独立保证；同一外部部署使用新时间戳签名可继续走业务幂等和合法状态更新。
- SecretRef 和来源标识必须成对配置，且与管理 Token、数据源凭据和通知凭据隔离。Webhook 未配置、审计不可用、签名无效或签名过期时拒绝处理。
- DeploymentEvent 才是已部署事实；Release 只用于代码版本/Commit/镜像摘要交叉校验。冲突被明确保留为来源不一致，不覆盖来源事实，也不把 GitHub 事件升级成部署。
- CI/CD 事件与人工登记/修正都保留请求/来源、操作者、版本与审计历史。人工 actor 来自可信部署配置，不是多用户身份认证。状态变更经过领域状态机和 PostgreSQL 事务/版本检查。
- 候选查询受项目、服务、环境和最大 24 小时时间窗约束，返回所有匹配部署；多项结果标记 `AMBIGUOUS`，空结果标记 `NO_DEPLOYMENT_CONTEXT`。

## 明确不做

- 不触发部署、回滚、重启、扩容或其他基础设施写操作。
- 不把 Commit、Pull Request 或 GitHub Release 自动创建为 DeploymentEvent。
- 不提供多租户 RBAC、Webhook 身份目录或生产级公网暴露方案。

## 安全、迁移与回滚

风险级别为 high，因为该入口写入诊断唯一发布事实并接受外部请求。部署前需配置 API 专用 HMAC SecretRef，并先应用迁移 `000006`。迁移增加事件版本与来源一致性字段、不可变更新历史和短期签名摘要表。`Down` 会删除新增审计/重放数据及一致性字段，只适用于隔离测试库；生产恢复应通过兼容前向迁移。Webhook Secret 必须从环境变量或受保护文件注入，且必须脱敏，不得记录原文。

本实现的 HMAC 仅认证共享密钥持有者，不提供请求主体的独立身份、授权范围或不可抵赖性。回调成功后仍需结合原始脱敏 payload、来源身份和 Release 信息核验，不应将部署事件本身视为根因证明。
