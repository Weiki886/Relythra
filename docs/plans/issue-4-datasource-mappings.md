# Issue #4 数据源映射实现计划

**目标**：为 Project、Service、Environment 建立经过授权、持久化和安全校验的数据源映射，提供 Compose 标签检查、固定查询模板、连接测试、稳定错误与审计证据。

**架构**：`internal/datasource` 负责映射领域模型、运行时标签和固定模板注册表；`internal/security/outbound` 负责地址白名单、DNS 解析与连接地址锁定；`internal/connector/health` 通过注入的 HTTP client 和 Secret resolver 执行只读连接测试；`internal/store` 在 PostgreSQL 中保存带版本号的配置；`internal/api` 提供需要独立管理 Token 的 CRUD、校验、查询计划与连接测试接口。所有外部请求使用固定路径、超时、响应上限和零重定向策略。

**技术栈**：Go 1.26、`database/sql`、pgx stdlib、PostgreSQL 17、Go 标准库 `net/http`、`net`、`crypto/subtle`、现有 SecretRef、Redaction 和 Audit 包。

**需求依据**：Issue #4、Proposal #1 的 5、7.4、7.5、7.6、7.8、9、11 节，ADR 0002、ADR 0003 和仓库 `AGENTS.md`。

## 全局约束

- Project、Service 和 Environment 是映射的唯一归属；禁止在诊断时按服务名称模糊兜底。
- `runtime_type` 只接受 `docker-compose` 与 `kubernetes`；v1 连接测试只支持 `docker-compose`。
- Compose 必需标签为 `project`、`service`、`env`、`host_id`、`container_id`。
- 缺少运行时标签可以保存草稿，但 `validate`、查询和连接测试必须返回结构化 issue。
- API 不接受原始 PromQL、LogQL、SQL、shell、GitHub GraphQL、任意 HTTP method、请求头或模板正文。
- 管理 API 只接受 `Authorization: Bearer <token>`，Token 只从 SecretRef 读取；未配置 Token 时管理路由返回 `management_unavailable`。
- 数据源 endpoint 只接受 `http` 或 `https` 根地址，禁止 userinfo、query、fragment 和非根路径。
- 主机必须在显式 allowlist 中；端口必须在显式 allowlist 中；回环、链路本地、云元数据、未指定、多播和混合安全 DNS 结果一律拒绝。
- 私有地址只在 `AllowPrivate=true` 时允许；HTTP 只在 `AllowHTTP=true` 时允许。
- DNS 解析结果必须锁定到本次连接；禁止重定向；请求超时、响应大小和读取时间有上限。
- SecretRef 只保存引用，不返回明文；连接测试和 API 响应只返回配置状态、权限、连通性、版本和能力。
- 每次管理写操作、校验、查询计划、连接测试及拒绝操作都写不可变脱敏审计。
- 已合入的 `000002`、`000003` 不修改；版本、软删除唯一性和枚举约束由 `000004` 前向迁移补充。

## 授权边界裁决

Issue #4 要求“仅管理员配置”，但 Proposal 明确 v1 不做 RBAC、SSO，仓库当前也没有身份系统。本轮实现独立管理 Token、SecretRef 注入、常量时间比较和审计，并把管理 API 定位为可信内网的单管理员入口。该方案不等于多用户 RBAC；Token 缺失时管理 API 关闭，公网部署仍被禁止。

---

### Task 1: 映射领域模型、标签校验与固定模板注册表

**文件**

- 新增：`internal/datasource/model.go`
- 新增：`internal/datasource/model_test.go`
- 新增：`internal/datasource/registry.go`
- 新增：`internal/datasource/registry_test.go`

**接口**

