# 交互层与 UI 改进方案（v1.31.0 建议）

> **落地状态：7 项全部已实现，见文末「实施记录」。**
>
> 基线：`D:\win7harness\work\win7harness` 当前源码（v1.30.0）；所有行号对应当前工作区。
> 范围：仅交互层与 UI，**不含 LLM 能力改动**。
> 硬约束（不可破）：Go 1.20.14 · CGO=0 · 单文件 exe · Win7 SP1 x86/x64 · 不新增第三方依赖 · canvas+GDI 渲染内核不变 · 全部 Win32 调用走 pcall。

---

## 总览

| # | 改进项 | 优先级 | 预估工作量 | 风险 | 依赖 |
|---|---|---|---|---|---|
| 0 | 浅色主题对比度核验 + 令牌一致性加固 | P0（前置） | 0.5 天 | 低 | 无 |
| 1 | 消息内文件路径/行号可点击跳转 | P1 | 1.5 天 | 中 | 0 |
| 2 | Inline diff（行号+高亮）+ 逐 hunk 接受/拒绝 | P1 | 3 天 | 中高 | 0 |
| 3 | plan mode 显式批准/拒绝/修改对话框 | P1 | 2 天 | 中 | 0 |
| 4 | 自定义 slash command（文件加载） | P2 | 2 天 | 低 | 无 |
| 5 | 代码块独立操作按钮（复制/保存） | P2 | 0.5 天 | 低 | 1 |
| 6 | Ctrl+R 历史搜索 + 可配置 keybindings | P2 | 2.5 天 | 中 | 4 |

建议批次：**批次 A = 0+1+5**（低风险、体感提升最大）→ **批次 B = 2+3**（审批与计划核心体验）→ **批次 C = 4+6**（生态与键盘党）。

---

## 0. 浅色主题对比度核验（前置）

### 结论（已查明）
`shots/01*.png` 等浅色截图的"白字浅底"**不是当前源码的令牌 bug**，而是抓图/转换产物：
- `LightTokens().TextPrimary/Secondary/Tertiary` = `#2A2520 / #6B645E / #A09A94`（深色），`theme.go:67-69` 直接映射为正文色。
- 抓图脚本（临时目录 `shot_kit.py` / `shot_diag.py` / `shot_auto.py`）设置 `theme='light'` 且未设 `WIN7H_RENDER`，且写的是**无 BMP 头的裸 BGRA**；转换路径可疑。
- 同为浅色的 `docs/shots/P4/01-chat-wide.png` 文字正常。

### 但仍有两个真实隐患需要修

1. **`theme.go:77`**：`AccentText = tk.AccentHover`（浅色下 `#E8875B` 浅珊瑚）配 `AccentSoft = #FDF4E9`（浅奶油），消费点 `pages.go:159`，是真实的浅底浅字。改为 `tk.AccentText` 或对比度安全的深色。
2. **全局令牌可能残留**：`primitive.go:70` 的 `currentTokens` 默认是 `DarkTokens()`，`App.Paint` 只在 `WIN7H_GALLERY` 分支（app.go:750）调用 `SetTokens`。kit 页靠各自 `SetTokens` 调用，存在主题切换后背景（`a.th`）与文字（全局令牌）分叉的可能——这正是截图所呈现的现象。

### 改动
- `app.go` `Paint`（721-800）：在 `c.Clear(a.th.Bg)` 之前无条件加 `ui.SetTokens(a.th.Tokens())`，删除各页零散的 `SetTokens` 调用。
- `theme.go:77`：修正 `AccentText` 映射。
- `tokens_test.go`：保留 `TestTokensForMatchesTheme`（45-56）作为回归护栏。
- 用**带正确 BMP 头的 GetDC+BitBlt 抓图**（不要用 PrintWindow）重新采集浅色页截图，确认对比度。

### 验收
浅色主题下全部页面的正文/次要文字/徽章文字对比度 ≥ 4.5:1；两套截图（重新抓取）文字均为深色。

---

