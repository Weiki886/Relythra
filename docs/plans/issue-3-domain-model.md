# Issue #3 领域模型实现计划

**目标**：建立可迁移、可约束、可审计的发布诊断领域模型，并交付 Investigation 状态机、幂等写入和报告可追溯读取契约。

**架构**：PostgreSQL 是事实源，SQL 约束负责跨行唯一性、引用完整性和终态保护；Go 领域包负责稳定标识、UTC 时间、状态转换和错误语义；仓储使用事务与条件更新实现乐观并发；API 只暴露脱敏后的只读 DTO。

**技术栈**：Go 1.26、`database/sql`、pgx stdlib、PostgreSQL 17、goose。

**需求依据**：Issue #3、Proposal #1 的 7.4、7.5、7.6、9 节，以及仓库 `AGENTS.md`。

## 全局约束

- 所有持久化时间使用 `timestamptz`，业务时间统一转 UTC，同时保留外部来源的原始时间与时区字段。
- 主键使用数据库生成的稳定 UUID；外部 ID 与幂等键不能替代内部主键。
- `DeploymentEvent` 是发布关联的唯一事实源，GitHub Release 或最新 Commit 不是部署事实。
- 终态 Investigation、Evidence、ToolCall、Hypothesis、Report 和审计事件不可原地改写。
- 非法状态转换、重复幂等键和乐观并发冲突返回稳定错误码，且不产生部分写入。
- 配置对象采用软删除；被历史报告引用的部署、告警、证据和工具调用保留可解析引用。
- 公开 API 不返回 SecretRef 明文或数据库内部错误。

---

### Task 1: 领域原语与 Investigation 状态机

**文件**

- 新增：`internal/domain/id.go`
- 新增：`internal/domain/id_test.go`
- 新增：`internal/domain/status.go`
- 新增：`internal/domain/status_test.go`
- 新增：`internal/domain/time.go`
- 新增：`internal/domain/time_test.go`

**接口**

- 产出 `NewID() (string, error)` 与 `IsID(string) bool`。
- 产出 `InvestigationStatus`、`DeploymentMatchStatus`、`IncidentStatus`、`NotificationStatus` 及其校验方法。
- 产出 `Investigation.Transition(to InvestigationStatus) error`。
- 产出 `NormalizeTime(time.Time) time.Time` 与 `ValidateTimeRange(start, end time.Time) error`。

- [x] **步骤 1：写失败测试**
  - UUID v4 格式与变体正确，连续生成的 ID 不同。
  - 7.6 所有合法转换成功，跳过阶段、回退和终态后继续转换失败。
  - UTC 归一化不改变时刻，空时间和倒置时间窗失败。
- [x] **步骤 2：运行 `go test ./internal/domain -run 'Test(NewID|Investigation|Normalize|Validate)'`，确认因类型与函数缺失失败。**
- [x] **步骤 3：实现最小编码器、枚举校验、转换表和稳定 `TransitionError`。**
- [x] **步骤 4：重跑同一命令，确认全部通过。**
- [x] **步骤 5：提交 `feat(domain): add identifiers and investigation state machine`。**

### Task 2: PostgreSQL 领域 schema 与不可变约束

**文件**

- 新增：`migrations/000002_domain_model.sql`

**接口**

- 创建 Project、Service、Release、DeploymentEvent、DataSourceMapping、Alert、Incident、Investigation、ToolCall、Evidence、Hypothesis、DiagnosisCase、Report、Notification。
- 创建 Deployment 候选关系、Hypothesis-Evidence 关系和 Investigation 转换审计表。
- 为服务/环境/发布时间窗、告警指纹、幂等键、外部 ID、状态和关系建立唯一约束与索引。
- 创建 `enforce_investigation_transition()`、`touch_updated_at()` 和 `reject_mutation()` 触发器。

- [x] **步骤 1：先用空迁移 Down/Up 定义约束测试场景，确认当前缺少 `000002` 会导致表检查失败。**
- [x] **步骤 2：运行空库迁移检查，确认失败原因是领域表不存在。**
- [x] **步骤 3：实现完整 schema、约束、索引与触发器。**
- [x] **步骤 4：从空库执行 `up`、重复执行 `up`、检查 `down` 与再次 `up`。**
- [x] **步骤 5：提交 `feat(data): add domain schema and integrity constraints`。**

