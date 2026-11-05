# Cross-service fixtures

后续 Issue 将在这里存放连接器、告警和诊断流程共用的跨服务测试夹具。夹具必须脱敏、可重复，并由使用它的测试说明来源与预期行为。

评测真值与故障注入说明必须放在 `test/evaluation/cases/`，不能放进运行时可查询的 `diagnosis_cases` 表、通用夹具、Prompt 或日志。使用 `go run ./cmd/relythra-casecheck test/evaluation/cases` 校验所有版本；同一案例的新版本使用独立文件，不覆盖已发布版本。运行时构造 Prompt 只使用 allowlist 上下文，读取评测案例必须通过带审计的 Evaluator 访问路径。

Issue #2 的集成验证使用真实 PostgreSQL 和 Compose 服务，不依赖预录响应；Go 与 React 单元测试分别靠近其包和源码。
