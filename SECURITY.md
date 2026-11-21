# 安全策略 / Security Policy

## 报告问题

请不要在公开 Issue 里贴 API Key、会话正文、代理凭据或完整配置。

请通过 GitHub Security Advisories 私下报告：

https://github.com/MC5lan/WIN7Harness/security/advisories/new

如果该入口不可用，再开一个不包含敏感细节的 Issue，说明“需要私下联系”。

## 本项目会处理的数据

- 模型 API Key（标准模式 DPAPI，便携模式 U 盘密码 + AES-256-GCM）
- 会话、附件、快照、日志、代码索引
- 可选的远程指挥台配对 token、MCP 凭据

## 建议

- 不要在公共仓库提交 `config.json`、`data/`、`sessions/`、`keys/`、更新私钥。
- 分享诊断包前先打开 `config.redacted.json` 检查；支持包会清空 Key/密文，但不保证
  会话正文和路径完全脱敏。
- U 盘丢失风险请用 BitLocker To Go / VeraCrypt 保护整个盘；U 盘密码只保护 API Key。
- 在不可信项目里运行工具前，先看确认弹窗和命令参数；高危操作默认需要确认。