## 1. 消息内文件路径 / 行号可点击跳转

### 现状锚点
- `chatview.go:151-164` `visItem`：`lines []ui.VisLine`（已解析好的 markdown 布局），是挂元数据的地方。
- `chatview.go:506-510` assistant 绘制：`ui.DrawLines(c, it.lines, ox+4, top+4, clip, th)`；文本原点 = `clip.X+28, top+4`。
- `ui/layout.go:10-18` `Run{X, Text, Bold, Mono, Dim, Color, Size}`；`ui/markdown.go:21-26` `Span{Text, Bold, Code, Dim}` —— **无链接/路径字段，无检测逻辑**。
- `widgets.go:209-216` `HitAt` **倒序扫描，后注册者胜**；`drawMsgActions`（chatview.go:694-719）注册的 `msgzone:N` 覆盖整条消息且无 dispatch 分支，会吞掉点击。
- `core/atref.go:26-56` 已有 `file:line` / `file:from-to` 解析；`core/guard.go:48-68` `ResolvePath`；`core.EscapesWorkDir` / `IsForbiddenPath`。
- `app.go:654-667` 的 `/open` 是"打开 /find 结果"，**不是打开文件**；没有现成的编辑器跳行 helper。

### 设计
新建 `internal/app/msgpaths.go`：
- `var pathLineRe = regexp.MustCompile(...)`：匹配 `internal/app/chatview.go:123`、`foo.go:10-20`、`D:\p\main.go:5`，可选反引号包裹。
- 提取 `core.ParsePathLineRef(rel, workDir) (abs string, from, to int, ok bool)`（从 `atref.go:26-56` 抽出），供 `ExpandAtRefs` 与 UI 共用。
- `type msgLink struct { MsgIdx, LineIdx, RunIdx int; Path string; From, To int; Rect ui.Rect }`，每帧 `a.msgLinks []msgLink`。

### 步骤
1. 在 `chatview.go:284-291` assistant 分支计算 raw blocks（或从 `it.lines` 的 `Run.Text`）做路径检测；因 `msgSig`（chatview.go:185-229）只采样首尾 32 字节，**不要缓存 link 元数据**，改为 draw 时计算（`drawSession` 已有完整坐标）。
2. 在 `chatview.go:506-510` 的 `ui.DrawLines` **之后、且在 `drawMsgActions` 之后**注册 hit：`id := fmt.Sprintf("path:%d:%d:%d", mi, li, ri)`，`u.Hit(id, rect)`；hover 时画强调下划线（`u.hoverT(id)`）。
3. rect 公式：`x = clip.X+28+run.X+(code?12:0)`；`y = dy+ln.Y+(ln.H-run.Size)/2-2`；`w = c.MeasureFace(run.Text, run.Size, weight, face)`；`h = ln.H`。
4. `app.go:1826-1913` `dispatchClick` 增加 `case strings.HasPrefix(id, "path:")`：解析出 `mi/li/ri`，从 `a.msgLinks` 取目标，调 `a.openPathAtLine(path, from)`。
5. `openPathAtLine`：`core.ResolvePath(a.eng.WorkDir, raw)` → 越界/禁路径检查 → `os.Stat`；优先 `code -g path:line`（`exec.LookPath("code"/"code.cmd")`），否则 `win32.ShellOpen(p)` + toast 提示"跳行需 VS Code"，或 `revealPath(p, true)`（`diag.go:171-177`）。

### 风险 / 注意
- **hit 注册顺序**必须在 `drawMsgActions` 之后，否则 `msgzone:N` 吞点击。
- 绘制在 `c.SetTextScale(a.textScale())`（chatview.go:436）下，测量用同一 canvas。
- 正则误报：先 `os.Stat` 校验文件存在再注册 hit，或仅在 hover 时校验。

### 验收
消息中出现 `foo.go:12` 时鼠标变手型、hover 有下划线、点击后用默认编辑器（或 VS Code 跳行）打开；点击非文件路径不触发任何动作；流式输出中不注册 hit。

---