- 产出 `type SourceType string`，常量 `SourceGitHub`、`SourceGrafana`、`SourcePrometheus`、`SourceLoki`、`SourceRuntime`。
- 产出 `type RuntimeType string`，常量 `RuntimeDockerCompose`、`RuntimeKubernetes`。
- 产出 `type ValidationIssue struct { Field, Code, Message string }`。
- 产出 `type PublicSettings struct { Endpoint, Repository string; RuntimeLabels map[string]string }`。
- 产出 `func ValidateMapping(SourceType, RuntimeType, PublicSettings, []string, *secrets.Reference) []ValidationIssue`。
- 产出 `func ValidateRuntimeLabels(RuntimeType, map[string]string) []ValidationIssue`。
- 产出 `type Template struct { ID, Version, Source, Purpose, ResultSchema string; AllowedParameters []string }`。
- 产出 `func ResolveTemplates(SourceType, []string) ([]Template, []ValidationIssue)`。
- 产出 `func ValidateQuery(Template, QueryParameters) (NormalizedQuery, []ValidationIssue)`。

- [x] **步骤 1：写失败测试**

```go
func TestValidateRuntimeLabelsReportsEachMissingComposeLabel(t *testing.T) {
	issues := ValidateRuntimeLabels(RuntimeDockerCompose, map[string]string{
		"project": "relythra",
	})
	for _, field := range []string{
		"runtime_labels.service",
		"runtime_labels.env",
		"runtime_labels.host_id",
		"runtime_labels.container_id",
	} {
		if !hasIssue(issues, field, "required") {
			t.Errorf("missing %s required issue: %#v", field, issues)
		}
	}
}

func TestResolveTemplatesRejectsTemplateFromAnotherSource(t *testing.T) {
	_, issues := ResolveTemplates(SourceLoki, []string{"prometheus.service.latency.v1"})
	if !hasIssue(issues, "template_ids", "unsupported_template") {
		t.Fatalf("issues = %#v", issues)
	}
}

func TestValidateQueryRejectsUnknownQueryText(t *testing.T) {
	template, _ := ResolveTemplates(SourcePrometheus, []string{"prometheus.service.latency.v1"})
	_, issues := ValidateQuery(template[0], QueryParameters{
		Service: "checkout-api",
		Extra:   map[string]any{"promql": "up"},
	})
	if !hasIssue(issues, "parameters.promql", "unsupported_parameter") {
		t.Fatalf("issues = %#v", issues)
	}
}
```

- [x] **步骤 2：运行 `go test ./internal/datasource -run 'Test(ValidateRuntimeLabels|ResolveTemplates|ValidateQuery)'`，确认因包、类型和函数不存在而失败。**
- [x] **步骤 3：实现最小领域模型、稳定 issue 顺序、固定模板表和仅允许 `service/start/end/limit/cursor/max_series` 的参数校验。**
- [x] **步骤 4：重跑目标测试，确认 Compose 五标签、跨源模板、未知参数和数值边界全部通过。**
- [x] **步骤 5：提交 `feat(datasource): define mapping and template contracts`。**

### Task 2: 版本化 PostgreSQL 映射仓储与迁移

**文件**

- 新增：`migrations/000004_datasource_mapping_constraints.sql`
- 新增：`internal/store/mapping.go`
- 新增：`internal/store/mapping_test.go`

**接口**

- 产出 `CreateDataSourceMappingInput`、`UpdateDataSourceMappingInput`、`DataSourceMapping`。
- 产出 `Postgres.CreateDataSourceMapping(context.Context, CreateDataSourceMappingInput) (DataSourceMapping, []datasource.ValidationIssue, error)`。
- 产出 `Postgres.ListDataSourceMappings(context.Context, projectID, serviceID, environment string) ([]DataSourceMapping, error)`。
- 产出 `Postgres.GetDataSourceMapping(context.Context, string) (DataSourceMapping, error)`。
- 产出 `Postgres.UpdateDataSourceMapping(context.Context, UpdateDataSourceMappingInput) (DataSourceMapping, []datasource.ValidationIssue, error)`。
- 产出 `Postgres.DeleteDataSourceMapping(context.Context, id string, expectedVersion int) error`。
- `DataSourceMapping` 的 JSON 不包含 SecretRef；响应 DTO 另行选择字段。

