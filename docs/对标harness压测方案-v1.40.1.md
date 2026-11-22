# 对标 Harness 的稳定性压测方案（v1.40.1）

> 目标：不是再跑一次“能不能启动”，而是用公开 benchmark 的方法论，对
> WIN7 Harness 的 **GUI + Agent 运行时** 做可复现、可量化、多次重复的抗压测试。
> 重点覆盖 Win7 SP1 虚拟机（VirtualBox 7.0.20，x64 / x86 双架构）。

---

## 1. 公开 benchmark 对标（为什么要这样测）

| Benchmark | 它测什么 | 我们借用的方法 | 不照搬的原因 |
|---|---|---|---|
| **SWE-bench Pro**（1865 题 / 41 仓库；Verified 已被 OpenAI 于 2026-02 判定污染并退役） | 真实 GitHub issue 的代码修复；最终以仓库测试通过与否判定 | **结果校验**：只看最终状态（文件落盘、会话可重新打开），不看“点了按钮就算成功” | 我们用 Go + 自绘 Win32 UI，不是 Python 仓库；且重点是稳定性而非解题率 |
| **Terminal-Bench 4.0**（66 题，avg@3，8 小时上限，每个任务独立 verifier） | 终端里的长周期真实任务；harness 与模型对分数影响极大 | **avg@N 重复跑**：同一套用例跑 3 次以上，报告通过率与波动；**错误分类**：超时/崩溃/UI 停滞/状态不一致分开记 | 我们没有 Docker 化任务集；把同类方法落到 PowerShell 驱动的真实 EXE 上 |
| **OSWorld 2.0**（369→2.0 版；真实桌面多应用操作） | 在真实操作系统里操作 GUI，分数强依赖环境稳定性 | **真实 VM + 真实窗口消息**：不用 mock，PostMessage 驱动 Win7 上的真实窗口；DPI/低色深/窗口尺寸矩阵 | OSWorld 面向 Ubuntu/Windows 10+ 的现代桌面，Win7 自绘 GDI 不在其环境内 |
| **τ-bench / τ²-bench**（工具调用 + 模拟用户，关注跨轮一致性） | 多轮工具调用后数据库终态是否正确 | **多轮状态一致性**：连续新建/切换/删除会话后校验会话文件数量与内容 | 我们没有客服数据库，会话目录就是终态 |
| **BFCL**（工具调用格式正确性） | 工具调用是否合法 | **工具压力**：在 Agent 层用现有 bench fixture 校验工具参数、停止/取消语义 | 已由 `internal/bench` 22 个 fixture 覆盖，本轮不重复跑（需 LLM key） |
| **AgentBench**（8 环境，ICLR 2024） | 多环境能力，长期推理与指令跟随是主要瓶颈 | **长时运行**：会话历史 240 条、600 个文件、连续滚轮/翻页，观察是否累积劣化 | 早期 benchmark，分数参考意义低于上面几项 |

参考链接：tbench.ai、swebench.com、os-world.github.io、github.com/sierra-research/tau-bench、
github.com/ShishirPatil/gorilla（BFCL）、arxiv.org/abs/2308.03688。

**结论**：公开 benchmark 证明了一件事——同一个模型/系统在不同 harness 下分数会大幅波动，
且必须报告重复次数与置信区间。因此我们的压测只承诺三件事：**重复、终态校验、错误分类**。

---

## 2. 测试分层

### L1 单元 / 纯函数（宿主，Go 1.20.14，CGO_ENABLED=0）
```
go test ./...
go vet ./...
CGO_ENABLED=0 GOOS=windows GOARCH=386 go build ./...
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ./...
```
覆盖：布局纯计算、kit 路由、会话搜索、滚动钳制、工具取消、配置校验等。

