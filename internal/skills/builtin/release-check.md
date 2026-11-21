---
name: release-check
description: 发布前的版本、构建、签名、安装器与产物检查清单
when: 用户要求发版、打包、生成 Release，或问“发布前还要检查什么”
category: 发布
tags: release, build, installer, sign
version: 1
---

# 发布检查

1. **确认版本单一来源**：`internal/core/config.go` 的 `Version` 是唯一事实；README、CHANGELOG、`version.txt`、安装器版本必须一致。
2. **更新 CHANGELOG**：写清用户可见变化、兼容性影响、迁移步骤；不要把内部重构流水账当发布说明。
3. **跑测试**：`go test ./...`；在 Windows 上跑全量，Linux 至少跑可跨平台包。失败先修，不要带红发布。
4. **离线自检**：`WIN7Harness-amd64.exe -selftest --offline`，确认 SKIP 项只是模型未配置等预期原因。
5. **双架构构建**：`build.sh` 或 `build\build-release.ps1`；确认 amd64/386 都能生成、PE 结构与图标校验通过。
6. **安装器回归**：标准安装/卸载/便携/覆盖升级/数据保留；便携模式确认 API Key 加密与 U 盘路径。
7. **签名与更新**：正式发布必须设置 `UPDATE_PUBKEY` / `REQUIRE_SIGNED=1`；确认 `latest.json`、SHA256、Ed25519 签名可验证。
8. **检查产物内容**：exe、LICENSE、NOTICE、COMMERCIAL、THIRD_PARTY_NOTICES、README.txt、setup.bat、version.txt；源码 zip 与二进制 zip 分开。
9. **输出**：给出产物路径、大小、校验值、已验证项和未验证项；不要只说“构建成功”。

如果缺少 Win7 真机或签名环境：明确标为“未验证/不可发布”，不要用开发机构建冒充正式产物。
