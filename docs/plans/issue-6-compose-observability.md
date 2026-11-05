# Issue #6：Compose 示例业务与观测环境

需求：[Issue #6](https://github.com/Weiki886/relythra/issues/6)，Proposal #1，M1。
采用 maintained/full；本会话顺序执行，不创建 goal 或子代理。

## 设计与范围

复用现有 API/Worker/Web/数据库 Compose，在 `demo.yaml` 增量叠加演示环境，保留无 Docker 开发入口。两个独立构建/发布的 Go 进程：orders → inventory → 独立 demo PostgreSQL。inventory 使用只允许 SELECT 的角色；初始化账号只挂在 demo PostgreSQL。控制面不挂业务数据库凭据。

标准库 HTTP、slog 与现有 pgx 足够，不增加 Go 依赖。共享的示例处理器提供有限路由、超时、固定标签、直方图和 JSON 日志。示例日志写 stdout 与有界轮转文件，Alloy 只读采集专用卷，不需要 Docker Socket。故障测试入口默认关闭，仅在显式启用时允许固定的有界延迟，不开放任意 SQL/命令。

Prometheus/Loki 存储只在后端网络，诊断侧使用 Nginx 固定 GET 路径 + 独立 Bearer Token 网关。Grafana 预配置只读数据源、Viewer、面板与告警；部署发送密钥/通知地址只提供独立占位，不实现未来的回调/通知。node_exporter 只读挂载 Linux proc/sys，cAdvisor 使用原始 cgroup，不提权、不挂 socket；无法从 Docker 元数据获取的名称/镜像标签明确为未知，不能据此做容器关联。Docker Desktop 指标代表 Linux VM。

## 全局约束

- 所有长期服务有 healthcheck、CPU/内存/PID 限额和日志轮转，端口只绑定回环。
- 不执行系统恢复动作，不挂载 Docker Socket，不提交 Secret，不自动清理用户卷。
- 初次 schema/种子初始化只发生在 demo 空卷；旧卷不匹配时 readiness 失败并提示，不静默清库。
- 不改变已应用 goose 迁移；基线 API 数据库/迁移不受演示初始化影响。
- cAdvisor 的原始 cgroup 不具备 Docker 元数据；容器与服务关联须等待有证据的映射，不猜测。

## Task 1：可测试的示例调用链

文件：`internal/demo/service.go`、`service_test.go`、`log.go`、`log_test.go`、`cmd/demo-orders/main.go`、`cmd/demo-inventory/main.go`、`internal/demo/run.go`。
接口：`NewHandler(Config, StockStore, *http.Client, *slog.Logger) (http.Handler, error)`；`StockStore.Stock(context.Context) (int, error)`；Config 固定 service/project/environment/release/host/container 标签与 inventory URL。

先写真实 HTTP 测试：启动 fake inventory，GET `/orders` 断言固定 `/stock` 调用、JSON 与指标；依赖失败返回 503、readiness 503 而 liveness 200；非法路由不增加指标标签基数；JSON 日志具备必需标签；默认故障端点 404。`go test ./internal/demo` 确认缺实现失败后实现。日志轮转测试写入超过限制，断言文件总量有界。

```go
response := httptest.NewRecorder()
handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/orders", nil))
if response.Code != http.StatusOK { t.Fatalf("status = %d", response.Code) }
```

提交：`feat(demo): add observable orders and inventory services`。

## Task 2：有界、安全的观测拓扑

文件：`deploy/compose/demo.yaml`、`deploy/demo/**`、`deploy/docker/observe.Dockerfile`、`cmd/relythra-probe/main.go`、`scripts/demo.sh`、`.env.example`、`Makefile`。
消费 Task 1 的 `/orders`、`/stock`、`/metrics`、`/health/live`、`/health/ready`。
产出：`./scripts/demo.sh up|status|down`；`up` config 校验后 `--wait`，失败列出服务；`down` 保留卷。

配置先用 `docker compose -f deploy/compose/compose.yaml -f deploy/compose/demo.yaml config --quiet` 验证，然后从专用空卷执行 `up --build --wait`。标准库探针仅接受固定启动参数 URL、3 秒超时、禁止重定向，便于无 shell 镜像健康检查。网关仅 GET 查询白名单，所有写路径 403；Token 来自 Secret 文件，临时渲染在容器 tmpfs，禁止打印。Grafana provisioning 从环境读取独立只读 Token。

```bash
docker compose -f deploy/compose/compose.yaml -f deploy/compose/demo.yaml up --build --wait --wait-timeout 240
```

提交：`feat(infra): provision isolated demo observability stack`。

## Task 3：真实数据与失败边界验收

文件：`scripts/demo-smoke.sh`、`.github/workflows/ci.yml`、`docs/demo.md`、`docs/adr/0005-demo-observability.md`、`README.md`、`AGENTS.md`。
在专用项目/端口/随机 Secret/独立卷运行，清理只作用于本轮专用资源；与现有 smoke 串行执行。

- orders → inventory → 种子 SELECT 成功，demo_reader 写表/跨库连接拒绝。
- 所有 Prometheus targets UP；查询业务延迟、Worker、node 内存、cgroup 内存有实际样本。
- Loki 查到 JSON 请求日志，project/service/env/host/container 标签齐全。
- Grafana datasource/panel/alert provisioning 可读；无认证访问网关 401，Bearer 读取成功，写操作拒绝。
- 停止 demo DB：inventory/orders readiness 503，liveness 与控制面 readiness 200；恢复后健康。重启保留种子、面板和观测卷。
- 原有 `make smoke`、`make fmt-check vet test build web-check`、shell 语法检查、`git diff --check`。

提交：`test(infra): verify demo telemetry and dependency isolation`。

## 回滚

停止 demo 叠加环境并保留卷，退回原基础 Compose/无 Docker 启动。无 goose 迁移变化。仅人工确认后删除演示卷，不能通过脚本重置控制面数据。

## 执行台账

- 计划自检：Issue 的调用链、权限、采集、面板/告警、资源、健康与恢复均有对应 Task。部署/通知配置仅占位，不冒充已实现能力。
- Task 1 complete：`go test ./internal/demo` 首次因缺少 Config/NewHandler/RotatingLog 失败；实现后 `go test -race ./internal/demo` 通过。
- Task 1 补充边界：缺失库存字段 `{}` 曾被当作零库存，回归测试先得到 200 而失败；现用指针字段+有界完整 JSON 校验，返回 503，race 测试通过。
- Task 2 in progress：探针测试先因缺少 probe 失败，实现后通过；完整栈首次构建进行中。
- Ruling：增加 Alloy scrape target，使缺失日志标签的 drop counter 可在 Prometheus 中查询；验收 target 总数由 5 改为 6。仍不增加 Docker 元数据权限。
- 快速验证：`make fmt-check vet test build`、`make web-check`、现有 `make smoke` 已通过；完整观测栈尚未完成验收。
- Task 2 首次真实运行发现：64 位十六进制 Bearer 超过 Nginx 默认 map hash bucket；设置 bucket 128，保持完整 Token 比较。
- Ruling：PostgreSQL 初始化脚本在 Docker Desktop bind 模式下执行失败，且 UID postgres 不能依赖读取 root-only Secret。改为专用派生镜像 COPY 可执行初始化脚本；root bootstrap 将 reader Secret 复制为 postgres 私有文件，再走官方初始化。只改变专用业务数据库，无共享数据删除。
- Task 2 第二次运行：所有容器 healthy，宿主机却无法访问 orders；Docker 不转发仅连接 internal 网络容器的发布端口。新增只供 orders 的 demo-ingress 网络，业务数据库仍只在 business 内网；静态验收新增“发布端口必须具备非 internal 网络”检查。不能以容器 healthy 代替真实宿主机请求验收。
- Task 2/3 complete（权限复核前）：完整 smoke 两次通过，包含实例身份与日志一致、UID 10001/472、6 targets、Grafana 告警/Viewer、数据库中断、schema 缺失和重启恢复。
- Ruling（安全复核）：只读宿主机根目录可能间接暴露 Unix Socket，不满足无控制入口的意图。新增配置回归检查先在 cadvisor 失败；移除 node_exporter/cAdvisor 的 `/` 挂载，node 仅保留 CPU/meminfo/loadavg/time，磁盘容量明确未知。重新验收剩余 CPU/内存/cgroup 数据，不能以缺失代替健康。