## 2. Inline diff（行号 + 高亮）+ 逐 hunk 接受/拒绝

### 现状锚点
- `core/diff.go:23` `UnifiedDiff` 只返回**一个扁平字符串**，无 hunk 结构；`diffOp` 私有。
- `tools/patch.go:52-62` 有结构化 `patchHunk{at, lines}` / `hunkCatalog`，`parseHunkSel`（459-488）支持 `hunks="1,3-4"` 选择，`dry_run`（177-179）——**已有模型侧的 hunk 选择能力，UI 尚未利用**。
- `tools/registry.go:22` `Confirm func(title, detail string) bool` —— `bool` 返回无法表达"部分接受"。
- `engine.go:461` 授权确认：`e.Confirm(permBanner(...), permDetail(...))`。
- `app.go:3134-3149` `confirmReq{Title, Detail, Resp chan bool}` / `Modal{Title, Detail, Resp, Confirm, Scroll(未用), OnOK}`。
- `app.go:3175-3186` `paintModal` 详情：`FaceMono` 单色逐行绘制，**无行号/配色/滚动**。
- `app.go:3206-3214` `modalClick` 只处理 `modal.ok` / `modal.cancel`（`dispatchClick` 收不到 modal id）。
- `ui/highlight.go:73-192` `highlightLine` 可着色单行，但**未导出且 windows-only**；`ui/layout.go:325-382` `DrawLines` 已导出。

### 设计
1. **结构化 diff API**（`core/diff.go` 新增）：
   ```go
   type DiffLine struct { OldNo, NewNo int; Prefix byte; Text string }
   type DiffHunk struct { Title string; Lines []DiffLine }
   func DiffHunks(oldText, newText, path string, contextRows int) []DiffHunk
   ```
   复用现有 `lcsDiff`（126）与裁剪逻辑，保留 old/new 行号。
2. **确认载荷扩展**：`Confirm` 返回值从 `bool` 升级为结构化决策：新增 `ConfirmDecision{Approved bool; AcceptedHunks map[int]bool}`；`tools.Context` 与 `Engine.Confirm` 同步改签名（`registry.go:22`、`engine.go:113`、bootstrap 注入点 `app.go:322`）。`write_file` / `edit_file` 维持全接受/全拒绝；`apply_patch` 把 `AcceptedHunks` 转成 `hunks` 参数传回 `runApplyPatch`。
3. **请求/模态扩展**：`confirmReq` 增 `Hunks []core.DiffHunk`；`Modal` 增 `Kind`(confirm|plan)、`Hunks`、`Decide func(ConfirmDecision)`。保留 `Resp chan bool` 兼容简单确认，新路径走 `Decide`。
4. **富渲染**：`paintModal` 详情区改用 `ui.DrawLines` + 自建 `[]ui.VisLine`：
   - 双行号槽（old/new），固定宽度 gutter；
   - `+` 行用 `th.Success` 与 `th.CodeBg` 混合底色，`-` 行用 `th.Danger` 混合，`@@` 头用 `th.Accent`；
   - 正文语法色：导出 `ui.HighlightLine`（包装 `highlightLine`）或在 `ui` 包新增 `DrawDiffBlock`；
   - 用 `Modal.Scroll` 接上滚轮（`app.go:1915-2044`）。
5. **逐 hunk 按钮**：每个 hunk 头画 `u.Button("modal.hunk.accept:<i>", "接受", ...)` / `reject:<i>`；在 `modalClick`（3206-3214）里路由，维护 `a.modalHunkState map[int]bool`；最终"允许执行"提交 `AcceptedHunks`。
6. **键盘**：`app.go:2122-2135` 的 modal 键处理增加 H/J/K/L 或 Tab 在 hunk 间移动、空格切换接受/拒绝。

