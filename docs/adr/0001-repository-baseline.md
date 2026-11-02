# ADR 0001：仓库骨架与本地运行基线

- 状态：Accepted
- 日期：2026-11-02
- 关联：Issue #2、Proposal #1

## 背景

后续连接器、诊断 Agent 和 Web 工作台需要共享一套可构建、可测试的工程边界。本 ADR 只固定仓库骨架，不提前决定领域表、连接器协议或 Agent 编排方案。

## 决策

采用单仓库。Go API、Worker 和迁移命令分别位于 `cmd/`，共享代码位于 `internal/`；React 工作台位于 `web/`；嵌入式 SQL 迁移位于 `migrations/`；Compose 与镜像构建文件位于 `deploy/`；跨服务夹具位于 `test/fixtures/`。包内单元测试靠近实现，端到端冒烟入口为 `scripts/smoke.sh`。

工具链基线为 Go 1.26、Node.js 24、PostgreSQL 17 和 Docker Compose v2。Go 版本由当前 `goose` 与 `pgx` 依赖共同约束，本地、容器和 CI 必须一致。前端使用 Vite、TypeScript、Vitest 和 ESLint；生产静态文件由 nginx 提供，并由 nginx 代理 `/health/` 到 API。

API 的 liveness 不访问数据库，readiness 使用带超时的 PostgreSQL ping。数据库错误写入服务端日志，HTTP 响应只返回稳定的 `dependency unavailable`，避免泄露连接串或内部地址。Worker 独立暴露 Prometheus 文本格式的心跳指标。API 与 Worker 不共享进程生命周期，并支持信号触发的优雅关闭。

迁移使用 `pressly/goose` 和 Go 嵌入文件。Compose 按 PostgreSQL 健康、一次性迁移成功、API 健康的顺序启动；API 进程自身不自动迁移。初始迁移只保留 schema 版本，领域表由对应领域 Issue 引入。重复执行 `migrate up` 必须是成功的空操作。

前端 `package.json` 暂时将传递依赖 `minimatch` 固定到 `11.0.1`，以消除当前安装树中的已知漏洞。上游依赖吸收兼容补丁后，运行 `npm audit` 并移除此 override，不把它作为永久版本策略。

GitHub Actions 复用 Makefile 命令，分别执行后端、前端和真实 Compose 冒烟。第三方 Action 固定完整提交 SHA，工作流 token 仅授予 `contents: read`。依赖缓存只用于提速，不作为正确性的前提。

## 后果

空数据库可以重复迁移并启动完整演示骨架；数据库故障会阻止 API readiness，但不会让 liveness 失败。当前没有业务 API、身份认证、连接器或 Agent，不能将该环境视为生产部署。

新增领域能力时应复用这些边界。需要变更迁移所有权、进程拓扑或工具链最低版本时，必须新增 ADR，并同步 Makefile、容器、CI 与 README。
