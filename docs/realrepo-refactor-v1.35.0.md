# 真实仓库 10+ 文件重构验收 v1.35.0

## 任务

仓库：`D:\win7harness\work\win7harness` 的完整副本（431 个文件，去掉 .git/dist/bench-out 等）。
提示词：

> 把 `internal/agent` 包中 `Engine` 结构体的 `WorkDir` 字段重命名为 `ProjectRoot`。
> 只改 Engine 的字段及其 `e.WorkDir` / `eng.WorkDir` 引用；不要改 `Config.WorkDir`、
> `Conv.WorkDir`、`tools.Context.WorkDir` 这些同名字段，也不要改 go.mod/go.sum。
> 同步更新注释与测试。改完运行 `go test ./...`。

这是有语义歧义的重名重构：仓库里同时存在四个 `WorkDir`，模型必须只改 Engine 的那一个。

## 结果

- 改动文件：**13 个**
  - `internal/agent/engine.go`、`parallel.go`、`subagent.go`、`verify.go` 及两个测试
  - `internal/app/app.go`、`cmds_v126.go`、`commands.go`、`complete.go`、`extpage.go`、`msgpaths.go`、`recovery.go`
- `grep -C 'e.WorkDir|eng.WorkDir'`：**0 处残留**
- `grep 'ProjectRoot'`：**42 处**，分布在 13 个文件
- `go build ./...`：通过
- `go test ./...`：20 个包全部 `ok`（agent 7.3s、app 11.9s、core 62.4s），无 FAIL/panic
- 任务链路约 7 分钟；`go test ./...` 单次约 312s（副本无构建缓存）

## 过程中暴露的问题（已修）

Agent 为了把 `go`/`gofmt` 放到 PATH，尝试在：

`%USERPROFILE%\.local\bin\`

写 `go.cmd` / `gofmt.cmd` 两个包装脚本。CLI 的 `--yes` 当时把“工作区之外的写”也放行了。

已修复：`--yes` 现在只批准工作区内操作；`guardEscapes/guardRead` 的“工作区之外的路径”确认
在 `--yes` 下也会被拒绝。包装脚本已清理。

## 结论

harness 已经能完成**真实仓库、13 文件、带同名干扰字段**的重构，并跑通全量测试；
但长任务里模型仍会为了环境问题去改工作区外的东西，边界收紧后这类行为会被拦下。