### 风险 / 注意
- `Confirm` 签名变更影响面：grep 所有 `ctx.Confirm` / `e.Confirm` 调用点（`fs.go`、`patch.go`、`guard*`、`engine.go`、`remote/approve.go`、`app.confirm`），一次性改完并保留 bool 兼容包装以缩小 diff。
- `apply_patch` 拒绝部分 hunk 时，未接受 hunk 的语义要在 `runApplyPatch` 里明确（跳过即可，已有 `hunks` 过滤）。
- 远程审批（`remote/approve.go:56`）只回 bool，对 hunk 选择暂回退为"全接受"，文档说明。

### 验收
`DiffPreview=always` 下，确认框显示带双行号、语法高亮、绿/红底色的 diff；可单独接受/拒绝某个 hunk；`apply_patch` 按选择只应用被接受的 hunk；`write_file` 行为不变；手机审批仍可用。

---

## 3. plan mode 显式批准 / 拒绝 / 修改对话框

### 现状锚点
- `agent/plan.go:12-101`：`looksLikePlan` / `planTitle` / `planStepCount` —— **plan 只是文本**，无结构。
- `engine.go:373-379`：发 `Event{Kind:"plan", Text, Name, Args}` 后立即 `turn_end` + `return`，**无等待、无 gate**。
- `app.go:2863-2869` `onEvent` 的 plan 分支：把 `a.input` 覆写成"按上面的计划开始执行" + toast；**事件 payload 被忽略**。
- `app.go:3033-3038` `send()`：靠 `strings.HasPrefix(text,"按上面的计划开始执行")` 退出 `PlanMode`。
- `commands.go:191-200` `/plan` 切换；`bootstrap.go:122-124` 进入时把 `Policy.Mode` 设为 `PermPlan`。
- **潜在 bug**：`send()` 只清 `Engine.PlanMode`，未重置 `Policy.Mode`；`policy.go:117,130` 仍会拒绝写操作。本方案一并修复。
- `app.go:3141-3214` Modal 基础设施可复用，但 `modalAnswer(ok bool)` / `Resp chan bool` 是 bool-only。

### 设计
1. **真实 gate（推荐）**：在 `Engine` 新增阻塞回调（与 `Confirm` 并列，`engine.go:112-115`）：
   ```go
   PlanApprove func(title, plan string, steps int) (decision string, modified string)
   ```
   `loop` 在 `engine.go:375-379` 发完 plan 事件后调用它（在 `turn_end`/`return` 之前），把决策作为一条合成 user 消息注入 `e.Messages`，让模型明确知道"批准/拒绝/修改"。
2. **App 侧**：`onEvent` 的 plan 分支改为投递 `planReq{Title, Plan, Steps, Resp chan PlanDecision}` 到新 channel；主 tick（`app.go:2680-2705`）消费并打开 `Modal{Kind: Plan}`（三按钮：`批准并执行` / `拒绝` / `修改`）。
3. **决策处理**（新增 `planAnswer(dec PlanDecision)`，仿 `modalAnswer` 3216-3238）：
   - 批准：`a.eng.PlanMode=false` 且 `a.eng.Policy.Mode=core.PermAsk`（修 bug）→ 注入 `"用户已批准方案，请开始实施：<plan>"`；
   - 拒绝：保持 `PlanMode=true`，注入 `"用户拒绝了该方案，请说明原因或修改"`；
   - 修改：把 plan 文本填入输入框并聚焦（`a.ui.SetFocus("input")`，参 `msgacts.go:112`），记 `a.pendingPlanModify=true`，`send()` 消费后作为新 plan 送审。
4. **渲染**：plan 正文是 markdown，复用 `ui.ParseMarkdown` + `ui.LayoutMarkdown` + `ui.DrawLines`（参 `chatview.go:678-679`）；标题/步数用 `ev.Name`/`ev.Args`。
5. **键盘**：Enter 默认"拒绝"（安全姿态），Tab 在三按钮间切换，Esc=拒绝。

### 风险 / 注意
- 若不想引入阻塞回调（改动更大），可走"app 状态机"轻量版：保持现有 `PlanMode` 不退出，只把预填输入换成 Modal 三按钮，决策后仍靠注入用户消息驱动模型——这与当前架构一致，但不是严格 in-loop gate。
- 必须同时重置 `Policy.Mode`，否则批准后写仍被拒。
- 子 agent 继承 `PlanMode`（`subagent.go:65-66`、`parallel.go:325-326`），gate 只在主引擎触发。