- [x] **步骤 1：写失败测试**

```go
func TestCreateDataSourceMappingAllowsDraftWithMissingRuntimeLabels(t *testing.T) {
	db := openStoreTestDB(t)
	fixture := insertStoreFixture(t, context.Background(), db, "mapping-draft")
	repository := NewPostgres(db)

	mapping, issues, err := repository.CreateDataSourceMapping(
		context.Background(),
		CreateDataSourceMappingInput{
			ProjectID: fixture.projectID,
			ServiceID: fixture.serviceID,
			Environment: "prod",
			SourceType: datasource.SourceRuntime,
			RuntimeType: datasource.RuntimeDockerCompose,
			PublicSettings: datasource.PublicSettings{Endpoint: "http://cadvisor:8080"},
			SecretRef: "env:RUNTIME_TOKEN",
		},
	)
	if err != nil {
		t.Fatalf("create draft: %v", err)
	}
	if mapping.Version != 1 || len(issues) != 5 {
		t.Fatalf("mapping = %#v, issues = %#v", mapping, issues)
	}
}

func TestUpdateDataSourceMappingRejectsStaleVersion(t *testing.T) {
	db := openStoreTestDB(t)
	fixture := insertStoreFixture(t, context.Background(), db, "mapping-version")
	repository := NewPostgres(db)
	mapping := createPrometheusMapping(t, repository, fixture)

	input := updatePrometheusMapping(mapping)
	input.ExpectedVersion = 2
	_, _, err := repository.UpdateDataSourceMapping(context.Background(), input)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
}
```

- [x] **步骤 2：运行 `RELYTHRA_TEST_DATABASE_URL=<隔离测试库> go test ./internal/store -run 'Test(Create|Update|Delete)DataSourceMapping'`，确认因仓储方法和 `version` schema 缺失而失败。**
- [x] **步骤 3：新增前向迁移，加入 `version integer NOT NULL DEFAULT 1`、部分唯一索引和来源/运行时组合约束；实现创建、读取、列表、乐观更新与软删除。**
- [x] **步骤 4：在空库执行 `up`、重复 `up` 和隔离 schema 测试，确认草稿保存、版本冲突、软删除后可重建、跨 Project/Service 拒绝和 SecretRef 持久化通过。**
- [x] **步骤 5：提交 `feat(store): persist versioned datasource mappings`。**

### Task 3: 地址白名单、DNS 重绑定防护与固定路径 HTTP client

**文件**

- 新增：`internal/security/outbound/policy.go`
- 新增：`internal/security/outbound/policy_test.go`

**接口**

- 产出 `type PolicyConfig struct { AllowedHosts []string; AllowedPorts []int; AllowHTTP, AllowPrivate bool }`。
- 产出 `func NewPolicy(PolicyConfig) (*Policy, error)`。
- 产出 `type Target struct { URL *url.URL; Addresses []net.IPAddr }`。
- 产出 `func (*Policy) Validate(context.Context, string) (Target, error)`。
- 产出 `func NewHTTPClient(*Policy, time.Duration) (*http.Client, error)`；client 使用自定义 `DialContext` 锁定 `Validate` 返回的 IP，并在 `CheckRedirect` 中拒绝所有重定向。
- 产出稳定错误 `ErrHostNotAllowed`、`ErrPortNotAllowed`、`ErrSchemeNotAllowed`、`ErrUnsafeAddress`、`ErrRedirectBlocked`、`ErrResolutionFailed`。

- [x] **步骤 1：写失败测试**

