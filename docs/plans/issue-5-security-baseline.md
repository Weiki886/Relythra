# Issue #5 安全基线实现计划

**目标**：建立可验证的 SecretRef 注入、结构化脱敏、不可信数据边界、Prompt 白名单和不可变审计基线，确保 canary Secret 不进入日志、HTTP 响应、审计、Evidence 或模型上下文。

**架构**：运行时凭据只通过显式 `env:` 或 `file:` SecretRef 解析；所有日志、结构化对象、审计参数和 Prompt 字段经过统一过滤器；Prompt 输入使用拒绝未知字段的版本化 DTO；PostgreSQL 审计表只允许追加。任何无法安全脱敏的外发路径都失败关闭。

**技术栈**：Go 1.26 标准库、`log/slog`、PostgreSQL 17、goose。

**需求依据**：Issue #5、Proposal #1、ADR 0002、仓库 `AGENTS.md`。

## 全局约束

- 公开仓库中的测试凭据全部是可识别的虚构 canary，不读取开发者真实环境。
- SecretRef 只保存引用名；Secret 值不能出现在错误、日志、JSON、审计参数或 Prompt。
- 脱敏同时保留字段名；已知 Secret 残留时返回错误，调用方不得回退为原文。
- 日志、日志摘要和证据必须通过显式截断标记限制长度，不能静默丢弃尾部。
- `DiagnosisCase.expected_root_cause`、`injection_method`、评分规则和任意未知 Prompt 字段不进入运行时上下文。
- 审计记录只追加，数据库触发器拒绝更新和删除。
- `000003_security_audit.sql` 只增加新表、触发器和索引；已合入迁移保持不可变。

## Task 1：SecretRef 解析与安全值对象

**文件**

- 新增：`internal/security/secrets/secret.go`
- 新增：`internal/security/secrets/secret_test.go`
- 修改：`internal/config/config.go`
- 修改：`internal/config/config_test.go`

**接口**

- `ParseReference(string) (Reference, error)` 只接受 `env:NAME` 和 `file:/absolute-path`。
- `Resolver.Resolve(context.Context, Reference) (Secret, error)` 从注入的 `LookupEnv` 或受限文件读取器解析非空值。
- `Secret` 不实现可逆的默认格式化；只有显式 `Reveal()` 返回运行时原值，日志和 JSON 表示固定为 `[REDACTED]`。
- 空引用、未知 scheme、缺失环境变量、空文件、超限文件和读取失败返回稳定错误，不包含秘密内容。
- 后续连接器配置缺少必需 SecretRef 时在启动阶段返回错误。

- [x] **步骤 1：写失败测试**
  - 合法 env/file 引用可解析，文件仅移除一个结尾换行。
  - 缺失、空、未知 scheme、相对 `file:` 与超限输入失败。
  - canary 不出现在错误、`fmt`、JSON 或 `slog` 输出。
- [x] **步骤 2：运行 `go test ./internal/security/secrets ./internal/config`，确认 RED。**
- [x] **步骤 3：实现引用解析、受限读取、安全值对象和配置校验。**
- [x] **步骤 4：重跑目标测试，确认 GREEN。**
- [x] **步骤 5：提交 `feat(security): add secret and redaction primitives`（SecretRef 与脱敏原语同一可构建提交）。**

## Task 2：统一文本与 JSON 脱敏

**文件**

- 新增：`internal/security/redact/filter.go`
- 新增：`internal/security/redact/filter_test.go`

**接口**

- `NewFilter(knownSecrets ...string) (*Filter, error)` 拒绝无法安全处理的空配置并编译固定规则。
- `Filter.Text(context.Context, string) (string, error)` 处理 Authorization、Bearer、URL userinfo/query token、常见 Token/私钥格式和已知 Secret。
- `Filter.JSON(context.Context, any) (any, error)` 递归脱敏敏感键，保留键名；序列化或校验失败时返回错误。
- `Filter.Untrusted(context.Context, source, text string, limit int) (UntrustedData, error)` 返回带 `trust=untrusted_data`、原字节数和 `truncated` 标记的边界对象。
- 脱敏后再次扫描已知 Secret；仍有残留时返回 `ErrUnsafeOutput`。

- [x] **步骤 1：写失败测试**
  - Header、URL、JWT、GitHub/Slack/AWS/OpenAI 风格 Token 和 PEM 私钥被替换。
  - JSON 敏感键递归替换且键名保留，普通诊断字段保留。
  - 多字节文本按字节上限截断后仍是合法 UTF-8，并显式标记截断。
  - 无法清除的已知 Secret 使外发失败。
