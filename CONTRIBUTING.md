# 贡献指南

感谢参与 WIN7 Harness。这个项目的存在理由是 **在 Windows 7 SP1 上运行**，
所以下面几条是红线，不是风格偏好。

## 红线

1. **Go 版本锁死 1.20.14**。Go 1.21+ 的产物无法在 Win7 SP1 启动，`go.mod` 的
   `go 1.20` 不得改动。
2. **必须 `CGO_ENABLED=0`**。引入任何需要 CGO 的依赖即失败。
3. **双架构 386 + amd64 必须同时可编译**。任何改动后两条链路都要验证。
4. **单文件 exe、运行时零依赖**。不得引入需要外部 DLL / 运行时安装的组件。
5. **只使用 Win7 SP1 存在的 Win32 API**。`internal/win32/` 下的 syscall 声明、
   `LazyDLL` 加载名、结构体布局一律不改。新增 API 前先看
   [docs/win32-api-notes.md](docs/win32-api-notes.md)。
6. **不新增第三方依赖**。确有必要时先跑 `go run ./cmd/depguard`，并在 PR 中单独说明理由。
7. **不改 `.gitattributes` 的行尾规则**：`*.bat/*.cmd/*.ps1/*.txt` = CRLF，
   `*.sh/*.go/*.md` = LF。
8. **不改更新签名的 payload 结构**。`core.canonicalManifestPayload` 的字段集合与顺序
   决定 Ed25519 兼容性，改一个字段所有旧清单全部失效。

## 本地构建

准备 Go 1.20.14、`rsrc`、Python 3：

```bash
GO=/path/to/go1.20.14/bin/go ./build.sh
```

Windows 上一键构建（含安装包）：

```powershell
powershell -ExecutionPolicy Bypass -File build\build-release.ps1 -Test
```

## 本地验证

提交 PR 前至少跑完：

```bash
# 依赖准入
go run ./cmd/depguard

# 双架构编译
GOOS=windows CGO_ENABLED=0 GOARCH=amd64 go build ./...
GOOS=windows CGO_ENABLED=0 GOARCH=386 go build ./...

# 静态检查
GOOS=windows go vet -unsafeptr=false ./...
./scripts/vet.sh

# 单测
go test ./... -count=1
```

Windows 侧再跑：

```powershell
powershell -ExecutionPolicy Bypass -File scripts\installer_regression.ps1
powershell -ExecutionPolicy Bypass -File scripts\ui_regression.ps1
```

## 提交规范

一个提交只做一件事，标题使用：

- `feat:` 新功能
- `fix:` 修复
- `refactor:` 重构（必须声明无行为变更）
- `docs:` 文档
- `test:` 测试
- `build:` 构建脚本
- `ci:` CI / Release
- `chore:` 杂项

重构类提交请附 UI 冒烟截图；行为变更请附前后对比。

## PR 要求

- 一个 PR 只做一件事；重构 PR 必须写明“无行为变更”。
- 红线自查清单逐项勾选。
- 说明测试方式与真实命令输出，不要只写“已修复”。
- 涉及 UI 的改动附截图。
- 不要在 PR、Issue、日志里贴 API Key、会话正文或完整配置。

## 代码风格

- `gofmt` 强制，`gofmt -l .` 必须为空。
- 注释说明“为什么”，不要复述“是什么”。
- 保留审计编号（如 `TL-P0-4`）与设计理由。
- 新增 Win32 调用一律走 `pcall` / `win32.SafeCall`，不要直写 `.Call(`。

## 许可证

提交即表示你同意以 [Apache License 2.0](LICENSE) 授权你的贡献。
