# 自迭代（RSI-lite）Stage 0

> 目标：无人值守地发现小改进、隔离实现、自动验证、合入影子分支，失败自动回滚。
> 当前阶段只动 `rsi/auto`，不动 `master`。

## 为什么先做 Stage 0

自迭代最容易失败的地方不是“让模型改代码”，而是“没有可信的尺子”。当前 eval 只有 9 个 base 用例，单项 100% 通过时，任何候选都是同分。因此第一版先把**隔离、门禁、记录、回滚**做出来，再逐步扩评估集和自主边界。

## 运行方式

```bat
go run ./cmd/evolve -dry-run -goal "只给一个低风险提案"
go run ./cmd/evolve -max 1
go run ./cmd/evolve -max 2 -goal "补一组快照去重回归测试"

:: 观察期：看影子分支领先/落后与最近实验，不需要模型
go run ./cmd/evolve -status

:: 常驻夜间循环；创建 .rsi/STOP 或 Ctrl+C 停止
go run ./cmd/evolve -nightly -interval 24h -max 1

:: 离线复现：不调用模型提案/实现，直接用现成提案和补丁过门禁
go run ./cmd/evolve -max 1 -proposal-file rsi-out\proposal.json -patch rsi-out\candidate.patch
```

常用参数：

| 参数 | 说明 |
| --- | --- |
| `-dry-run` | 只提案，不执行、不合并 |
| `-base` | 基线分支，默认当前分支 |
| `-target` | 目标分支，默认 `rsi/auto` |
| `-max` | 本轮候选上限，默认 2 |
| `-rounds` | 单候选工具轮数上限，默认 32 |
| `-max-tokens` | 本轮 token 预算（按实现器累计） |
| `-max-minutes` | 本轮墙钟上限，默认 180 |
| `-max-diff` / `-max-files` | 单候选改动规模上限 |
| `-allow-dirty` | 允许主工作区有未提交改动（仅调试用） |
| `-proposal` / `-proposal-file` | 直接使用指定提案，跳过模型提案（复现/离线） |
| `-patch` | 直接应用指定补丁，跳过模型实现（复现/离线） |
| `-bench-gate` | 候选改到 agent/tools/llm/skills 时跑一个真实 bench（默认关） |
| `-bench-task` | bench 门禁 fixture，默认 `argsfix` |
| `-bench-timeout` | bench 门禁超时，默认 5 分钟 |
| `-nightly` | 常驻循环，每隔 `-interval` 跑一轮 |
| `-interval` | 夜间循环间隔，默认 24h |

## 一轮做什么

```
信号（PROGRESS 待办 / TODO / 历史失败）
  → 提案（1-3 条，必须可验证）
  → git worktree 隔离
  → 受限子代理实现
  → 可信门禁
  → 合入 rsi/auto
  → 合并后门禁；失败自动 revert
  → 写入 rsi-out/<run>/ 与 history.jsonl
```

## 门禁

1. 改动规模（文件数 / 增删行数）
2. 受保护路径哈希（候选不能改尺子）
3. 测试完整性：允许新增测试行，但禁止删改既有 `*_test.go`（防止删断言刷绿）
4. 只检查候选改过的 Go 文件是否 `gofmt`
5. `go vet -unsafeptr=false ./...`
6. `go test ./... -timeout 20m`
7. 可选 agent bench：仅当候选改到 `internal/agent` / `tools` / `llm` / `skills` / `bench`，且开启 `-bench-gate`
8. `GOARCH=386` 与 `amd64` 的 `go build ./...`
9. 评测基线逐用例对比：skip 视为失败，用例消失视为失败
10. 端到端自检（selftest）
11. 合并后再跑一次门禁，失败自动 `git revert`

受保护路径默认包括：

- `internal/eval`
- `internal/selftest`
- `internal/evolve`
- `cmd/evolve`
- `cmd/evalrun`
- `cmd/selftest_run`
- `cmd/bench`
- `internal/bench`
- `build.sh`
- `go.mod` / `go.sum`
- `scripts/check.py`

## Agent Bench（模型层尺子）

`internal/eval` 的 base 层只测工具链；`internal/bench` 用两个小 fixture 测
“模型 + 工具 + 提示词”的真实修复能力：

```bat
go run ./cmd/bench
go run ./cmd/bench -task argsfix -timeout 5m
```

每个 fixture 初始有一个失败测试，agent 在临时副本里修；跑完再验证测试通过。
测试文件、`go.mod`、`go.sum` 的哈希会被校验，agent 改测试来“通过”直接判失败。
报告落在 `bench-out/<时间>/report.md|json`。

当前 fixture：

