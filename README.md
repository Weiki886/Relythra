# Relythra

Relythra 是一个面向发布后异常排查的只读诊断系统：关联发布与代码变更、指标、日志和运行状态，组织可追溯的根因假设与处理建议。

当前仓库已完成 Issue #2 的工程骨架、Issue #3 的领域持久化契约和 Issue #5 的安全数据边界：Go API 与 Worker、React 健康状态页、PostgreSQL 领域与不可变审计迁移、只读追溯 API、SecretRef、脱敏、Prompt allowlist、Docker Compose 和 CI。业务连接器、Agent、完整工作台与通知能力尚未实现；本项目不是生产部署方案。

## 工具链

| 组件 | 最低版本 |
| --- | --- |
| Go | 1.26 |
| Node.js | 24 |
| PostgreSQL | 17 |
| Docker Compose | v2（含 BuildKit，仅 Compose 方式需要） |

其他依赖由 `go.mod`、`web/package-lock.json` 和容器镜像固定。

## 本地启动（不使用 Docker）

本地已启动 PostgreSQL 时，可以从仓库根目录一次启动 API、Worker 和前端开发服务器：

```bash
./scripts/dev.sh
```

脚本会检查本机依赖，按需创建 `relythra` 数据库，执行数据库迁移，安装缺失的前端依赖，并等待三个服务通过健康检查。服务地址如下：

- Web：<http://127.0.0.1:5173>
- API readiness：<http://127.0.0.1:8080/health/ready>
- API 只读领域端点：`/api/v1/reports/{id}`、`/api/v1/evidence/{id}`、`/api/v1/deployments/{id}`
- Worker metrics：<http://127.0.0.1:8081/metrics>

按 `Ctrl+C` 会停止脚本启动的全部进程。默认连接 `postgres://$USER@127.0.0.1:5432/relythra?sslmode=disable`。连接凭据也可以从绝对路径文件注入，避免把密码写进仓库或命令行：

```bash
RELYTHRA_DATABASE_URL_REF='file:/run/secrets/relythra-database-url' \
  ./scripts/dev.sh
```

`RELYTHRA_DATABASE_URL_REF` 指向包含完整 PostgreSQL DSN 的文件。直接运行 API 或迁移命令时，也可使用 `RELYTHRA_DATABASE_PASSWORD_REF` 配合 `RELYTHRA_DATABASE_HOST`、`RELYTHRA_DATABASE_PORT`、`RELYTHRA_DATABASE_NAME`、`RELYTHRA_DATABASE_USER` 和 `RELYTHRA_DATABASE_SSL_MODE`。SecretRef 只接受 `env:NAME` 或 `file:/absolute/path`。

只检查环境、准备本地数据库并查看脚本将使用的地址，不启动服务：

```bash
./scripts/dev.sh --check
```

## 使用 Compose 启动

要求 Docker daemon 已启动。先创建一个不进入 Git 的本地密码文件，再复制非敏感路径配置：

```bash
mkdir -p secrets
chmod 700 secrets
read -rsp 'PostgreSQL password: ' DB_PASSWORD
printf '%s' "$DB_PASSWORD" > secrets/postgres_password
unset DB_PASSWORD
chmod 600 secrets/postgres_password

cp .env.example .env
docker compose --file deploy/compose/compose.yaml up --build
```

`.env.example` 只保存 `secrets/postgres_password` 的相对路径；Compose 将它挂载为 PostgreSQL、迁移命令和 API 使用的只读 Secret。Compose 会等待 PostgreSQL 健康、执行迁移，再启动 API、Worker 和 Web。首次启动需要拉取镜像并安装依赖，耗时会随网络变化。

- Web：<http://localhost:3000>
- API liveness：<http://localhost:8080/health/live>
- API readiness：<http://localhost:8080/health/ready>
- API 只读领域端点：`/api/v1/reports/{id}`、`/api/v1/evidence/{id}`、`/api/v1/deployments/{id}`
- Worker metrics：<http://localhost:8081/metrics>

所有演示端口只绑定回环地址，数据库映射到本机 `15432` 端口。停止服务：

```bash
docker compose --file deploy/compose/compose.yaml down
```

下面的命令会同时删除演示数据库卷及其数据：

```bash
docker compose --file deploy/compose/compose.yaml down --volumes
```

## 数据库迁移

迁移通过独立命令执行，API 启动时不会自动修改 schema：

```bash
docker compose --file deploy/compose/compose.yaml run --rm migrate status
docker compose --file deploy/compose/compose.yaml run --rm migrate up
docker compose --file deploy/compose/compose.yaml run --rm migrate down
```

`000001_baseline.sql` 建立迁移基线，`000002_domain_model.sql` 创建 Project、Service、Release、DeploymentEvent、Alert、Incident、Investigation、ToolCall、Evidence、Hypothesis、DiagnosisCase、Report、Notification 及关联审计表。`000003_security_audit.sql` 创建只允许追加的安全审计表和索引，触发器拒绝 UPDATE/DELETE。迁移包含 UUID 主键、UTC 时间、幂等唯一约束、外键、索引、状态机和不可变历史保护。

