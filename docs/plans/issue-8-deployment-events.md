# Issue #8 DeploymentEvent 接入实现计划

**目标**：把实际部署记录作为发布事实源，提供受认证的 CI/CD 接入、管理员人工登记/修正、幂等状态推进和告警时间窗候选查询。

**需求依据**：Issue #8、Proposal #1 第 7.4、7.6、8、9 节，ADR 0002、0003 及仓库 `AGENTS.md`。

## 范围与设计选择

- 使用独立的 `RELYTHRA_DEPLOYMENT_WEBHOOK_SECRET_REF` HMAC-SHA256 密钥；签名覆盖时间戳和原始 body，固定时间比较，并限制时间戳偏差和 body 大小。相同外部部署 ID 的重试幂等返回同一事件；过期签名拒绝。密钥缺失时回调端点关闭。
- 外部部署身份由来源系统、外部部署 ID、Service 和 Environment 组成，映射到现有幂等唯一键。负载中的 Service/Environment/Release/版本/Commit 与数据库记录交叉验证；Release 的 Commit 或镜像摘要不一致时保留两侧事实并标记冲突，不宣称其已匹配。
- HMAC 的有效时间戳窗口为 ±5 分钟，并将已接受签名的不可逆摘要写入唯一约束表；相同签名重放拒绝，不同时间戳重签的同一外部部署 ID 仍走事件幂等/状态更新。
- 摘要登记采用数据库唯一键原子 Claim；仅保留到请求签名的最大有效期（签名时间戳 + 5 分钟），后续新 Claim 清理已过期摘要，不保存签名头或密钥明文。
- 数据库新增显式一致性状态、原始回调摘要/负载保留和不可变状态变更审计表。状态更新在单事务中锁定事件、验证允许的状态转移、更新当前状态并追加审计；相同状态重放为幂等，不允许终态任意覆盖。
- CI/CD 签名回调使用来源身份作为 actor；人工登记和修正复用现有管理 Token，要求审计 recorder 可用，操作者由配置的管理 actor 标识提供，不从不可信请求体采信身份。
- 告警候选查询限定 project/service/environment 和显式有上限的时间窗；返回全部匹配事件并标注开始/结束与版本/来源冲突，不以“最近一条”替代多个候选。当前没有告警写入 API，本 Issue 只交付可复用仓储查询及受控 API。
- 不假定有真正的用户身份系统；人工操作身份来自受信任配置，且不能声称等同多用户 RBAC。GitHub Release 不创建部署事件。

## 任务

### Task 1：部署事件领域规则

**文件**：`internal/domain/status.go`、测试及新增部署事件验证/状态文件。

先增加失败测试，覆盖来源/外部 ID、状态枚举、缺省结束时间、乱序/重复状态、时间窗与候选窗口上限；实现状态迁移表和输入边界后复测。

### Task 2：PostgreSQL 事务仓储与迁移

**文件**：新增 `migrations/000006_deployment_event_ingestion.sql`、`internal/store/postgres.go` 与集成测试。

添加版本/摘要冲突字段、不可变 `deployment_event_updates` 与 webhook 签名摘要去重表。测试创建幂等、同键不同内容冲突、签名重放拒绝、状态事务审计、终态保护、Release/Service/Project 不匹配、时间窗多候选、候选数量上限及回滚。

### Task 3：Webhook 与人工管理 API

**文件**：新增 `internal/api/deployment_handler.go`、`deployment_signature.go` 及测试，修改 `internal/api/handler.go`、`cmd/relythra-api/main.go`、`internal/config/config.go` 及相关测试。

提供 `POST /api/v1/deployment-events/webhook`、管理端 `POST /api/v1/deployment-events`、`PATCH /api/v1/deployment-events/{id}` 和受限 `GET /api/v1/deployment-events/candidates`。请求大小、JSON 单文档、未知字段、签名时间窗、错误脱敏、审计不可用时失败关闭均有测试。更新必须要求期望状态/版本，不能通过 PATCH 任意改写来源原始事实。

### Task 4：文档和完整验证

**文件**：新增 ADR 与本计划，更新 `.env.example`、README、Compose 或 CI 的必要配置。

跑 Go 格式、vet、全测试、build、真实 PostgreSQL 迁移集成测试、Web 检查、Compose 配置/适用冒烟、`git diff --check`。PR 标记为高风险，明确签名密钥配置、兼容性、迁移/回滚限制及未覆盖项。

## 回滚与数据边界

迁移回滚仅在隔离库使用；`Down` 会删除新增变更审计数据和冲突状态字段。生产恢复优先使用兼容前向迁移。原始 payload 只保留经过统一脱敏的 JSON，不存 Authorization、签名头或密钥；输入超限、脱敏失败或审计不可用均拒绝写入。

## 记录

- 初始实现分支从 `origin/main` 的 `8156fc9` 创建。发现 PR #39 同时占用 `000005` 后，将本分支叠到 `origin/feat/7-diagnosis-case-contract`，保证 Issue #8 的 `000006` 依序应用；PR #40 暂以 PR #39 为 base，PR #39 合并后再改为 `main`。
- Ruling：保留并采用显式迁移 `000006`，不把评测隔离字段迁移改写或压入 Issue #8。数据库迁移必须按版本单调前进；若 PR #39 顺序改变，先更新本分支基线和 PR base，再继续交付。
- Ruling：事件幂等与签名重放是两个独立保证。幂等键允许新签名重试同一外部部署；数据库保存签名摘要唯一键以拒绝同一签名在有效期内再次被接受，不保存签名或共享密钥明文。
- 若现有领域 schema 的状态枚举、发布引用语义或安全装配与上述计划冲突，先追加 Ruling 并修改计划，再继续实现。