```go
func TestPolicyRejectsLoopbackEvenWhenPrivateAccessIsEnabled(t *testing.T) {
	policy, err := NewPolicy(PolicyConfig{
		AllowedHosts: []string{"localhost"},
		AllowedPorts: []int{80},
		AllowHTTP: true,
		AllowPrivate: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = policy.Validate(context.Background(), "http://localhost")
	if !errors.Is(err, ErrUnsafeAddress) {
		t.Fatalf("err = %v, want ErrUnsafeAddress", err)
	}
}

func TestPolicyRejectsMixedSafeAndUnsafeDNSResults(t *testing.T) {
	policy := testPolicyWithResolver(t, []net.IPAddr{
		{IP: net.ParseIP("93.184.216.34")},
		{IP: net.ParseIP("127.0.0.1")},
	})
	_, err := policy.Validate(context.Background(), "https://metrics.example.com")
	if !errors.Is(err, ErrUnsafeAddress) {
		t.Fatalf("err = %v, want ErrUnsafeAddress", err)
	}
}

func TestClientDoesNotFollowRedirect(t *testing.T) {
	server := httptest.NewTLSServer(http.RedirectHandler("https://blocked.example", http.StatusFound))
	defer server.Close()
	client := testClientForServer(t, server)
	_, err := client.Get(server.URL)
	if !errors.Is(err, ErrRedirectBlocked) {
		t.Fatalf("err = %v, want ErrRedirectBlocked", err)
	}
}
```

- [x] **步骤 2：运行 `go test ./internal/security/outbound -run 'Test(Policy|Client)'`，确认因策略与安全 client 不存在而失败。**
- [x] **步骤 3：实现 URL、主机、端口、IP 分类、全量 DNS 检查和固定 IP dialer；测试使用注入 resolver 与受控 TLS server，不访问公网。**
- [x] **步骤 4：重跑目标测试，确认回环、链路本地、云元数据、混合 DNS、未授权主机/端口/协议和重定向均失败关闭。**
- [x] **步骤 5：提交 `feat(security): guard outbound datasource connections`。**

### Task 4: 只读连接测试器

**文件**

- 新增：`internal/connector/health/tester.go`
- 新增：`internal/connector/health/tester_test.go`

**接口**

- 产出 `type SecretResolver interface { Resolve(context.Context, secrets.Reference) (secrets.Secret, error) }`。
- 产出 `type Tester struct`、`func NewTester(*outbound.Policy, SecretResolver, ...Option) (*Tester, error)`。
- 产出 `func (*Tester) Test(context.Context, datasource.DataSourceMapping) (Result, error)`。
- 产出 `Result`：`Connectivity`、`Permission`、`Version`、`Capabilities`、`CheckedAt`。
- 固定检查路径：GitHub `/repos/{owner}/{repo}`、Grafana `/api/health`、Prometheus `/api/v1/status/buildinfo`、Loki `/loki/api/v1/status/buildinfo`、Runtime `/-/ready`。
- 401/403 返回 `DENIED`；连接成功但响应不可解析返回 `UNKNOWN` 权限和静态能力；超时、DNS、SSRF 和 TLS 使用稳定类别。

- [x] **步骤 1：写失败测试**

```go
func TestTesterReturnsVersionAndPermissionWithoutSecret(t *testing.T) {
	server := newMetricsTestServer(t, http.StatusOK, `{"status":"success","data":{"version":"3.5.0"}}`)
	const canary = "health-canary-84a2"
	tester := newTesterForServer(t, server, canary)
	result, err := tester.Test(context.Background(), prometheusMapping(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	if result.Permission != PermissionGranted || result.Version != "3.5.0" {
		t.Fatalf("result = %#v", result)
	}
	if strings.Contains(fmt.Sprintf("%#v", result), canary) {
		t.Fatal("result exposed secret")
	}
}

func TestTesterReportsKubernetesUnsupported(t *testing.T) {
	result, err := newTestTester(t).Test(context.Background(), kubernetesMapping())
	if err != nil {
		t.Fatal(err)
	}
	if result.Capabilities[0] != "runtime.kubernetes.unsupported" {
		t.Fatalf("capabilities = %#v", result.Capabilities)
	}
}
```