- [x] **步骤 2：运行 `go test ./internal/security/redact`，确认 RED。**
- [x] **步骤 3：实现固定规则、递归 JSON 过滤器、截断边界和残留检测。**
- [x] **步骤 4：重跑目标测试，确认 GREEN。**
- [x] **步骤 5：提交 `feat(security): add secret and redaction primitives`（与 Task 1 同一可构建提交）。**

## Task 3：Prompt 白名单与不可信数据隔离

**文件**

- 新增：`internal/security/prompt/context.go`
- 新增：`internal/security/prompt/context_test.go`

**接口**

- `DecodeContext(io.Reader, *redact.Filter) (Context, error)` 使用 `DisallowUnknownFields`、单文档检查和 1 MiB 上限。
- `Context` 只包含版本、调查/服务/环境、告警摘要、运行时证据、代码变更和工具结果白名单字段。
- Evidence/日志/代码片段使用 `redact.UntrustedData` 包裹；每个字段和集合有上限。
- `expected_root_cause`、`injection_method`、`scenario_metadata`、评分规则、Secret 与未知字段被拒绝。
- 已知 Secret 在构造上下文时经过过滤器；过滤失败则不返回 Prompt。

- [x] **步骤 1：写失败测试**
  - 合法 v1 上下文可解码并序列化为 `untrusted_data`。
  - DiagnosisCase 答案/注入/评分字段和未知字段被拒绝。
  - 超长集合、字段和文档被拒绝；canary 不进入输出。
- [x] **步骤 2：运行 `go test ./internal/security/prompt`，确认 RED。**
- [x] **步骤 3：实现版本化 DTO、严格解码、长度约束和字段脱敏。**
- [x] **步骤 4：重跑目标测试，确认 GREEN。**
- [x] **步骤 5：提交 `feat(security): isolate model prompt context`。**

## Task 4：不可变审计事件与 PostgreSQL 记录器

**文件**

- 新增：`internal/security/audit/event.go`
- 新增：`internal/security/audit/postgres.go`
- 新增：`internal/security/audit/postgres_test.go`
- 新增：`migrations/000003_security_audit.sql`
- 修改：`migrations/domain_model_test.go`
- 修改：`scripts/smoke_domain.sql`

**接口**

- `Event` 记录 actor、action、object type/id、result、request ID、脱敏参数摘要和发生时间。
- `NewPostgresRecorder(*sql.DB, *redact.Filter)` 在依赖缺失时失败。
- `Recorder.Record` 在写库前验证必填字段、脱敏参数并限制参数 JSON 大小。
- `audit_events` 只追加；触发器拒绝 UPDATE/DELETE，索引支持对象和请求追踪。
- 迁移测试先回滚安全迁移，再回滚领域迁移；真实 PostgreSQL 测试验证审计可重建操作且不能读取 Secret。

- [x] **步骤 1：写失败测试**
  - 缺依赖、缺字段、脱敏失败、超长参数均不写库。
  - canary 和 URL/Header 敏感值写入后只能读到 `[REDACTED]`。
  - 审计事件可按 request/object/action 重建，UPDATE 和 DELETE 被数据库拒绝。
- [x] **步骤 2：运行单元测试与真实 PostgreSQL 测试，确认 RED。**
- [x] **步骤 3：实现事件模型、记录器、`000003` 迁移和数据库不可变触发器。**
- [x] **步骤 4：重跑迁移、审计和仓储测试，确认 GREEN。**
- [x] **步骤 5：提交 `feat(audit): persist immutable redacted events`。**

## Task 5：API 外发边界接入

**文件**

- 修改：`internal/api/handler.go`
- 修改：`internal/api/response.go`
- 修改：`internal/api/handler_test.go`
- 修改：`cmd/relythra-api/main.go`

**接口**

- 内部错误先经 `Filter.Text` 再交给 `slog`；脱敏失败只记录稳定错误码。
- API 响应经 `Filter.JSON` 后编码；无法安全序列化时返回稳定 `internal_error`，绝不写原始对象。
- `X-Request-ID`、路径和响应字段名保留，canary 不进入响应或日志。
- 现有只读端点契约保持兼容。

- [x] **步骤 1：写失败测试覆盖依赖错误中的 URL userinfo、Header 和已知 canary，断言响应和日志均无原值。**
- [x] **步骤 2：运行 `go test ./internal/api`，确认 RED。**
- [x] **步骤 3：实现脱敏日志与失败关闭的 JSON 响应路径。**
- [x] **步骤 4：重跑 API 测试，确认 GREEN。**
- [x] **步骤 5：提交 `feat(security): enforce runtime security boundaries`（API 与运行时配置接入同一可构建提交）。**

## Task 6：威胁模型、ADR 与运行文档

**文件**

