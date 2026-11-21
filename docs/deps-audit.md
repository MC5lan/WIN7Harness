# 依赖审计（v1.39.4）

审计时间：2026-11-22
工具：`go run ./cmd/depguard -json`、`go list -m all`、`go mod graph`
结论：**6 个直接依赖全部为纯 Go、无 CGO、Go 1.20 可构建；无需裁剪。**

## 总览

| 指标 | 数值 |
| --- | --- |
| `go list -m all` 模块数 | 77（含主模块与测试/工具链传递依赖） |
| `go.mod` 直接依赖 | 6 |
| `go.mod` 间接依赖 | 20 |
| depguard 实际审查模块 | 30 |
| `go.sum` 模块条目 | 30 |
| `go mod graph` 边数 | 120 |
| 最长依赖链 | 6 层 |

depguard 许可证分布：

```text
Apache-2.0×2, Go-BSD×15, ISC×1, LGPL×1, MIT×9,
Public domain×1, dual(BSD/GPL)×1
```

> LGPL 为 1 个间接依赖（depguard 记为警告级，不是阻断项）。它不直接出现在本项目的
> import 列表中；发布二进制前建议做一次法务复核，确认静态链接义务。dual(BSD/GPL)
> 为 FreeType 的双许可证，可按 BSD 分支使用。

## 直接依赖

| 模块 | 版本 | 许可证 | 最近更新 | 用途 | Win7 影响 |
| --- | --- | --- | --- | --- | --- |
| `go.etcd.io/bbolt` | v1.3.8 | MIT | 2023-10-25 | 会话/索引/任务队列的嵌入式 KV | 纯 Go，无 CGO，Win7 可用 |
| `github.com/alexbrainman/sspi` | v0.0.0-20250919150558 | BSD-3-Clause | 2025-09-19 | NTLM/Negotiate 企业代理转发桥 | 只调用 Win7 已有的 SSPI，无新 DLL |
| `golang.org/x/net` | v0.17.0 | BSD-3-Clause | 2023-10-10 | HTTP/2、代理、IDNA | 纯 Go |
| `golang.org/x/image` | v0.13.0 | BSD-3-Clause | 2023-09-11 | 图片缩放、字体/栅格辅助 | 纯 Go |
| `golang.org/x/text` | v0.13.0 | BSD-3-Clause | 2023-08-28 | GBK/UTF-16 编码、宽度计算 | 纯 Go |
| `github.com/tdewolff/canvas` | v0.0.0-20231121141746 | MIT | 2023-11-21 | 抗锯齿矢量光栅（`internal/ui/rx.go`） | 纯 Go；不依赖系统字体服务 |

## `tdewolff/canvas` 是否可裁剪

**结论：不裁。**

- 用途：只在 `internal/ui/rx.go` 里做矢量路径/字形的抗锯齿光栅化，是自绘 UI 的
  高质量渲染后端；GDI 路径仍保留为兼容回退（`兼容渲染（GDI）`）。
- 体积代价：如果完全改回 GDI，预计可减少约 1–2 MB 二进制，但会失去跨机器一致的
  抗锯齿效果；16bpp/老显卡上的观感退化更明显。
- 依赖代价：canvas 引入 `textlayout`、`textprocessing`、`freetype` 等间接模块，
  但全部纯 Go、无 CGO，且已在 Win7 SP1 实机通过自检与 UI 回归。
- 维护代价：当前锁定在 2023-11 的伪版本，升级会带来字体排版行为变化；在没有
  明确收益前不升级。

## 间接依赖与依赖链

`go mod graph` 共 120 条边，最长链 6 层：

```text
win7harness
  -> tdewolff/canvas
  -> benoitkugler/textprocessing
  -> benoitkugler/textlayout
  -> x/net (旧版本，被上游 require)
  -> x/term
  -> x/sys
```

说明：

- 链上的 `x/net`/`x/sys` 是上游模块的历史 require；最终构建按 `go.mod` 的
  直接版本（x/net v0.17.0、x/sys v0.13.0）解析，不会把旧版本编进二进制。
- 没有 `replace`、没有 `vendor/`、没有本地私有模块。
- 所有模块的 `go` 指令 ≤ 1.20，未触发 Go 1.21+ 的构建约束。

## 复现命令

```bash
go run ./cmd/depguard -json
go list -m all | wc -l
go mod graph | wc -l
go mod why -m github.com/tdewolff/canvas
go mod why -m go.etcd.io/bbolt
go mod why -m github.com/alexbrainman/sspi
```

## 结论

1. 没有需要替换或裁剪的直接依赖。
2. 没有 CGO 依赖，满足单文件、零运行时依赖的 Win7 约束。
3. `tdewolff/canvas` 保留；如未来必须减体积，先做 GDI 回退路径的实机观感对比。
4. LGPL 间接依赖与 FreeType 双许可证在发布前做一次法务复核即可，不阻塞构建。