### 验收
`/plan` → 模型出方案 → 弹出带标题/步骤/正文的对话框；批准才开始写文件；拒绝不产生任何文件改动；修改可编辑后重新送审；批准后 `Policy.Mode` 不再是 `plan`。

---

## 4. 自定义 slash command（文件加载）

### 现状锚点
- `commands.go:84-869` `handleSlash` 是**扁平 switch**（~70 case），default（866-867）对未知命令 toast 后返回 true。
- `commands.go:35-81` `slashHelp` 是单个 `const string`，无动态结构。
- `palette.go:20-77` `palCmds` 是 56 条静态 `var`；`palMatches`（84-116）只扫它；`palRun`（164-201）默认 `a.handleSlash(c.key)`（198）——**自定义命令注入 palCmd 后自动可调度**。
- `internal/skills/skills.go` 是现成模板：`ProjectDir`（49-51）、`GlobalDir`（54-56）、`Scan`（75-116）、`Find`（119-130）、`splitFrontMatter`（216-252）。
- `commands.go:326-350` `/skills`、`pagedata.go:22-87` 的 `uiData()` TTL + `uiDataDrop()` 失效模型。
- `core/config.go:99-217` 无 commands/键位字段；`Load()`（381-424）只在启动调用。

### 设计
1. 新建 `internal/commands`（或并入 `skills`）：
   ```go
   type Command struct { Name, Desc, Body, Path, Scope string }
   func ProjectDir(wd string) string // <wd>/.harness/commands
   func GlobalDir() string            // <DataDir>/commands
   func Scan(wd string) []Command     // 镜像 skills.Scan
   func Find(wd, name string) (Command, bool)
   ```
   front matter 复用 `skills.splitFrontMatter`（`name/description`），body 上限 20000 rune。
2. `handleSlash` 在 default 之前（866）：`if c, ok := commands.Find(a.cfg.WorkDir, cmd); ok { a.insertInput(expand(c.Body, rest)); return true }`。采用 Claude Code 语义——**展开到输入框供用户确认/编辑**（`insertInput` 在 `app.go:2575`），不自动发送，避免 `send()` 递归。支持 `$ARGUMENTS` / `{{args}}` 占位符替换为 `rest`。
3. `/help`（97-102）追加动态生成的 `**自定义命令**` 段（把 `slashHelp` 保持 const，额外拼一段运行时字符串）。
4. `palette.go:84-116` `palMatches`：注入 `palCmd{key:"/"+c.Name, name, desc}`，使 Ctrl+K 面板列出自定义命令（兑现 `使用说明.txt:235-238` 的承诺）。
5. 新增 `/commands` 处理器（仿 `/skills` 326-350），支持无参列出、有参编辑路径；`pageData` 增 `commands` 字段并在 `uiDataDrop()` 后失效。
6. 文档：`docs/使用说明.txt` 在 405-426（skills 段）旁边新增命令目录说明：`<工作目录>/.harness/commands/*.md` 与 `%APPDATA%\Win7Harness\commands\*.md`。

### 风险 / 注意
- 与内置命令同名的处理：内置优先（default 分支才查自定义）。
- 不把命令正文注入 `buildContext`（`engine.go:629`）——自定义命令是用户主动调用，不是自动注入。

### 验收
在 `commands/hello.md` 写 front matter+正文 → `/hello` 展开到输入框 → Ctrl+K 能搜到 → `/commands` 列出 → `/help` 显示；项目级覆盖全局同名命令。

---

## 5. 代码块独立操作按钮（复制 / 保存）

