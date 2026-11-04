# ADR 0003：密钥、外部数据与模型上下文边界

- 状态：Accepted
- 日期：2026-11-04
- 关联：Issue #5、Proposal #1、ADR 0001、ADR 0002

## 背景

Relythra 后续会同时读取 GitHub、Grafana、Prometheus、Loki、Docker 和宿主机数据，并把日志、Diff、指标摘要与工具结果交给模型。外部数据既可能包含凭据，也可能是恶意 Prompt；评测答案和故障注入元数据还会污染诊断结果。仅在 UI 隐藏字段或降低日志级别无法形成安全边界。

本 ADR 固定运行时密钥注入、外发数据脱敏、Prompt 输入、审计记录和只读连接器的共同约束。

## 决策

### SecretRef 与运行时值

- 新配置只保存引用，不保存明文。SecretRef 仅接受 `env:NAME` 或绝对路径 `file:/path/to/secret`；未知 scheme、相对文件路径、空值、读取失败和超过 64 KiB 的输入必须在启动阶段失败。
- `file:` 只移除一个结尾换行，不缓存值。连接器通过显式 `Reveal()` 取得当前进程所需的短期值，其他格式化、JSON 和 `slog` 表示固定为 `[REDACTED]`。
- 数据库配置支持完整 DSN 的 SecretRef，也支持由主机、端口、库名、用户和密码 SecretRef 组合 DSN。Compose 使用挂载的 PostgreSQL 密码文件，`.env` 只保存未跟踪文件的路径。
- 现有 `RELYTHRA_DATABASE_URL` 直接注入保留为本地兼容入口；新连接器不得新增明文配置入口。缺少必需 SecretRef 时连接器启动失败，不能回退到空值、匿名访问或默认凭据。

### 统一脱敏与失败关闭

- 日志、HTTP 响应、审计参数、Evidence 摘要和模型 Prompt 使用同一脱敏语义：识别 Authorization/Cookie、URL userinfo 与敏感 query、常见 GitHub/Slack/AWS/OpenAI Token、JWT、私钥和调用方注册的已知 Secret。
- 字段名保留以便诊断，值替换为固定标记。脱敏后再次扫描已知 Secret；仍无法安全处理时返回 `ErrUnsafeOutput`，调用方拒绝外发，不回退到原文。
- 日志中的 `error` 先转换为文本再脱敏，避免 JSON 编码把错误变成无诊断信息的空对象。不可信文本裁剪到字段上限，并记录原始字节数和 `truncated` 标记。
- API 成功响应在写状态码前完成序列化、脱敏和编码。任一阶段失败时返回稳定 `internal_error`，不写出半截或未脱敏 JSON。

### Prompt 白名单

- 模型输入使用版本化 `v1` DTO，启用未知字段拒绝、单 JSON 文档检查和 1 MiB 总大小限制。
- 允许字段仅包含调查标识、服务与环境、告警摘要、证据、代码变更和工具结果。日志、Diff、告警摘要与工具摘要都包裹为 `trust=untrusted_data`。
- 服务、来源类型、仓库、Commit、工具名和状态等标识字段必须保持“经脱敏后与原值相同”；一旦包含已知 Secret 或命中凭据规则，整个上下文构造失败。
- `expected_root_cause`、`injection_method`、`scenario_metadata`、评分规则和任意未知调试字段不在 DTO 中，无法进入运行时 Prompt。
- 模型输出只是候选。最终结论必须引用 Evidence ID；缺少关键证据或存在未解释反证时输出 `inconclusive`。

### 不可变审计

- `audit_events` 记录 actor、action、对象类型与 ID、结果、request ID、脱敏参数摘要和发生时间。标识字段在验证和写库前同样经过脱敏。
- 审计表只允许追加，PostgreSQL 行级触发器拒绝 UPDATE 和 DELETE，语句级触发器拒绝 TRUNCATE。索引支持按对象、请求和 actor/action 重建关键操作。
- 数据库约束是第二道防线；应用角色不应拥有绕过触发器所需的超级用户能力。生产部署仍需单独的数据库角色、备份和保留期策略。

### 连接器只读边界

- GitHub、Grafana、Prometheus、Loki、容器和宿主机连接器只使用查询权限。不得挂载 Docker Socket，不得提供 SSH、Kubernetes 写 API、部署回滚、重启或扩缩容工具。
- 数据源地址、固定查询模板、超时、分页和结果上限由服务端配置。用户和模型只能提供已注册工具允许的服务、时间窗和分页参数。
- 远端响应、告警标签、日志正文、Commit/PR 内容和 Diff 都按不可信输入处理。

## 后果

安全边界在进程入口、共享过滤器、Prompt DTO、数据库约束和 Compose 配置中分别落实，并有单元测试与真实 PostgreSQL 测试。代价是连接器必须显式接收 Secret 和 Filter，新增字段时必须同步 Prompt allowlist 与长度限制。

当前没有组织级密钥管理、多租户权限、生产数据库角色隔离、公网访问控制或通知签名实现。这些能力不得由本 ADR 推导为已经存在；启用公网部署前必须另行威胁建模和评审。

## 回滚

代码可回滚到不含安全包的版本，但不得恢复记录原始 Secret 的旧行为。若审计 schema 必须回滚，先停止依赖审计写入的进程，再在隔离环境执行 `000003_security_audit.sql` 的 `down`；该操作会删除审计数据。生产回滚优先使用保持兼容的前向迁移。
