## 变更说明

<!-- 一句话说明这个 PR 解决什么问题 -->

## 变更类型

- [ ] `feat` 新功能
- [ ] `fix` 修复
- [ ] `refactor` 重构（无行为变更）
- [ ] `docs` 文档
- [ ] `test` 测试
- [ ] `build` 构建脚本
- [ ] `ci` CI / Release
- [ ] `chore` 杂项

## 红线自查

- [ ] Go 1.20.14，`go.mod` 的 `go 1.20` 未改
- [ ] `CGO_ENABLED=0`
- [ ] amd64 与 386 都能编译
- [ ] 单文件 exe、无新增运行时依赖
- [ ] 只使用 Win7 SP1 存在的 Win32 API
- [ ] 未新增第三方依赖（或已通过 `go run ./cmd/depguard` 并说明理由）
- [ ] 未改 `.gitattributes` 行尾规则
- [ ] 未改更新签名 payload 结构

## 测试方式

```text
# 贴真实命令与输出
```

## UI 变更

<!-- 有 UI 改动请附截图；无则写“无” -->

## 关联 Issue

<!-- Closes #123 -->