### 现状锚点
- `ui/layout.go:226-265` fenced code 块产出连续 `VisLine`（`lineCodeFirst`…`lineCodeLast`），但 **VisLine 无 block id**；第 141 行 `bi` 被丢弃（`_ = bi`）。
- 代码块原始文本在 `ui/markdown.go:29-41` `Block.Lines` + `Lang`；**不要从高亮后的 Runs 重建文本**。
- `chatview.go:255-334` `buildMsgItems` 可拿到 blocks 与 lines 的对应关系。
- `chatview.go:666-668` `toolcopy` 是 `u.Button` 的现成范例；`msgacts.go:17-35` `copyMsg` 是复制范例。
- `diag.go:171-177` `revealPath`；`win32/multimodal.go:223-240` 有保存对话框相关 helper（可评估是否用原生 SaveFile 对话框）。

### 设计
1. `visItem`（chatview.go:151-164）增 `codeBlocks []codeBlockRegion{BlockIdx, FirstLine, LastLine int; Lang, Text string}`；在 `buildMsgItems` 里把每个 `Block{Kind:BlockCode}` 的 `b.Lines` join 成 `Text` 并记录对应 VisLine 区间。
   （备选：给 `ui.VisLine` 加 `Block int` 字段，在 `layout.go:240-263` 用 `bi` 填充。）
2. `drawSession` assistant 分支（506-510）在 `drawMsgActions` **之后**，对每个代码块首行右上角画两个小按钮：
   - `codeact.copy:<mi>:<bi>` → `ui.R(rect.Right()-104, rect.Y+2, 46, 20)`
   - `codeact.save:<mi>:<bi>` → `+52`
3. `dispatchClick` 增两个 case → `a.copyCodeBlock(mi,bi)` / `a.saveCodeBlock(mi,bi)`。
4. `copyCodeBlock`：`win32.SetClipboardText(text)` + toast。
   `saveCodeBlock`：用 `core.ResolvePath(a.eng.WorkDir, 建议文件名)` + `core.WriteFileAtomic`，再 `revealPath(p,true)`；或接原生保存对话框让用户选路径。

### 风险 / 注意
- hit 注册顺序同 item 1（在 `drawMsgActions` 之后）。
- 代码背景铺满 `clip.W`，文字有 `pad=12`；按钮放右上角避免遮挡语言标签。
- `msgSig` 弱签名：若把 codeBlocks 缓存进 `laySeg`，需加强签名为全文 hash。

### 验收
代码块右上角出现"复制/保存"按钮；复制得到原始代码（非高亮 Runs）；保存到工作区后能在资源管理器中定位。

---

## 6. Ctrl+R 历史搜索 + 可配置 keybindings

### 现状锚点
- **无任何 prompt history**（全仓搜索确认）；用户 prompt 只作为 `Message` 存在会话文件里（`store.go`）。
- `app.go:2180-2182` `editKey` 把 Up/Down 消费为"按显示行移动光标"，所以方向键不能调历史。
- `app.go:2165-2166` 算 `ctrl/shift`；**alt 未读取**，无左右键区分。
- `VK_R` 未定义（`win32/types.go:263-284`；`app.go:2268` 用裸 `0x4E` 表示 N）。
- `Ctrl+R` 当前未处理，落入 `DefWindowProc`。
- `palette.go` 是 overlay 模板：状态（app.go:110-114）、`openPalette`（141-161）、`palKey`（204-240）、`palType`（257-269）、`palRun`（164-201）、`palBound`（242-254）、`palClick`（271-285）、`paintPalette`（287-359）；绘制分发在 `app.go:782-785`（用 `c.Layer()`）。
- 评分：`palMatches`（84-116）为前缀/包含；`codeidx/index.go:744-794` `Search` 有更丰富分级；**无真正 fuzzy/subsequence 匹配器**。
- `config.go:134` 只有 `SendOnEnter` 一个输入相关配置；无 keybindings 字段。

### 设计