- [x] **步骤 2：运行 `go test ./internal/connector/health -run TestTester`，确认因连接测试器不存在而失败。**
- [x] **步骤 3：实现来源专用固定请求、最小只读 Header、Secret 注入、64 KiB 响应上限、5 秒默认超时和结构化结果。**
- [x] **步骤 4：重跑目标测试，覆盖成功、401/403、超时、错误 JSON、超大响应、Kubernetes 未支持和 Secret 不泄漏。**
- [x] **步骤 5：提交 `feat(connector): add bounded readonly connection tests`。**

**Ruling**：计划中的 `datasource.DataSourceMapping` 实际只存在于持久化包 `store`，连接器直接依赖仓储类型会倒置层次。实现改为由连接器定义只含来源、运行时、公开设置和 Secret 引用的 `health.Target`；API 负责把仓储记录转换为该类型。错误代价是调用方需要显式转换字段。

**Ruling**：管理 Token 与数据库连接串都是运行时已知 Secret。API 进程在创建日志和审计脱敏 Filter 时同时注册两者；数据库连接串不再作为唯一的已知 Secret。该选择覆盖 Bearer Token 在内部错误或调试输出中出现的路径，代价是配置变更后必须重启 API 才能更新脱敏集合。

### Task 5: 管理 Token、CRUD、校验与查询计划 API

**文件**

- 新增：`internal/api/admin_auth.go`
- 新增：`internal/api/admin_auth_test.go`
- 新增：`internal/api/mapping_handler.go`
- 新增：`internal/api/mapping_handler_test.go`
- 修改：`internal/api/handler.go`
- 修改：`internal/api/response.go`
- 修改：`internal/config/config.go`
- 修改：`internal/config/config_test.go`
- 修改：`cmd/relythra-api/main.go`

**接口**

- 产出 `AdminAuthenticator`、`BearerTokenAuthenticator` 和 `ManagementUnavailableAuthenticator`。
- 产出 `NewHandlerWithDependencies(Dependencies, *slog.Logger, ...*redact.Filter) http.Handler`，保留现有 `NewHandler` 兼容入口。
- 新增管理路由：
  - `GET /api/v1/data-source-mappings`
  - `POST /api/v1/data-source-mappings`
  - `GET /api/v1/data-source-mappings/{id}`
  - `PUT /api/v1/data-source-mappings/{id}`
  - `DELETE /api/v1/data-source-mappings/{id}`
  - `POST /api/v1/data-source-mappings/{id}/validate`
  - `POST /api/v1/data-source-mappings/{id}/queries`
  - `POST /api/v1/data-source-mappings/{id}/connection-test`
- 请求体限制 1 MiB、拒绝未知字段和多个 JSON 文档；错误码覆盖 `unauthorized`、`management_unavailable`、`validation_failed`、`unsupported_runtime`、`stale_version`、`rate_limited`。
- 配置新增可选 `RELYTHRA_ADMIN_TOKEN_REF`、`RELYTHRA_DATASOURCE_ALLOWED_HOSTS`、`RELYTHRA_DATASOURCE_ALLOWED_PORTS`、`RELYTHRA_DATASOURCE_ALLOW_HTTP`、`RELYTHRA_DATASOURCE_ALLOW_PRIVATE`。

- [x] **步骤 1：写失败测试**

```go
func TestManagementEndpointRequiresBearerToken(t *testing.T) {
	handler := newMappingAPI(t, staticAuthenticator("admin", "admin-secret"))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/data-source-mappings", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	assertErrorResponse(t, response, http.StatusUnauthorized, "unauthorized")
}

func TestQueryEndpointRejectsRawPromQL(t *testing.T) {
	handler := newMappingAPI(t, staticAuthenticator("admin", "admin-secret"))
	request := authenticatedJSONRequest(
		http.MethodPost,
		"/api/v1/data-source-mappings/"+mappingID+"/queries",
		`{"template_id":"prometheus.service.latency.v1","parameters":{"service":"checkout","promql":"up"}}`,
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	assertErrorResponse(t, response, http.StatusUnprocessableEntity, "validation_failed")
}
```

