# Security Policy

## Supported versions

Relythra 尚未发布生产版本。`main` 分支和最新 `v0.x` 里程碑产物接收安全修复；旧版本不承诺持续安全更新。

## Reporting a vulnerability

请使用 GitHub 仓库的 [private security advisory](https://github.com/Weiki886/relythra/security/advisories/new) 报告漏洞。不要在公开 Issue、Pull Request、讨论区或截图中披露凭据、利用细节或真实客户数据。

报告中请包含受影响版本或 Commit、影响、最小复现步骤和建议的修复方向。维护者确认后会在私密渠道协调修复、轮换受影响凭据和披露时间。

## Credential exposure

发现 Secret 已提交或进入日志、构建产物、通知或模型上下文时，先撤销或轮换凭据，再删除暴露内容并检查访问记录。仅清理文本不能使已泄露凭据恢复安全。

本项目只读诊断的边界不意味着连接器自动安全。部署者必须为每个外部系统使用最小权限凭据，并验证实际账号、网络和容器挂载权限。