**（a）历史持久化**
- `core/config.go` 增 `PromptHistoryPath() = DataDir()/prompt-history.json`（仿 281-287 的 `*Path()`），或直接在 `Config` 加 `PromptHistory []string`（`omitempty`）。
- 记录点：`app.go:3014-3059` `send()`，在 `/` 命令 bail-out（3023-3025）与空检查（3026）之后，记录 `text`；去重 + 上限（仿 `TouchRecentDir` 688-704，建议 200 条）。
- `core.WriteFileAtomic` 写入（仿 `config.go:641-650`）。

**（b）Ctrl+R overlay（镜像 palette）**
- App 状态：`histOpen/histQ/histCur/histIdx/histResults/histRect`。
- 插入点：`KeyDown` 早返回区（`app.go:2116-2135`，palette 之后）加 `if a.histOpen { return a.histKey(vk) }`；Ctrl+R 触发点放在 `app.go:2184-2221` 的 Ctrl 加速区、**`editKey`（2180）之前**，确保输入框聚焦时也生效。
- `Char`（app.go:2533-2573）路由到 `histType`；`paint`（782-785 附近）用 `c.Layer()` 画 `paintHistory`；`MouseDown/Up`（1727/1807）加 `histClick`。
- 结果评分：新增 `fuzzyScore(query, candidate)`（subsequence + 前缀/词边界/相邻加分，参照 `codeidx.Search` 761-777），稳定排序；结果行用 `convsearch.go:56-105` 的 `clipSnippet` 风格预览。
- Enter → `a.insertInput(selected)` 并关闭；Esc 关闭；↑↓ 导航；再次 Ctrl+R 关闭。

**（c）可配置 keybindings**
- `core/config.go` 增：
  ```go
  type KeyBindings map[string]string // action -> "ctrl+r", "ctrl+shift+t"
  // Config 增字段：KeyBindings map[string]string `json:"keybindings,omitempty"`
  ```
  `DefaultConfig()`（289-321）填默认值（`historySearch:"ctrl+r"`, `palette:"ctrl+k"`, ...）；因 `Load()` 直接 unmarshal 到默认配置上，缺失键自动保留默认。
- 纯 Go parser/canonicalizer：`parseCombo("ctrl+shift+t") -> {ctrl, alt, shift, vk}`，小名→VK 表（补 `VK_R=0x52`、`VK_N=0x4E` 到 `win32/types.go`）。add `alt := win32.GetKeyState(win32.VK_MENU)`（types.go:276 已有 VK_MENU）。
- `KeyDown` 用 `a.matchBinding("historySearch", vk, ctrl, shift, alt)` 替换硬编码判断；保留硬编码兜底以兼容旧配置。
- 设置页（`pages.go:559` 附近）加绑定编辑行 + `a.cfg.Save()`。

### 风险 / 注意
- Ctrl+R 在 `editKey` 之前拦截，不能破坏输入框内普通 `R` 字符输入（只有 Ctrl+R 组合才触发）。
- 历史文件含用户 prompt 文本，写入前不宜记录 API key 类输入——可加最小长度/敏感词过滤（可选）。
- keybindings 配置错误时回退默认，不能导致无快捷键可用。

### 验收
Ctrl+R 弹出历史搜索框；输入子串/字母序列能筛出过往 prompt；Enter 回填输入框；历史跨会话/重启保留；改配置后 `Ctrl+R` 改为其他组合仍生效（默认兜底正常）。

---

## 依赖与排期建议

```
批次 A（约 2 天）: 0 ──> 1 ──> 5
批次 B（约 5 天）: 2、3（共用 Modal/决策通道改造，建议一起做）
批次 C（约 4.5 天）: 4 ──> 6（6 复用 4 的 palCmd 注入与 overlay 模式）
```

- 批次 A 不改任何协议/签名，可独立发布。
- 批次 B 改 `Confirm` 签名与 Modal 决策通道，是最大改动面，务必一次性 grep 全调用点并保留 bool 兼容包装。
- 批次 C 纯增量，`internal/commands` 与 palette overlay 互不耦合。
- 每批次完成后跑：`go test ./...`、自检 77/77、交互回归 11/11、双架构编译，并补对应截图到 `docs/shots/`。

---

## 实施记录（2026-11-18）

