# Cross-service fixtures

后续 Issue 将在这里存放连接器、告警和诊断流程共用的跨服务测试夹具。夹具必须脱敏、可重复，并由使用它的测试说明来源与预期行为。

Issue #2 的集成验证使用真实 PostgreSQL 和 Compose 服务，不依赖预录响应；Go 与 React 单元测试分别靠近其包和源码。