- [x] **步骤 2：运行 `go test ./internal/api ./internal/config -run 'Test(Management|Query|LoadAPI)'`，确认因路由、认证、DTO 与配置字段不存在而失败。**
- [x] **步骤 3：实现 Token 常量时间校验、严格 JSON、有界分页、字段选择 DTO、CRUD、标签校验、固定查询计划和稳定状态码。**
- [x] **步骤 4：重跑目标测试，确认 Token 缺失/错误、未知字段、无效 UUID、过期版本、任意查询文本、SecretRef 回显和依赖错误脱敏。**
- [x] **步骤 5：提交 `feat(api): manage datasource mappings securely`。**

### Task 6: 连接测试 API、拒绝审计与可观察性

**文件**

- 修改：`internal/api/mapping_handler.go`
- 修改：`internal/api/mapping_handler_test.go`
- 新增：`internal/api/mapping_audit_test.go`

**接口**

- 管理 CRUD、校验、查询计划与连接测试统一调用 `audit.Recorder.Record`。
- 未授权、Token 未配置、SSRF 拒绝、重定向拒绝、连接成功和远端权限拒绝均有审计事件。
- 审计参数只含 mapping ID、source type、result、error code、模板 ID 和耗时；不写 endpoint query、Authorization、SecretRef locator 或响应正文。
- 审计写入失败时 API 返回 `internal_error`，外部连接测试不继续执行。

- [x] **步骤 1：写失败测试**

```go
func TestRejectedConnectionWritesAuditWithoutSecret(t *testing.T) {
	const canary = "audit-mapping-canary-f61a"
	recorder := &capturingRecorder{}
	handler := newMappingAPIWithAudit(t, recorder, canary)
	response := serveAuthenticated(
		handler,
		http.MethodPost,
		"/api/v1/data-source-mappings/"+mappingID+"/connection-test",
	)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	event := recorder.last(t)
	if event.Action != audit.ActionConnectionTested || event.Result != audit.ResultDenied {
		t.Fatalf("event = %#v", event)
	}
	if strings.Contains(fmt.Sprint(event.Parameters), canary) {
		t.Fatal("audit parameters exposed secret")
	}
}
```

- [x] **步骤 2：运行 `go test ./internal/api -run 'Test.*Audit|TestRejectedConnection'`，确认因审计接线不存在而失败。**
- [x] **步骤 3：在认证后、外部请求前记录必要审计；连接结束后记录最终结果；所有参数通过现有 Redaction Filter。**
- [x] **步骤 4：重跑 API 与 audit 测试，确认成功、失败、拒绝、脱敏、请求 ID 和审计失败关闭语义。**
- [x] **步骤 5：提交 `feat(audit): trace datasource management operations`。**

### Task 7: ADR、威胁模型、README、示例配置与冒烟验证

**文件**

- 新增：`docs/adr/0004-datasource-mapping-boundary.md`
- 修改：`docs/security/threat-model.md`
- 修改：`README.md`
- 修改：`.env.example`
- 修改：`scripts/smoke_domain.sql`
- 修改：`scripts/smoke.sh`
- 新增：`scripts/smoke_management.sql`
- 修改：`cmd/relythra-api/main.go`
- 修改：`deploy/compose/compose.yaml`
- 修改：`deploy/docker/entrypoint.sh`

**接口**

- ADR 记录单管理员 Token 边界、SecretRef、固定模板、DNS/IP 锁定、零重定向、端口与私有地址策略、软删除版本和 v1 Kubernetes 边界。
- README 记录全部环境变量、API 示例、连接测试固定路径、错误语义和尚未实现的真实诊断连接器。
- 冒烟验证 `000004` 空库迁移、重复迁移、版本冲突、草稿保存、部分唯一索引和审计追加。