七项全部落地，新增/修改文件如下：

| 项 | 主要文件 | 实现要点 |
|---|---|---|
| 0 | `ui/theme.go`、`app/app.go` | `AccentText` 改用 `TextPrimary`；`Paint` 每帧无条件 `ui.SetTokens(a.th.Tokens())` |
| 1 | `core/atref.go`、`ui/layout.go`、`app/msgpaths.go` | 抽出 `ParsePathLineRef`；`VisLine.IsCode()`；路径正则 + 文件存在校验 + `path:<i>` 命中 + VS Code `code -g` 跳行 |
| 5 | `ui/layout.go`、`ui/highlight.go`、`app/chatview.go`、`app/msgcode.go` | `VisLine.Block` 定位代码块；导出 `HighlightLine`；复制/保存按钮 + `codeBlockEntry` 帧缓存 |
| 2 | `core/diff.go`、`tools/registry.go`、`tools/fs.go`、`tools/patch.go`、`agent/engine.go`、`app/decision.go` | `DiffHunks` 结构化 API；`ConfirmDiff` 回调（write/edit 整体确认，patch 逐 hunk）；行号/增删底色/语法高亮弹窗 + hunk 开关 + 滚轮 |
| 3 | `agent/engine.go`、`bootstrap/bootstrap.go`、`app/decision.go`、`app/app.go` | `PlanApprove` 阻塞闸门；批准/拒绝/修改三按钮；批准时同时清 `PlanMode` 与恢复 `Policy.Mode`（修掉旧 bug） |
| 4 | `internal/commands/`（新）、`skills/skills.go`、`app/commands.go`、`app/palette.go` | 命令目录扫描/front matter/`{{args}}`；default 分支展开到输入框；`/commands`；Ctrl+K 注入；使用说明已补 |
| 6 | `core/prompthistory.go`、`core/keybind.go`、`app/history.go`、`app/app.go` | 跨会话历史文件；fuzzy 评分浮层；`Config.KeyBindings` + `MatchBinding`；Ctrl+R 在 `editKey` 之前拦截 |

### 验证
- `go build ./internal/app ./internal/commands ./internal/core ./internal/agent ./internal/tools ./internal/ui ./internal/bootstrap ./internal/skills` 通过。
- 上述包 `go test` 全绿；新增 `core/diff_test.go`、`core/keybind_test.go`、`core/prompthistory_test.go`、`commands/commands_test.go`、`app/msgpaths_test.go`。
- 注意：验证期间同一仓库有**另一个进程并发新增** `internal/evolve` 等包，全量 `go build ./...` 曾因该包自身未完成的符号（`SaveRun`/`LoadHistory` 等）报错；与本方案无关，待其作者收尾后重跑全量即可。

### 视觉验收（已做）
用 `WIN7H_SHOT_MODAL=diff|plan` 调试入口（样例数据挂模态，不需要真实 LLM）实际起程序抓图：
`shots/modal-diff.png`、`shots/modal-plan.png`。

抓图发现并修掉的两个真实缺陷：
1. **底栏按钮被裁掉**：`paintDiffModal` / `paintPlanModal` 的正文 `c.Clip(body)` 用了 `defer PopClip`，
   而"取消/应用所选"、"拒绝/修改/批准"按钮画在 body 下方，整块被裁掉，弹窗底部一片空白。
   改为正文绘制完显式 `c.PopClip()` 后再画底栏。
2. **代码行字号过小**：`FsXs-2`(9px) 在 1296×840 下偏小，提到 `FsXs`(11px)、行高 17→18。
3. hunk 开关从 `BtnSubtle`（无边框，看着像普通文字）改为 `BtnGhost`（有描边，可点性明确）。

### 仍需真机验收
- 浅色主题两页对比度（截图产物问题，已加令牌一致性护栏，需真机重抓图）。
- 16bpp / DPI 125%/150% 下的排版；代码块按钮与语言标签是否重叠。
- 全量自检 77/77、交互回归 11/11、双架构编译打包。
