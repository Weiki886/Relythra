# Issue #7 DiagnosisCase 契约实现计划

**目标**：定义版本化、可重复验证的评测案例格式，保存慢 SQL 代表性案例，并防止预期答案、注入状态和评分规则进入运行时输入。

**范围依据**：Issue #7 与 Proposal #1 第 7.5、9、11 节；Prompt 白名单依据 ADR 0003。仅实现 M1 契约和一个静态案例，不实现故障运行器、评分执行器或完整评测集。

## 设计选择

- 案例使用严格 JSON 和 Go 标准库 `encoding/json`；Go 类型与显式校验提供字段路径错误，不新增 JSON Schema 依赖。
- 评测案例存放在 `test/evaluation/cases/`，不放入通用 `test/fixtures/`，不由生产 Go 包嵌入，也不复制进 API/Worker 镜像。每个 `case_id` 的新版本使用独立文件；集合校验拒绝重复版本。
- 运行时上下文使用独立 allowlist DTO，只含服务、环境和告警时间。评测文件读取 helper 对 Runtime 默认拒绝，拒绝时要求审计 sink；此进程内 principal 参数仅是边界测试，不是身份认证。实际隔离由生产代码不调用 helper、案例不嵌入且镜像不复制目录保证。
- 当前运行库 `diagnosis_cases` 含答案字段但没有使用方。新增迁移移除评测专用列；若旧环境有行，迁移拒绝运行且不丢数据。回滚恢复列定义。

## Task 1：严格案例契约与测试

**文件**：新增 `internal/diagnosiscase/case.go`、`access.go` 及测试。

定义案例身份/版本、场景类型、服务/环境、基线/故障版本、故障时间窗、重复次数/随机化、根因分类和允许变体、Evidence 谓词、反证与禁止结论、健康基线、注入/恢复步骤、预期遥测/变更、清理要求、评分规则版本与指标。拒绝缺字段、未知字段、尾随 JSON、非法枚举、倒置时间窗、空候选/谓词、重复版本和缺少恢复/清理信息。测试先于实现；运行时 DTO 测试注入答案 canary 并验证其不出现在序列化结果。

## Task 2：评测文件与访问边界

**文件**：新增 `test/evaluation/cases/slow-sql/v1.json`、`cmd/relythra-casecheck`；更新 `test/fixtures/README.md` 和 Makefile/CI。

慢 SQL 案例只使用虚构服务、版本和证据。校验命令遍历 JSON 文件并检查集合版本唯一性。读取测试覆盖 evaluator 读取、runtime 拒绝、审计 sink 缺失及路径穿越。生产 Prompt 仍只接受现有 ADR 0003 allowlist DTO；不把评测 JSON 解码成 Prompt 输入。

## Task 3：运行数据库收敛与文档

**文件**：新增迁移 `000005_diagnosis_case_evaluation_separation.sql`；扩展 `migrations/domain_model_test.go`；新增 `docs/adr/0006-evaluation-case-isolation.md` 和本计划。

真实迁移测试断言敏感列被移除、回滚恢复列、旧行使前滚失败且数据未丢失。明确公开仓库里的案例真值不属于保密数据；防护目标是避免运行时输入读取答案，不是阻止用户查看公开文件。

## Ruling

- 不修改历史迁移；用新迁移收敛运行 schema。对任何旧 `diagnosis_cases` 行失败关闭，要求人工导出/确认，不静默删除评测数据。
- 当前没有 Agent 读取 DiagnosisCase 的生产代码。隔离验证因此覆盖严格案例读入边界、Prompt 正向 DTO 和实际运行数据库 schema，不虚构不存在的 Agent API。
- 不实现故障注入器、评分器、评测运行器或所有故障案例。

## 验证

- `go test ./internal/diagnosiscase -run 'Test(Decode|CaseSet|Runtime|Evaluation)'`：记录 TDD 红绿结果。
- `RELYTHRA_TEST_DATABASE_URL=... go test ./migrations -run TestDomainSchemaMigratesAndRollsBack -count=1`：验证真实迁移、旧数据保护和回滚；CI backend 使用仅本次 job 的 PostgreSQL 服务执行该测试。
- `go run ./cmd/relythra-casecheck test/evaluation/cases` 与 CI `make case-check`。
- `make fmt-check vet test build web-check`、`git diff --check`。