- 新增：`docs/adr/0003-security-data-boundary.md`
- 新增：`docs/security/threat-model.md`
- 修改：`README.md`
- 修改：`.env.example`

**接口**

- ADR 固定 SecretRef 语法、短期值、脱敏失败关闭、Prompt 白名单、审计不可变和连接器只读边界。
- 威胁模型覆盖凭据泄漏、日志注入、Prompt 注入、评测污染、SSRF、审计篡改、重放和供应链风险。
- README 记录配置、数据边界、凭据轮换、漏洞报告和当前未覆盖风险。
- `.env.example` 只提供 `env:`/`file:` 示例引用，不包含真实值。

- [x] **步骤 1：更新文档与占位配置。**
- [x] **步骤 2：运行链接/文本检查并核对公开仓库无 canary 以外的敏感样例。**
- [x] **步骤 3：提交 `docs(security): document trust and secret boundaries`。**

## Task 7：全量验证与自审

- [x] **步骤 1：运行真实 PostgreSQL 集成测试。**
- [x] **步骤 2：运行 `make fmt-check vet test build`。**
- [x] **步骤 3：运行 `make web-check`。**
- [x] **步骤 4：运行 Compose 配置检查和隔离冒烟。**
- [x] **步骤 5：按 `code-review` 固定 `origin/main...HEAD`，检查需求、正确性、安全、迁移、兼容性和无关 diff。**
- [x] **步骤 6：修复自审问题，重复受影响验证。**
- [ ] **步骤 7：推送分支、创建关联 Issue #5 的中文 PR，设置 M1 和 Issue 标签，等待远程三项 CI。**

## 验收映射

| Issue 验收项 | 证据 |
| --- | --- |
| canary 不进入日志/响应/审计/Evidence/Prompt | secrets、redact、prompt、audit、API 单元测试和真实 PostgreSQL 测试 |
| Authorization、URL token、常见密钥格式脱敏且保留字段名 | 表驱动脱敏测试 |
| DiagnosisCase 答案和注入状态不进入模型上下文 | Prompt 未知字段与保留字段拒绝测试 |
| 审计可重建关键操作且不能读取 Secret | 审计查询测试和数据库 UPDATE/DELETE 拒绝测试 |
| 脱敏失败关闭、超长内容显式截断 | `ErrUnsafeOutput` 与 `UntrustedData.truncated` 测试 |
| 缺少 SecretRef 时连接器启动失败 | SecretRef/config 单元测试与后续连接器配置接口 |

## Ruling 与验证记录

- Ruling：Issue 要求审计表“只允许追加”，原设计仅用行级触发器拒绝 `UPDATE/DELETE`，表所有者仍可执行 `TRUNCATE`。决定增加独立的 `BEFORE TRUNCATE ... FOR EACH STATEMENT` 触发器，并同时加入真实 PostgreSQL 与 Compose 冒烟断言。错误代价是迁移多一个对象，但避免了整表审计证据可被单条语句删除的安全缺口。
- RED：`RELYTHRA_TEST_DATABASE_URL=... go test ./internal/security/audit -run TestPostgresRecorderPersistsRedactedImmutableAuditEvents -count=1` 失败，`TRUNCATE audit_events` 返回 `nil`，未产生预期的 SQLSTATE `23001`。
- GREEN：补充语句级触发器后同一测试通过，确认 `UPDATE`、`DELETE`、`TRUNCATE` 均被数据库拒绝。
- `make fmt-check vet test build`：格式、vet、全部 Go 测试和构建通过。
- `make web-check`：ESLint、TypeScript、3 个 Vitest 用例和生产构建通过。
- `RELYTHRA_TEST_DATABASE_URL=... go test ./internal/store ./internal/security/audit ./migrations -count=1`：真实 PostgreSQL 集成测试通过。
- `RELYTHRA_POSTGRES_PASSWORD_FILE=... make compose-config`：Compose 配置渲染通过。
- `RELYTHRA_POSTGRES_PASSWORD=ci-only-not-secret make smoke`：隔离 Compose 冒烟通过，覆盖迁移、领域约束、审计不可变性、可追溯 API 读取、服务健康和数据库失联行为。
- `bash -n scripts/dev.sh scripts/smoke.sh && ./scripts/dev.sh --help`：脚本语法与本地非 Docker 启动入口帮助通过。

## 回滚

- 应用回滚时先停止依赖审计写入的进程，再执行 `000003_security_audit.sql` 的 `down`；该操作删除审计数据，仅允许在隔离环境执行。
- 代码回滚可撤销安全包和 API 接入，但不得恢复记录原始 Secret 的旧行为；若必须回滚，应禁用受影响外发能力并保留失败关闭。
