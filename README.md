# Relythra

Relythra 是一个面向发布后异常排查的只读诊断系统：关联发布与代码变更、指标、日志和运行状态，组织可追溯的根因假设与处理建议。

当前仓库仅包含 Issue #2 的工程骨架：Go API 与 Worker、React 健康状态页、PostgreSQL 迁移、Docker Compose 和 CI。业务连接器、Agent、完整工作台与通知能力尚未实现；本项目不是生产部署方案。

## 工具链

| 组件 | 最低版本 |
| --- | --- |
| Go | 1.26 |
| Node.js | 24 |
| PostgreSQL | 17 |
| Docker Compose | v2（含 BuildKit） |

其他依赖由 `go.mod`、`web/package-lock.json` 和容器镜像固定。

## 使用 Compose 启动

要求 Docker daemon 已启动。复制非敏感配置，并将占位数据库密码换成本地密码：

```bash
cp .env.example .env
# 编辑 .env 中的 RELYTHRA_POSTGRES_PASSWORD
docker compose --file deploy/compose/compose.yaml up --build
```

Compose 会等待 PostgreSQL 健康、执行迁移，再启动 API、Worker 和 Web。首次启动需要拉取镜像并安装依赖，耗时会随网络变化。

- Web：<http://localhost:3000>
- API liveness：<http://localhost:8080/health/live>
- API readiness：<http://localhost:8080/health/ready>
- Worker metrics：<http://localhost:8081/metrics>

数据库只映射到本机 `15432` 端口用于演示。停止服务：

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

`down` 会回滚一个版本，执行前应确认影响。当前初始迁移只记录基线版本，没有领域表。

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

前端开发页需要可访问的 API；直接运行 Go API 时，必须通过 `RELYTHRA_DATABASE_URL` 提供 PostgreSQL 连接串。不要把包含真实凭据的连接串写入仓库。

## 完整冒烟

冒烟使用独立 Compose 项目与端口 `18080`、`18081`、`13000`、`15432`，会构建镜像、从空卷迁移、重复执行迁移，并检查 API、Worker 和 Web：

```bash
RELYTHRA_POSTGRES_PASSWORD=ci-only-not-secret ./scripts/smoke.sh
```

脚本退出时自动删除其容器和数据库卷。若已有服务占用上述端口，可通过 `RELYTHRA_API_PORT`、`RELYTHRA_WORKER_PORT`、`RELYTHRA_WEB_PORT` 和 `RELYTHRA_POSTGRES_PORT` 覆盖。

仅校验 Compose 渲染结果时运行：

```bash
docker compose --file deploy/compose/compose.yaml config --quiet
```

## 故障排查

- Web 显示 `API is not ready`：检查 API 容器日志与 PostgreSQL 健康状态。readiness 失败时 liveness 仍应成功。
- 迁移失败：查看一次性 `migrate` 容器日志；依赖未满足时 API 不会启动。
- 端口冲突：在 `.env` 中修改对应的 `RELYTHRA_*_PORT`。
- 依赖或镜像下载失败：先恢复网络，再重新执行相同命令；缓存不是构建成功的必要条件。

## 仓库边界

- `cmd/`：API、Worker 和迁移进程入口。
- `internal/`：Go 共享配置、健康检查与 Worker 能力。
- `web/`：React/TypeScript 工作台与前端测试。
- `migrations/`：嵌入式、版本化 PostgreSQL 迁移。
- `deploy/`：Compose 和镜像构建配置。
- `test/fixtures/`：后续跨服务测试夹具及其说明。
- `docs/adr/`：架构决策，见 [ADR 0001](docs/adr/0001-repository-baseline.md)。
- `scripts/smoke.sh`：真实服务端到端冒烟。

## 配置与数据边界

`.env.example` 只包含本地占位值，`.env` 被 Git 和 Docker 构建上下文忽略。数据库密码通过环境变量注入；不得提交真实密码、Token、私钥或含凭据的连接串。Go 与 Web 的 Docker 构建上下文也会排除依赖产物和本地环境文件。

诊断系统在后续实现中保持只读，不自动回滚、重启或扩容。当前健康检查只反映进程与数据库连通性，不代表业务诊断已经可用。