### Task 3: 事务仓储、幂等写入与乐观并发

**文件**

- 新增：`internal/store/postgres.go`
- 新增：`internal/store/postgres_test.go`
- 新增：`internal/store/errors.go`

**接口**

- 产出 `Postgres`、`NewPostgres(*sql.DB)`。
- 产出 `CreateDeploymentEvent` 与 `CreateAlert`，重复幂等键返回同一 ID。
- 产出 `TransitionInvestigation`，要求调用方提供期望版本；非法状态或版本冲突不修改数据库。
- 产出 `GetReport`、`GetEvidence` 与 `GetDeploymentEvent` 只读方法。
- 产出 `ErrNotFound`、`ErrConflict`、`ErrInvalidTransition`。

- [ ] **步骤 1：写仓储接口测试，覆盖幂等写入、条件更新、非法转换和终态保护。**
- [ ] **步骤 2：在真实 PostgreSQL 运行相关测试，确认因仓储不存在而失败。**
- [ ] **步骤 3：用参数化 SQL 和事务实现最少仓储能力，错误分类不泄露数据库信息。**
- [ ] **步骤 4：重跑仓储测试，确认数据库状态与返回值符合契约。**
- [ ] **步骤 5：提交 `feat(store): persist domain transitions idempotently`。**

### Task 4: 统一 HTTP 契约与可追溯只读 API

**文件**

- 新增：`internal/api/response.go`
- 新增：`internal/api/response_test.go`
- 新增：`internal/api/handler.go`
- 新增：`internal/api/handler_test.go`
- 修改：`cmd/relythra-api/main.go`

**接口**

- 产出统一成功包络 `{"data":...,"request_id":"..."}` 和错误包络 `{"error":{"code","message"},"request_id":"..."}`。
- 产出 `X-Request-ID` 中间件、稳定错误码和有界分页参数。
- 产出 `GET /api/v1/reports/{id}`、`GET /api/v1/evidence/{id}`、`GET /api/v1/deployments/{id}`。
- 仓储接口由 handler 注入，单元测试使用受控 fake，真实数据库链路由冒烟覆盖。

- [ ] **步骤 1：写失败测试覆盖 request ID 复用/生成、错误包络、无效 ID、404、依赖错误脱敏和成功读取。**
- [ ] **步骤 2：运行 API 单元测试，确认因路由与响应函数缺失失败。**
- [ ] **步骤 3：实现响应辅助函数、请求 ID 中间件、只读路由和 API 装配。**
- [ ] **步骤 4：重跑 API 与 health 测试，确认路由兼容且错误稳定。**
- [ ] **步骤 5：提交 `feat(api): expose traceable domain read contracts`。**

### Task 5: ADR、运行文档与真实 PostgreSQL 冒烟

**文件**

- 新增：`docs/adr/0002-domain-persistence.md`
- 修改：`README.md`
- 修改：`scripts/smoke.sh`

**接口**

- ADR 记录 UUID、迁移、状态机、不可变历史、软删除、保留和事务边界。
- README 记录领域 schema、只读端点和验证命令。
- 冒烟脚本从空卷迁移后验证表/索引、重复幂等、非法状态回滚和报告关联读取。

- [ ] **步骤 1：修改冒烟脚本加入失败的表与约束检查，对当前 schema 运行并确认失败。**
- [ ] **步骤 2：运行 `RELYTHRA_POSTGRES_PASSWORD=ci-only-not-secret make smoke`，确认缺少领域表的失败原因。**
- [ ] **步骤 3：补充 ADR、README 和真实 PostgreSQL SQL 断言。**
- [ ] **步骤 4：执行 `make fmt-check vet test build` 与完整 `make smoke`。**
- [ ] **步骤 5：提交 `docs(data): document domain persistence contract`。**

## 验收映射

| Issue 验收项 | 证据 |
| --- | --- |
| 空库迁移、重复迁移、Down/Up | `make smoke` 的空卷启动、重复 `migrate up` 与迁移断言 |
| 合法转换持久化、非法转换回滚 | domain 单元测试、store 集成测试、SQL 触发器冒烟 |
| 重复部署/告警返回稳定 ID | store 幂等测试与唯一约束冒烟 |
| 报告按 ID 回读 Evidence/DeploymentEvent | API handler 单元测试与真实 PostgreSQL 关联查询 |