`down` 会回滚一个版本，执行前应确认影响。领域迁移的 `down` 会删除领域表和其中的数据，只允许用于隔离的本地或测试环境。API 启动不会自动迁移。

三个领域端点只读，并返回统一请求 ID 和错误包络。数据库为空或资源不存在时返回 `404`，路径 ID 非法时在查询数据库前返回 `400`。当前没有创建这些资源的公开写接口。

## 本地检查

安装依赖并运行与 CI 相同的检查：

```bash
make web-install
make fmt-check vet test build web-check
```

也可以分别使用 `make fmt`、`make test`、`make build` 和 `make web-check`。前端开发服务器通过以下命令启动，默认监听 Vite 输出中的地址，并将 `/health` 代理到本机 `8080`：

```bash
npm --prefix web run dev
```

前端开发页需要可访问的 API；直接运行 Go API 时，必须通过 `RELYTHRA_DATABASE_URL`、`RELYTHRA_DATABASE_URL_REF` 或 `RELYTHRA_DATABASE_PASSWORD_REF` 提供 PostgreSQL 连接配置。不要把包含真实凭据的连接串写入仓库。

`make test` 在没有测试数据库时会跳过真实 PostgreSQL 用例。需要验证迁移和事务仓储时，显式提供隔离测试库：

```bash
RELYTHRA_TEST_DATABASE_URL='postgres://user@127.0.0.1:5432/relythra?sslmode=disable' \
  go test ./internal/store ./internal/security/audit ./migrations -count=1
```

## 完整冒烟

冒烟使用独立 Compose 项目与端口 `18080`、`18081`、`13000`、`15432`，会构建镜像、从空卷迁移、重复执行迁移，并检查领域表、索引、幂等约束、非法状态回滚、报告关联、只读 API、Worker 和 Web：

```bash
RELYTHRA_POSTGRES_PASSWORD_FILE=/absolute/path/to/local-password ./scripts/smoke.sh
```

脚本退出时自动删除其容器和数据库卷。CI 兼容入口 `RELYTHRA_POSTGRES_PASSWORD=ci-only-not-secret ./scripts/smoke.sh` 会创建临时只读密码文件并在退出时删除。若已有服务占用上述端口，可通过 `RELYTHRA_API_PORT`、`RELYTHRA_WORKER_PORT`、`RELYTHRA_WEB_PORT` 和 `RELYTHRA_POSTGRES_PORT` 覆盖。

仅校验 Compose 渲染结果时运行：

```bash
RELYTHRA_POSTGRES_PASSWORD_FILE=/absolute/path/to/local-password \
  docker compose --file deploy/compose/compose.yaml config --quiet
```

## 故障排查

- Web 显示 `API is not ready`：检查 API 容器日志与 PostgreSQL 健康状态。readiness 失败时 liveness 仍应成功。
- 迁移失败：查看一次性 `migrate` 容器日志；依赖未满足时 API 不会启动。
- 端口冲突：在 `.env` 中修改对应的 `RELYTHRA_*_PORT`。
- 依赖或镜像下载失败：先恢复网络，再重新执行相同命令；缓存不是构建成功的必要条件。

## 仓库边界

- `cmd/`：API、Worker 和迁移进程入口。
- `internal/`：Go 领域模型、事务仓储、HTTP 契约、配置、健康检查与 Worker 能力。
- `web/`：React/TypeScript 工作台与前端测试。
- `migrations/`：嵌入式、版本化 PostgreSQL 迁移。
- `deploy/`：Compose 和镜像构建配置。
- `test/fixtures/`：后续跨服务测试夹具及其说明。
- `docs/adr/`：架构决策，见 [ADR 0001](docs/adr/0001-repository-baseline.md)、[ADR 0002](docs/adr/0002-domain-persistence.md) 和 [ADR 0003](docs/adr/0003-security-data-boundary.md)。
- `docs/security/`：威胁模型、控制与剩余风险，见 [安全威胁模型](docs/security/threat-model.md)。
- `scripts/smoke.sh`：真实服务端到端冒烟；领域夹具与断言位于 `scripts/smoke_domain.sql`。

## 配置与数据边界

`.env.example` 只包含本地端口和未跟踪密码文件路径，`.env`、`secrets/` 被 Git 忽略。数据库和后续连接器通过 SecretRef 或 Secret 文件注入；不得提交真实密码、Token、私钥或含凭据的连接串。日志、API、审计和模型 Prompt 使用统一脱敏，无法安全处理时拒绝外发。

凭据轮换时先撤销旧值，再替换 Secret 文件或环境变量并重启使用它的进程；仅替换文件但保留旧凭据有效不构成完成处置。安全问题请按 [SECURITY.md](SECURITY.md) 的私密流程报告。

诊断系统在后续实现中保持只读，不自动回滚、重启或扩容。当前健康检查只反映进程与数据库连通性，不代表业务诊断已经可用。尚未实现身份认证、多租户授权、Webhook 签名、SSRF 动态防护、真实连接器、通知、模型调用和公网访问控制；生产数据库角色隔离、保留期和外部审计留存也需要后续设计。
