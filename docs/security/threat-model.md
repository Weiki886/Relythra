# Relythra 安全威胁模型

## 范围与信任边界

本模型覆盖 Issue #5 建立的基线：进程配置、日志、HTTP 响应、审计、Evidence、模型上下文和 Compose 凭据注入。GitHub、Grafana、Prometheus、Loki、容器和宿主机均为外部不可信数据源；浏览器、Webhook 调用方、评测数据和模型输出同样不可信。

| 信任边界 | 进入系统的数据 | 允许的外发 |
| --- | --- | --- |
| 运行时环境与 Secret 文件 | 连接凭据 | 只传给对应连接器或数据库驱动，不进入日志、API、审计、Prompt |
| 外部观测与代码平台 | 指标、日志、Diff、PR、容器状态 | 经限额和脱敏后形成 Evidence；不可信文本只能进入 `untrusted_data` 字段 |
| HTTP/Webhook | 请求头、query、正文、标签 | 校验、去重、稳定错误响应；原始输入不直接回显 |
| Agent 与模型 | allowlisted Prompt DTO、工具结果 | 结构化候选；必须引用 Evidence，不能触发任意查询或外部写操作 |
| PostgreSQL 审计 | 脱敏操作摘要 | 只追加读取；不允许应用更新或删除 |

## 威胁与当前控制

| 威胁 | 影响 | 当前控制 | 剩余风险 |
| --- | --- | --- | --- |
| Secret 出现在日志、异常、HTTP、审计或 Prompt | 凭据泄露、外部账号被滥用 | Secret 安全值对象、统一脱敏、canary 测试、脱敏失败关闭、Prompt 标识字段拒绝 | 尚未接入组织级 Secret Manager；标准库无法识别任意自造凭据格式，连接器必须注册已知 Secret |
| URL userinfo、query token 或 Header 泄露 | 数据库、Webhook、观测平台凭据泄露 | 固定规则覆盖 Authorization/Cookie、userinfo、敏感 query 和常见 Token | 新协议或新 Header 需要更新规则和测试 |
| Prompt Injection | 日志或 Diff 覆盖系统指令，诱导工具滥用 | 版本化 allowlist DTO、未知字段拒绝、`untrusted_data` 包裹、固定工具注册表边界 | 模型仍可能被不可信文本误导；v1 必须由证据校验和确定性规则约束候选 |
| 评测答案或注入元数据进入运行时 | 评测虚高，结论被预置 | Prompt schema 不含答案、注入、评分和场景元数据；输入总大小与集合上限 | 后续评测器仍需验证数据库视图、日志和工具响应没有旁路泄漏 |
| SSRF、DNS rebinding 与任意查询 | 访问内网或高成本查询 | 架构要求服务端地址 allowlist、固定模板、超时、分页和结果上限 | 对应连接器尚未实现，必须在 Issue #4 和连接器 Issue 中补齐动态校验 |
| Webhook 伪造与重放 | 重复调查、资源耗尽或伪造故障 | 架构要求签名/共享密钥、正文上限、指纹和时间窗去重 | 签名和重放防护将在告警接入 Issue 实现；当前骨架没有 Webhook |
| 审计篡改或删除 | 无法重建关键操作 | PostgreSQL append-only 触发器、请求/对象索引、真实数据库拒绝测试 | 超级用户仍可禁用触发器；生产需要角色隔离、备份和外部日志留存 |
| 日志注入与超长数据 | 日志伪造、成本失控、截断误导 | 结构化日志、控制字符所在值脱敏、长度限制、显式 `truncated` | 当前只限制结构化外发边界，不代表所有未来连接器都自动具备超时和分页 |
| 容器或集群写权限 | 诊断系统意外修改生产 | 产品范围只读；禁止 Docker Socket、SSH 和 Kubernetes 写 API | 运行时连接器尚未实现，最终发布前必须验证实际挂载与账号权限 |
| 供应链或依赖漏洞 | 构建或运行环境被控制 | 依赖锁文件、固定 Action SHA、最小 CI 权限和现有测试门禁 | 尚未完成 SBOM、镜像签名、运行时漏洞扫描和完整安全验收 |

## 关键安全不变量

1. 任何已知 Secret 经过日志、API、审计、Evidence 或 Prompt 边界后都不得保持原值。
2. 无法证明输出已安全脱敏时，系统拒绝外发；不得降级为原文或空凭据继续运行。
3. Prompt 只接受明确 allowlist 字段，不可信内容必须带来源、原字节数和截断状态。
4. 根因候选必须引用 Evidence；没有机制证据时只能输出候选或 `inconclusive`。
5. 诊断连接器不得创建外部副作用，也不得暴露任意 URL、PromQL、LogQL、SQL 或 shell 执行入口。
6. 审计记录在应用权限下不可修改或删除。

## 安全测试

- `go test ./internal/security/... ./internal/api ./internal/config` 验证 Secret、脱敏、日志、Prompt、API 和配置边界。
- `RELYTHRA_TEST_DATABASE_URL=... go test ./migrations ./internal/security/audit -count=1` 验证真实迁移、脱敏写入和 UPDATE/DELETE 拒绝。
- `make smoke` 从独立 Compose 项目、空卷和密码文件启动，验证完整运行路径不依赖未跟踪明文配置。
- canary 只允许使用可识别的虚构值，并同时断言响应、日志、审计和 Prompt 中不出现。

## 漏洞报告与凭据处置

不要在公开 Issue、PR、日志或截图中提交 Secret。请通过仓库 Security 页面的私密漏洞报告入口联系维护者；若入口不可用，先私下联系仓库所有者并只提供复现所需的最小信息。

发现凭据泄露时，先撤销或轮换原凭据，再清理日志/制品并评估访问记录。仅删除文本不视为完成处置。替换 Secret 文件后重启使用它的进程；连接器启用后还应执行连接测试并确认旧值已失效。

## 当前限制

当前仍处于 M1 基线阶段。身份认证、多租户授权、生产数据库角色、Webhook 签名、SSRF 运行时校验、真实连接器、通知、模型调用、公网访问控制和完整供应链证明尚未实现。任何剩余风险在对应 Issue 关闭和 M4 安全验收完成前都不能宣称已消除。