- `argsfix`：修复 `--timeout=90` 等号形式被忽略；
- `slug`：修复 `Slug` 的首尾/连续空白归一化；
- `dedupe`：修复切片去重并保留首次出现顺序；
- `jsonfield`：修复 JSON 字段名与 `omitempty`。

bench 目前是“能力看板”，尚未接入自动合并门禁；等成绩稳定后再作为提示词/工具
改动的回归闸门。

## 安全边界

- 实现器禁网、禁子代理、禁 git 写操作；命令只放行 `go` / `gofmt` / `git` 只读。
- 任何需要确认的操作一律拒绝（无人值守没有“点确认”的人）。
- 候选 worktree 在仓库外，默认 `../win7harness-rsi`。
- 不 force push，不自动发布，不改主分支。
- 风险 C（权限、存储、更新器、Win32、依赖、构建发布）只记录，不执行。

## 产物

```
rsi-out/
  history.jsonl              # 追加式实验历史
  <run-id>/
    run.json                 # 结构化报告
    run.md                   # 人类可读报告
    run.log                  # 过程日志
    <candidate>.patch        # 候选完整 diff（清理 worktree 后仍可复查）
```

## 性能取舍

- 实现器被禁止运行全量 `go test ./...` 和 `run_tests`，只跑目标包；全量测试由门禁统一跑。
- 门禁不传 `-count=1`，未改动的包直接吃 Go 测试缓存。
- `cmd/evolve` 默认把 `GOCACHE` 指到 `%TEMP%\win7harness-rsi-gocache`，避免和开发机上其它 `go test` 抢默认缓存锁。
- 合并后如果目标分支的树与候选树完全一致（单线程循环的常态），跳过重复的 post-merge 全量门禁；只有树不一致时才重跑并保留自动回滚。
- 实现器轮数到顶但工作区已有真实改动时，不直接判失败，交给门禁判定：门禁过就合并，不过就丢弃。

## 当前已知限制

- 还没有 UI 页面和夜间调度；先用 CLI。
- agent bench 已扩到 4 个 fixture，并有可选的 `-bench-gate`（默认关）；UI 回归 / 性能基线尚未接入。
- 影子阶段不自动合并 `master`；观察 2 周后再放权。
- `internal/core` 的全量测试在 Win7/杀毒环境下可能十几分钟，首次冷缓存会较慢。

## Stage 0 验证记录

2026-11-18 影子运行（`rsi-out/20261118-120825`）：

- 候选：修复 `--timeout=90` 等号形式被静默忽略（B 类，2 文件 +41/-9）。
- 门禁：受保护路径、gofmt、vet、全量测试、386/amd64 构建、eval、selftest（76/77，1 skip）、工作区清洁全部通过。
- 合并：候选合入 `rsi/auto`（`581b395`），`master` 未改动；合并后树与候选一致，自动跳过重复门禁。
- 耗时：全量 `go test` 约 9 分钟（同时有其它会话在跑测试），整轮 10.1 分钟。

2026-11-18 完全模型驱动运行（`rsi-out/20261118-130606`）：

- 提案：给 `cmd/bench` 增加 `-json`，stdout 只输出 `Report.JSON()`。
- 实现：模型在隔离 worktree 里用 `edit_file/write_file` 完成 4 文件 +136/-2，并新增两个测试文件。
- 门禁：受保护路径、测试完整性、gofmt、vet、全量测试、386/amd64 构建、eval、selftest、工作区清洁全部通过。
- 合并：候选合入 `rsi/auto`（`8e0cfc6`），`master` 未改动；整轮 9.0 分钟，668k token。
- 说明：这是第一次 `propose → implement → gate → merge` 全部由模型驱动的完整闭环。

2026-11-18 Agent Bench 基线（`bench-out/20261118-174938`）：

- 4 个 fixture 全部通过：`argsfix`、`slug`、`dedupe`、`jsonfield`。
- 总耗时 49.8 秒，约 134k token；四个任务都只改非测试源文件，未触发测试哈希保护。

2026-11-18 负向门禁验证（`rsi-out/20261118-173913`）：

- 故意提交一个语法错误的 `internal/core/cliargs.go` 候选。
- 门禁在 `gofmt` 步骤失败，候选标记 `gate-failed`，未合入 `rsi/auto`，`master` 未改动。
- 观察期可用 `go run ./cmd/evolve -status` 查看影子分支领先/落后和最近实验。

## 下一阶段

1. 观察期在提示词/工具改动上启用 `-bench-gate`，校准耗时与 token 成本。
2. 加 UI 交互回归与性能基线。
3. 影子分支稳定后，允许 A/B 类自动合并 `master`，保留自动 revert。
4. 加“进化”页面和调度入口。