### L2 GUI 回归矩阵（宿主 Windows，真实 EXE）
```
powershell -File scripts/ui_regression.ps1 -Exe ... -DPI 1.0
powershell -File scripts/ui_regression.ps1 -Exe ... -DPI 1.25
powershell -File scripts/ui_regression.ps1 -Exe ... -DPI 1.5
powershell -File scripts/stability_matrix.ps1 -Exe ... -Rounds 3
```
每次跑完整页面导航（设置→工具→7 个二级页）、长会话虚拟滚动、懒加载、
kit 返回按钮、无 UI 停滞、无 panic、干净退出。`stability_matrix.ps1` 按
Terminal-Bench 的 avg@N 汇总通过率。

### L3 Win7 SP1 虚拟机压力（VirtualBox，guestcontrol 驱动）
```
VBoxManage startvm win7agent --type headless
VBoxManage sharedfolder add win7agent --name wh --hostpath <host-dir> --automount
VBoxManage guestcontrol win7agent run --username agent --password <pw> \
  --exe C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe \
  -- -NoProfile -NonInteractive -ExecutionPolicy Bypass -File Z:\stress_guest.ps1 \
     -Exe Z:\WIN7Harness-amd64.exe -Tag amd64 -Rounds 12 -Sessions 240
```
`stress_guest.ps1` 每轮做：

1. 独立 HOME，240 个历史会话 + 600 个工作区文件（触发后台索引与懒加载）；
2. 5 档窗口尺寸循环（900×600 → 1600×900），触发 rail/侧栏/检查器的抽屉断点；
3. 会话区主按钮连续新建、搜索、长列表滚轮、打开旧会话；
4. 设置 → 工具，逐个打开自检/定时/扩展/用量/工作区/费用/通知；
5. 返回对话（顶栏按钮与 Esc 交替）；
6. 每轮检查 `Responding`、扫描 `harness.log` 的 `ui-stall:` / `PANIC`；
7. 最后强杀进程、核对会话文件数量不丢、重启并再次打开全部会话页。

x86 架构用同一脚本把 `-Exe` 换成 `Z:\WIN7Harness-386.exe`，
这是 Win7 上最容易暴露 GDI/内存/对齐问题的组合。

### L4 Agent 运行时（需 LLM key，本轮未跑）
`go run ./cmd/bench -task <id>`（22 个 fixture 的历史基线：22/22，见
`docs/bench-baseline-v1.34.0.md`）。本轮聚焦 UI/稳定性，不重复消耗 token。

---

## 3. 错误分类（对齐 Terminal-Bench 的 error taxonomy）

| 类别 | 判定信号 | 处理 |
|---|---|---|
| 崩溃 | 进程消失 / `PANIC` | 立即失败，保留 dump |
| UI 停滞 | `harness.log` 出现 `ui-stall:` 或 `Responding=false` | 记 stall，附 goroutine 栈 |
| 状态丢失 | 强杀后会话文件数减少 / 重启后打不开 | 记 state-loss |
| 布局错误 | 控件命中偏移、按钮点不到（截图 + 命中日志） | 记 layout |
| 偶发 flaky | 同一 DPI 多次结果不一致 | 记 flaky，按 avg@N 报告 |

---

## 4. 实测结果（2026-11-22）

- L1：`go test ./...` 全包通过；宿主 Go 1.20.14 / CGO_ENABLED=0。
- L2：见 `stability_matrix.ps1` 输出（本轮以 L3 为主，L2 脚本已就位）。
- L3：见 `vm-share/stress-report-amd64.txt`、`stress-report-386.txt`（由 VM 内脚本生成）。
- 已知历史基线：v1.31.1 `-selftest --offline` 76/77（1 SKIP，LLM 未配置）；
  UI 回归 DPI 1.0/1.25/1.5 曾 15/15。

> 注：公开 benchmark 的“分数”不能直接搬到自绘 GUI 上。本方案只借它的
> **重复运行 + 终态校验 + 错误分类**，不声称与任何 leaderboard 分数可比。