- [x] **步骤 1：修改 SQL 冒烟夹具，加入 `version`、部分唯一索引、SecretRef 与草稿标签断言；先执行并确认当前 schema 在版本列处失败。**
- [x] **步骤 2：新增 ADR 与威胁模型条目，明确共享 Token 不是 RBAC、管理 API 不得公网暴露。**
- [x] **步骤 3：更新 README 和 `.env.example`，只使用占位 SecretRef、示例主机和示例端口。**
- [x] **步骤 4：运行 `make fmt-check vet test build`、`make web-check`、`RELYTHRA_POSTGRES_PASSWORD=ci-only-not-secret make smoke` 和 gitleaks。**
- [x] **步骤 5：提交 `docs(datasource): document secure mapping boundary`。**

## 执行证据

- 真实 PostgreSQL：`RELYTHRA_TEST_DATABASE_URL='postgres://yang@127.0.0.1:5432/relythra?sslmode=disable' go test ./migrations ./internal/store ./internal/security/audit -count=1` 通过，覆盖空库迁移、重复迁移、仓储约束、SecretRef 持久化和审计不可变。
- Go 门禁：`make fmt-check vet test build` 通过。
- 前端门禁：`make web-check` 通过。
- 脚本静态检查：`bash -n scripts/smoke.sh deploy/docker/entrypoint.sh scripts/dev.sh` 与 `git diff --check` 通过。
- Compose 端到端：`RELYTHRA_POSTGRES_PASSWORD=ci-only-not-secret make smoke` 通过，覆盖空卷迁移、重复迁移、管理认证、映射 CRUD、固定模板计划、过期版本、运行时草稿、审计断言、服务健康与数据库故障降级。
- 审计回归：创建映射的 `object_id` 在测试中先复现为 `collection`，修复为真实映射 ID 后，`go test ./internal/api -run TestDatasourceManagementOperationsAreAudited -count=1` 通过。
- Secret 扫描：提交前执行 gitleaks，只接受无泄漏或对固定测试 canary 有明确误报说明的结果。

## 当前未覆盖与限制

- v1 只有单管理员共享 Token，没有 RBAC、SSO、审批流或逐用户身份。
- 查询 API 当前生成并审计有界查询计划，不执行真实 Prometheus/LogQL 业务查询；连接测试只访问来源专用固定路径。
- Kubernetes 映射只保留契约和校验结果，连接测试明确返回未支持；没有 Docker Socket、SSH 或 Kubernetes 写权限。
- CRUD 写与最终审计写不在同一数据库事务内。审计写入失败时 API 返回 `internal_error`，但已提交的配置写入不会自动回滚；生产使用前需评估补偿或 transactional outbox。

## 验收映射

| Issue 验收项 | 实现与证据 |
| --- | --- |
| Project/Service/Environment 到五类数据源的 CRUD 与持久化 | Task 2 的真实 PostgreSQL 测试、Task 5 API 测试 |
| Compose 五标签与禁止模糊匹配 | Task 1 单元测试、Task 5 `validate` API 测试 |
| 管理配置授权 | Task 5 Token 测试、Task 7 ADR 与 README |
| 地址白名单、协议/端口、DNS rebinding、重定向与云元数据防护 | Task 3 表驱动与受控 TLS 测试 |
| 固定查询模板与有界参数 | Task 1 注册表测试、Task 5 原始 PromQL/LogQL 拒绝测试 |
| 连接测试返回连通性、权限、版本和能力且不泄漏 Secret | Task 4 与 Task 6 |
| 所有关键拒绝可审计 | Task 6 与 Task 7 冒烟 |
| 迁移兼容与回滚 | Task 2 空库 Up/重复 Up、Task 7 Down/Up 说明 |

## 回滚

应用可回滚到不暴露管理路由的版本。数据库优先使用前向兼容迁移；`000004` 的 Down 会移除 `version` 和部分唯一索引，只在隔离测试库执行。生产数据回滚必须停止管理 API 写入、备份映射表并由维护者明确授权。

