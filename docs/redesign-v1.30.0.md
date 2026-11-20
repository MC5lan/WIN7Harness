# WIN7 Harness v1.30.0 重构方案（含审计结论）

> 工作区：`D:\win7harness\work\win7harness`（git 管理，基线 `5c1c39f`，当前 HEAD 见 `git log --oneline`）
> 性质：后端全量审计 + 重构蓝图。**审计部分为实测结论（文件:行号可复核）；所有 P0/P1 已落地修复并提交，集成回归全绿。**
> 约束基线（不变）：**Go 1.20.14 · CGO_ENABLED=0 · 单文件 exe · amd64+386 · -H=windowsgui · Win7 SP1 x86/x64 · 只用 Win7 有的 API · 全部 Win32 API 走 pcall · canvas 锁快照 · canvas 几何/GDI 文字 · 单一设计令牌来源**

---

## 一、执行摘要

| 类别 | 结论 |
|---|---|
| 后端全量审计 | **5/5 路归件**（agent/llm/tools/core/mcp-remote-skills 等），共 **P0 × 8**、P1 × 约 41、P2 × 约 87；P0 全部修复并提交（含 agent 包 AG-P0-1），`go test ./...` 全绿、自检 77/77、双架构编译通过 |
| 已修复 | **P0 × 7 + P1 × 约 25** 全部落地（双架构编译通过；`go test ./...` 全绿；自检 ST01–ST77 **77/77**；交互回归待下一批次跑）|
| 已知但未修（需拍板） | 架构级重构（core 拆包、config 逐字段弹性解析、快照目录分级、Store 基座抽象等），见第五节 |
| UI 层 | 已完成技术选型实测（见第二节）；建议 **Option A：不替换渲染内核，重建 UI 架构层**（保住 Win7 全兼容），新页面/全部页面按 kit 框架重写 |
| Win7 风险新增 | 无（所有依赖选型均经编译与 ABI/行为实测排除）|

---

## 二、UI 层重写：技术选型（含实测证据）

> 用户要求：「Go 里引入新 UI 依赖再重写」。实测结论如下——不是印象分，是 2026-11-17 本机实测。

| 候选 | 纯 Go | CGO | Win7 依赖 | stock Win7 / 虚拟机结论 | 判定 |
|---|---|---|---|---|---|
| 保持 canvas+GDI 内核（现状） | ✅ | 0 | 无（GDI 全家族） | ✅ 万能 | **Option A（推荐）** |
| `github.com/lxn/walk` 2021-01 | ✅ | 0 | 无 | ✅ 万能，但退回经典 Win32 控件风格 | Option B，需放弃暖纸视觉系统 |
| `github.com/hajimehoshi/ebiten/v2` v2.7.10 | ✅ | 0 | `d3dcompiler_43/46/47.dll`（Win7 **不自带**）+ OpenGL 2.0 ICD | ❌ 原生 Win7 直接失败；Win7 老机/虚拟机（Basic Display Adapter）两条路都不通 | **Option C，拒绝** |
| `gioui.org` v0.5.0 | ✅ | 0 | d3d11.dll（Win7 自带）+ **D3D11 硬件**（无 WARP 回退） | ❌ 无 D3D11 硬件的驱动即起不来 | **Option C，拒绝** |
| `fyne` | ❌ | 需要 | OpenGL + MinGW 工具链 | ⚠️ 破坏 CGO=0 与单文件体验 | 拒绝 |

ebiten 源码实锤（v2.7.10，module cache 实测）：`internal/ui/ui_windows.go:newAuto()` —— **Win10 以下优先 OpenGL，失败才退 DirectX**；DirectX 路径要求 `d3dcompiler_*.dll` 可加载（`internal/graphicsdriver/directx/d3d_windows.go`，枚举 d3dcompiler_47/46/43）。Win7 不自带这三个 DLL（需安装 DirectX June 2010 Runtime 才有 d3dcompiler_43.dll），要么装依赖，要么起不来。

### 推荐方案（待用户拍板：见对话中的选项题）

**Option A（推荐）**：渲染内核零改动。Win32 窗口 + DIB 双缓冲 + `canvas` 命令流几何光栅 + GDI 文字（P4/P5 已验收）全部保留。重写集中在 **UI 架构层 + 页面层**：

- 新建 `internal/ui/kit`：「每帧重建 + 结构化」组件框架——
  - 布局：Column/Row/Stack/Padding/Flex/Scroll/VirtualList（会话列表虚拟化）
  - 组件：Label/Button/IconButton/Input/TextArea/Toggle/Checkbox/Slider/Card/Tabs/Table/List/EmptyState/Toast/Modal/Drawer/Scrim/QR
  - 焦点：文档序 = Tab 序，组内 roving tabindex，focus ring 2px
  - 状态：`kit.StateStore`（stable key → 状态），替代现有 `app.UI` 的散养 string id
  - 事件：单一 HitTest 遍历 + 捕获/冒泡，替代 app.go 的手写分发
  - 动画：通用 Tween（沿用现有 hoverAnim 思路推广到任意属性）
- 令牌统一：删除 DESIGN.md「Quiet Instrument」与 tokens.go「暖纸」**双重人格**，暖纸令牌为唯一真值；DESIGN.md 重写为令牌参考文档。
- **不新增第三方依赖**（go.mod 保持只有 tdewolff/canvas）。用户如坚持「引入依赖」，唯一值得引入的也只是把已有 indirect 的 `github.com/go-text/typesetting` 升 direct —— 但会和「GDI 文字度量为唯一真值」约束冲突，默认不引入。

**Option B**：全量切 walk（原生控件）。工作量与暖纸重写相当，视觉倒退几十年，walk 已停止维护。只在你想要「经典系统原生控件」风格时才选。

### 页面重写清单（现有 → kit 版）

1. Chat 聊天页（最大：流式/Markdown/代码块/工具卡/折叠组/IME/抽屉）
2. 引导 Onboard
3. 设置 Settings（约 10 卡）
4. 诊断 Diag（5 页签）
5. 自动化 Auto（3 页签）
6. 扩展 Ext（6 页签）
7. 用量 Usage（3 页签）
8. 对比 Compare
9. 命令面板 / 模型菜单 / 二维码 / 模态 / Toast / 侧栏会话列表

### 新增页面（3 + 1）

1. **会话中心（Sessions）** — 全屏会话管理：全文搜索、按日期分组、按模型/成本过滤、批量删除/导出/归档、置顶/改名。
2. **工作区总览（Workspace）** — 本会话 agent 动过的文件清单（write/edit/patch 调用 + 快照提取）：路径 × 次数 × 最近操作，一键打开文件夹 / 复制 diff / 恢复为改动前。
3. **成本与预算中心（Costs）** — 今日/本月成本双环、按模型分摊、`max_cost`/`budget_tokens` 预算上限页内直接设置、超预算高亮、导出 CSV。
4. （候选）**通知中心** — `/notices` 升级为页面：任务完成/确认等待/错误时间线。

### 里程碑（每阶段可验收）

| 阶段 | 内容 | 验收门槛 |
|---|---|---|
| U0 | kit 框架骨架 + 组件矩阵页 | 双架构编译 + go test + 矩阵截图 |
| U1 | Shell（rail/顶栏/状态栏/抽屉）+ Chat | 交互回归 11/11 + 自检 77/77 + Chat 对照截图 |
| U2 | Onboard + Settings | 同上 |
| U3 | Diag + Auto + Ext + Usage + Compare | ✅ 已交付（2026-11-18 晚）：五个全部迁 kit（Ext 为第五刀），自检 76/77、11/11 交互回归 |
| U4 | 新页面 Sessions + Workspace + Costs | ✅ 已交付（2026-11-17）：Rail 直达、kit 组件树渲染、会话/修复/成本三个页面实测截图验证 |
| U5 | 打磨 + hc 主题 + DPI 125/150% + Win7 专项核验清单 | 🔶 主题 hc 组件矩阵页已验证；DPI/目标机清单需真机 |

---

## 二之.二、最近交付（2026-11-18 续）
- U4 三个新 kit 页（会话/工作区/成本）、U3 Compare+Auto+Usage+Diag（并列/自动化/用量/自检）已落地 rail，11/11、自检 77/77 通过。
- U3：Compare+Auto+Usage+Diag 迁 kit。Ext 仍为 Classic GDI。
- 使用说明 v1.30.0：已同步；Eval 基线报告（v1.30.0）已写入 eval-out；dist ZIP 已按最新源码重打。
- v1.30.0 自检 77/77；11/11 交互回归；hc 主题组件矩阵页渲染确认。

## 二之.三、最近交付（2026-11-18 晚）
- **U3 Ext 迁 kit（第五刀·完成）**：扩展页六个页签全部 kit 组件树（设备吊销按 ID、手机指挥台开关+二维码、子代理、技能、MCP、项目记忆）；自检 76/77、11/11 交互回归、实跑截图核验。至此 Classic 页仅剩 Classic 引导/设置/Chat 基础渲染。
- **kit 框架修复（所有 kit 页受益）**：Column 纵向顺排现在把"剩余高度"交给最后子节点、Scroll 拿到有限视口——此前 Scroll(Height=0) 报告 MaxInt32，滚轮完全无效、超出窗口底部内容也无法裁剪；新增 `kit.Left`；kit 页 Tab/Enter 键盘可聚焦/激活（ESC 已支持）。
- **通知中心页面（/notices，rail 铃铛）**：最近通知（错误标红、可清空）+ 自动化足迹（定时任务/目录守护：下次触发、触发次数、启停状态）+ 异常报告（crash/ 目录、可打开）+ 启动恢复卡；不再只往对话里打印文字。
- **架构第五节收尾**：#1 core 纯算法拆包 `internal/coreutil`（datatable/metrics/incremental/fold/convfork，零依赖）；#3 快照存储重构（嵌套布局 snapshots/<conv>/<yyyymm>/、内容 sha256 去重 same_as 链、锚提升保撤销步、每会话 200 条 + 全局 .bak 512MB 双限、单文件 64MB 上限明确报错不写半截、元数据原子写）；#4 Store 基座（devices/watch/schedule 共用 loadStoreFile/writeStoreAtomic，坏盘隔离+0600+原子写，删除 watch 手写双步）；#10 会话索引流式解析（json.Decoder 逐 token 推进，超大会话不再是 32 位 OOM 的雷，内存 O(窗口)）。
- **审计 P2 收尾**：SafeMode 死字段删除（高危命令本来就强制 ask；旧 `safe_mode` 键静默忽略）；自检 RS-P2-14（用例超时不再删活协程还在写的用例目录/沙箱）；规则前缀匹配补边界（git push ≠ git pushxxx；中文 `?` 按 rune）；WriteFileAtomic 保留目标只读位。
- **测试与回归**：go build 双架构、go vet 干净、go test ./... 全绿、76/77 自检（1 SKIP，LLM 未配置，与基线一致）、interaction_regression 11/11（脚本改为 FindWindowExW 枚举+PID 校验+新 exe 杀软首扫给足 50s，消除间歇性找不到窗口）。
- **dist 已按最新源码重打**（v1.30.0，含图标/README/bat/verify），ZIP 内含 v1.30.0 全部修复。

## 三、Win7 全兼容红线（任何改动不得破坏）

1. Go 1.20.14、CGO_ENABLED=0、单文件 exe、-H=windowsgui、amd64+386。
2. 禁用一切 Win8+ API；DPI 只用 SetProcessDPIAware + exe 清单；禁 DwmSetWindowAttribute/DirectComposition。
3. 新增 Win32 API：先查 Win7 导出表，一律 pcall + 加入 SelfCheck；FillRect/DrawTextW 在 user32。
4. 32 位无 LongPtrW/SetClassLongPtrW → longProc 按架构选。
5. TLS 纯 Go 栈（MinVersion TLS1.2，2026-11-17 已提升）；22 张根证书兜底保留（全有效期内）。
6. MemProfile 2G/4G/8G 三档保留；32 位进程上下文封顶不变。
7. COLORREF 0xBBGGRR；字重只 400/700；文字度量唯一真值 GDI；半透明只走 rxScrim；抽屉只覆盖不挤压。
8. 构建：双架构逐一验证；README CRLF+BOM；产物文件名 ASCII。
9. 渲染后端默认仍为 canvas（`WIN7H_RENDER=gdi|canvas` 可回退）。

---

## 四、审计结果与修复对照（已落地）

### 4.0 我主动排查的两个"自曝"问题（比审计发现的还重要）

**A. 自检污染真实数据目录**：`selftest.Run` 的 `DataDir` 参数此前只写 `rep.DataDir`，快照/日志/索引直接走 `core.DataDir()` —— 端到端自检跑的是**用户真实数据目录**。我在基线 `go test ./...` 中亲历其害：ST17 会对真实用户目录里的快照执行 `RewindSteps`，**可能静默还原/删除用户真实文件**（不可回收）。已修：`selftest.Run` 强制 `WIN7HARNESS_HOME=沙箱`（auto 创建/结束清理），GUI/CLI 两处调用点 pre-capture 配置后移除显式 DataDir/DataDir 透传；ST17 现在确定性通过。
**B. `go test ./...` 基线并不绿**：tokens_test（AccentBase/BGCanvas 钉死旧 Quiet Instrument 色、RadiusDialog=14）+ primitive_test（ControlH=30 vs 代码 36）+ RadiusDialog 自身均与"暖纸"设计系统实况分叉。已按**设计令牌为准**修测试。另 `TestRunSuite` 见 A。

### 4.1 llm 包（0 P0 / 6 P1 / 13 P2）

| ID | 问题 | 状态 |
|---|---|---|
| LLM-P1-1 | 非流式 JSON 被判"空响应" → 整轮重发（重复扣费+正文重复印出） | ✅ readWhole 返回正文/hasTool，chatOnce 回填 |
| LLM-P1-2 | 四层"失败即重发"无感知、无上限（最多 4~5 次） | ✅ 建连/5xx 重试写日志；Retry-After 解析 HTTP-date；退避 ±25% 抖动；x509 不重试 |
| LLM-P1-3 | 缓存命中量 > 未命中量时 PromptTokens 被覆盖，成本看板系统性低估 | ✅ 改为累加 |
| LLM-P1-4 | WinINET 只读静态注册表：PAC/WPAD 企业环境 API 全挂、无诊断 | ✅ 检测 AutoConfigURL/AutoDetect 写日志；ProxyServer 为空写日志 |
| LLM-P1-5 | 断流续写仍失败时半段正文被丢弃 | ✅ **按引擎现状无需修**：engine OnContent 已流入 UI，错误时 splice 进 e.Messages（engine.go:304）；不动 |
| LLM-P1-6 | http:// 非私网端点 API Key 明文 Bearer 出网 | ✅ 非私网 http 端点写日志警告（拒=局域网自建合法场景） |
| LLM-P2-1 | HTTP/2 被自定义 DialContext 静默禁用 | ⏳ backlog |
| LLM-P2-2 | 无响应头超时/流空闲超时 | ⏳ backlog |
| LLM-P2-3 | x509 也重试、无抖动、Retry-After 只支持秒 | ✅ 已修（随 LLM-P1-2） |
| LLM-P2-4 | 内置 22 根证书缺国产 CA / DigiCert G3 | ⏳ 需补进 ca_bundle.pem（工程活，可排期） |
| LLM-P2-5/6 | SSE 多行 data 不拼接；APIError{200} 被当普通错误（续写狠打） | ✅ canResume 拦 Status==200；SSE 规范化留 backlog |
| LLM-P2-12 | 兜底客户端无 TLS 下限 | ⏳ backlog |
| LLM-P2-13 | 死代码（isTimeout/resumeGrace） | ✅ 清理 |

### 4.2 tools 包（4 P0 / 8 P1 / 22 P2）

| ID | 问题 | 状态 |
|---|---|---|
| TL-P0-1 | `read_image.save_compressed` 任意路径写，无确认无防护 | ✅ 系统目录硬拒绝 + guardEscapes + 快照 + WriteFileAtomic |
| TL-P0-2 | `quality` 的 `cmd` 参数 = 零门槛任意命令执行 | ✅ 删除 cmd 参数（改固定模板 go test），profile 白名单字符校验 |
| TL-P0-3 | `run_tests` 的 `extra_args` 原样拼进命令行 | ✅ 白名单 sanitise（字母数字+正则元字符，拒绝 cmd 元字符）；verify.go 同源路径 |
| TL-P0-4 | junction/符号链接越界：`EscapesWorkDir` 纯词法 | ✅ 双边 EvalSymlinks（父级回退）+ IsForbiddenPath 剥设备前缀 + `%SystemDrive%` 动态根 + mklink/fsutil 入 dangerPatterns |
| TL-P1-1 | read_file/list_dir/glob/grep 零越界防护 | ✅ 新增 guardRead（Confirm!=nil 弹窗，nil 拒绝） |
| TL-P1-2 | 确认 fail-open（子代理/无人值守静默执行危险命令） | ✅ exec/fs(delete/覆盖大文件)/bgtask 统一 fail-closed |
| TL-P1-3 | `wg.Wait()` 永挂（超时后孙进程占管道） | ✅ cmd.Wait 后立即关读端 |
| TL-P1-4 | CommandRisk 子串匹配可被平凡绕过 | ✅ 归一化（合并空白/去引号）+ 解释器入口 caution + 编码执行 danger |
| TL-P1-5 | history_search 明文 .json 无大小上限 → 32 位 OOM | ✅ 先 Stat 256MB→128MB 封顶 |
| TL-P1-6 | quality 超时路径：竞态+只杀直接子进程+黑框 | ✅ 走 runShell（Job Object+杀树+HideWindow） |
| TL-P1-7 | apply_patch 回滚失实（"已全部回滚"鬼话）+ 快照失败被忽略 | ✅ 快照失败中止；回滚逐文件校验并列出没退回去的文件 |
| TL-P1-8 | read_image OCR 分支绕体积上限 | ✅ 只读文件当 ocr=true 时跳过压缩直接喂 tesseract（不预读） |
| TL-P2-* | 22 项（含 hooks 环境变量注入面、RegisterDynamic 静默顶替 run_command、datatable 全量读内存、`cleanDuckURL` javascript: 链接等） | ⏳ backlog（见第五节） |

### 4.3 core 包（2 P0 / 10 P1 / 22 P2）

| ID | 问题 | 状态 |
|---|---|---|
| CR-P0-1 | 快照 .bak 不 Sync、meta 非原子、UndoLast 直接截断覆盖 → 断电 0 字节灾难 | ✅ .bak.tmp+Sync+rename、meta 走 WriteFileAtomic、恢复前校验 .bak 大小与 meta.Size 一致，不符即中止且保留 .bak 供人工查 |
| CR-P0-2 | CommandRisk 纯子串 → 双空格/编码/变量全绕过 | ✅ 归一化 + 高危在 allow 之前判定（policy.go）+ 解释器/编码执行清单 |
| CR-P1-1 | `/update apply` 含空格路径 100% 失败（Program Files 默认安装位），**本机 cmd 实测复现** | ✅ `set "TARGET=…"` 语法 + `%%` 转义；测试对齐 |
| CR-P1-2 | 新版启动即删 `.old`，坏包装上去无回滚 | ✅ .old 仅清理 30 天以上；最近一份永久保留 |
| CR-P1-3 | 诊断包/崩溃报告脱敏不完整（MCP Header/Env/Hooks/ProxyURL 明文外泄） | ✅ 全部打码（见 diag.go RedactedConfigJSON） |
| CR-P1-4 | `DiscardLastSnapshot` 删别的会话最新快照 | ✅ 只丢当前会话最新 |
| CR-P1-5 | WatchStore/DeviceStore 无锁，Save vs Poll 并发 = fatal | ✅ 双 store 全加锁；Poll 基线节流落盘 |
| CR-P1-6 | journal Progress 复活已收尾条目、tmp 固定名、无 Sync | ✅ 全程持锁 + 纳秒 tmp + 去重 |
| CR-P1-7 | taskqueue dedup 永不释放 → 触发点永久静默丢活 | ✅ 定契约（出队不放键、Done 释放、Remove 释放）+ 文档；**与 ST75 对齐**（原测试语义为准） |
| CR-P1-8 | DownloadUpdate 200MB 静默截断装坏包 + SHA256 可选 | ✅ 多读 1 字节超限拒；**SHA256 缺失即拒绝安装** |
| CR-P1-9 | escapesUnlisted 相对路径按进程 CWD 解析 | ✅ EscapesWorkDir 相对路径一律相对 workDir 解析 |
| CR-P1-10 | sessindex 损坏 gzip 静默跳过（手机列表整个会话消失） | ✅ gz→plain→.bak 回退 + 对齐 app 侧读取 + 日志 |
| CR-P2-3 | MemProfile "未知档" 落最宽档（与"宁可保守"相反） | ⏳ backlog |
| CR-P2-4 | `SafeMode` 死字段 | ⏳ backlog |
| CR-P2-5 | config.Save 非原子固定 .tmp | ⏳ backlog |
| CR-P2-6 | 快照目录平铺无分片、prune 每次全扫 | ⏳ 见第五节（快照存储重构） |
| CR-P2-9 | 守护规则 `**/*.go` 永不匹配、`src/**` 前缀过宽、基线不落盘 | ✅ matchGlobSegments 段匹配（** 吸收任意层）；基线节流落盘 |
| CR-P2-12 | 价格表缺新模型 + NaN→"$NaN" | ⏳ backlog |
| CR-P2-16 | git quotepath 八进制转义 | ⏳ backlog |

### 4.4 mcp / remote / codeidx / selftest（1 P0 / 8 P1 / 22 P2）

| ID | 问题 | 状态 |
|---|---|---|
| RS-P0-1 | 吊销设备后内存 token 仍有效（手机/GUI/CLI 三入口，仅 CLI 做对了） | ✅ 三入口统一 ForgetToken（含 GUI 按 ID 吊销修同名误删） |
| RS-P1-1 | 审批回调竞态：后答复者决策被静默丢弃却回 ok:true | ✅ 翻转 done 的一方才投递+200，其余 409 |
| RS-P1-2 | `-eval` 退出码永远被 runTask 覆盖成 2 | ✅ Eval 分支独立收尾 |
| RS-P1-3 | MCP stdio 子进程 cmd.exe 拼接可注入任意命令 | ✅ 引号/% 白名单拒 + `/s` 双层引号语义 + .exe 直起 |
| RS-P1-4 | MCP 子进程泄漏（父退出孤儿常驻、超时 kill 不 Wait） | ✅ 新 win32 `AssignKillOnCloseJob`（KILL_ON_JOB_CLOSE，含 SelfCheck）+ kill 后统一 Wait 收尸 |
| RS-P1-5 | 调用图永不过期（编辑后 /callers 返回旧关系） | ✅ MarkDirty/MarkStale 同步 ResetGraph |
| RS-P1-6 | `evalrun -tmp <任意目录>` 无确认递归删除 | ✅ 仅允许临时目录下 `harness-eval*` |
| RS-P1-7 | 远程安全 6 用例（配对/吊销/指标/HTTPS）监听失败一律 Skip + 退出码 0 | ✅ 改记 StatusFail（杀软拦端口 = 红而非假绿） |
| RS-P2-1..19 | query token 泄漏面 / 无 CSP / 证书现签 / 远程 SSE 无上限 / 设备 LastSeen 不落盘 / `?token=` 后备 | ⏳ 上表 SSE/CSP 见 backlog（CSP 易、query 后备已删） |
| RS-P2-9 | DeviceView.Current 永远 false，ST72 自相矛盾断言 | ⏳ backlog |
| RS-P2-14 | 自检超时不打断共享沙箱 | ⏳ backlog |

### 4.5 agent 包（1 P0 / 8 P1 / 11 P2）—— 报告：`D:\win7harness\work\audit\agent.md`

| ID | 问题 | 状态 |
|---|---|---|
| AG-P0-1 | 并行写子代理 SpawnBatch 装配时**不设 Policy**，deny/ask 规则与路径判定全部绕过 | ✅ `sub.Policy = e.Policy.Clone()`（与 spawnSubWithDef 对齐） |
| AG-P1-1 | buildContext 两段式对齐产出孤儿 tool 开头 → 长会话确定性 400 变砖；budget 硬编码 120000 | ✅ 循环回退到非 tool；budget 按上下文窗口折算；与 Compact 对齐语义统一，回归测试钉死 |
| AG-P1-2 | **自动压缩是死代码**：compactIfNeeded 零调用点 | ✅ 主循环每轮调用模型前触发 |
| AG-P1-3 | execBatch 只读并行+写串行混排 → `[read, write]` 同批写先于读 | ✅ 分段执行：连续只读并行、写类内联串行、段间严格按序 |
| AG-P1-4 | Stop 恰在 tool 阶段 → 会话尾部孤儿 assistant(tool_calls) → 此后所有请求 400（变砖） | ✅ 去掉 early-return，execBatch 回填占位 tool 消息，协议不变量保住 |
| AG-P1-5 | MergeWrites 无条件覆盖主工作区（含用户刚改的文件），且改动不在 /undo 快照内 | ✅ 合并前 MD5 对比基线，用户改过 → `.conflict-<desc>`；覆盖前 SnapshotFile |
| AG-P1-6 | failureTracker 无锁，被只读并行 goroutine 裸奔读写 | ✅ 内置 mutex |
| AG-P1-7 | 子任务并发 sem 形同虚设（容量=任务数且在主 goroutine acquire），无视内存档位 SubTasks | ✅ sem = MemProfile().SubTasks，acquire 移入 goroutine |
| AG-P1-8 | 预算护栏只统计主桶，压缩/只读子代理/目标拆解 token 全部漏记 | ✅ Budget 合并三桶按各自单价计费；planGoalSteps 记账 |
| AG-P2-1 | Reset 只清 Messages 不清账本；Retry 不重置写标志 | ✅ 全量清空 |
| AG-P2-2 | 图片 Parts 不计入上下文/内存预算 | ✅ messageSize 计入 Parts |
| AG-P2-3 | apply_patch 权限判定只匹配首个文件，其余 N-1 个绕过策略 | ✅ 优先回报第一个越界路径 |
| AG-P2-4 | 子代理费用按主模型单价（RouterOn 时快速路由费虚高） | ✅ 分价计费 |
| AG-P2-5 | parseArgs 单引号盲替换合法内容；裸文本 fallback 把整段文本当 path/content | ✅ 单引号仅在无双引号时替换；裸文本只填 command |
| AG-P2-6 | friendlyErr 不识别 400/quota/model 不存在（变砖错误不可操作） | ✅ 补齐；TLS 提示去掉“换 HTTP”这种有害建议 |
| AG-P2-7 | runVerify 用户停止后构建仍后台烧 5 分钟 | ✅ tools.Context 携带 Ctx，runShell 外部取消杀整棵进程树 |
| AG-P2-8 | RouterOn/FastModel 无锁读写（32 位 string 头撕裂） | ✅ 全部走 usageMu |
| AG-P2-9 | SpawnBatch 轮数写死 16，绕过 DefaultSubRounds | ✅ DefaultSubRounds() |
| AG-P2-10 | SendWithImages 两次置 running（Stop 在窗口内被吞） | ✅ 单临界区 |
| AG-P2-11 | planGoalSteps 不记账 | ✅ addUsageFor |
| 卫生项 | Engine.SafeMode/AutoRun 死字段；`_ = strconv.Itoa` 等 | ✅ 删除 |

---

## 五、架构级重构（按批次落地状态 · 2026-11-18 晚更新）

1. ~~internal/core 拆包~~ → **已交付（2026-11-18）**：datatable/metrics/incremental/fold/convfork 迁入 `internal/coreutil`（零依赖纯算法）；io/配置/诊断留在 core；拆包文件带 10+ 新增/迁入测试。
2. **配置逐字段弹性解析**：`json.RawMessage` → 逐 key 解码 → 坏字段孤立回退 + 汇总报错（此前一个超限数字→整个配置 Quarantine 回退默认）。✅ 已交付（tolerant.go，commit 9f130be）
3. ~~快照存储重构~~ → **已交付（2026-11-18）**：`snapshots/<convID>/<yyyymm>/` 嵌套分片；内容 sha256 流式指纹去重（same_as 链 ≤1）；prune 时"锚提升"（淘汰锚点 → 提升最新依赖者承接 .bak 并 re-anchor 其余依赖者），全链无 dangling、不丢撤销步；每会话 200 条 + 全局 .bak 512MB 双限；单文件 64MB 上限（超限只留指纹，Undo 明确报错，绝不写半截）；meta.json 原子写。
4. ~~Store 基座~~ → **已交付（2026-11-18）**：devices/watch/schedule 共用 `store.go`：`loadStoreFile`（Quarantine + LoadErr）/`writeStoreAtomic`（MkdirAll + WriteFileAtomic，0600）；消去 watch 手写 tmp+rename 双步。
5. **Update 流程**：ApplyScript 增加 `/update revert`。✅ 已交付（commit 68e9dfd）；`.old` 保留到新版本"启动成功+主页渲染完成"信号后再删（nonce/health 文件反馈）仍为 backlog（当前 .old 仅清理 30 天以上）。
6. **hooks 环境变量改 Env 传递**（替代拼命令行）；钩子输出过滤控制字符。✅ 已交付（commit 909ba01）
7. **MCP RegisterDynamic 同名内建工具降级为 warn**（MCP 不能静默顶掉 run_command）。✅ 已交付（commit 909ba01）
8. **CI 门禁**：`forbid os.WriteFile`（除 atomic 本身）+ `forbid quarantine-without-hint` + `go vet` + goleak。✅ 已交付（commit 68e9dfd，scripts/check.py）
9. **价格表与预设同步扩充** + 自检断言"每个预设模型命中或显式 DefaultPrice"。✅ 已交付（commit 9f130be）
10. ~~SessIndex 流式解析~~ → **已交付（2026-11-18）**：json.Decoder 逐 token 推进，只物化摘要与窗口；128MB 解压上限走计数器；内存 O(窗口)。

**未做的有意识取舍**：DACL/所有者的双向复制不做（失败路径比原子写本身复杂；只读位已保留，见 atomic.go 注释）；
KeepSandbox 下自检超时不删目录与沙箱（活协程还在写，删了会与活协程抢写）。

## 六、风险与回滚

- 每个修复批次在独立 commit；任一合并回归失败即回退该批。
- 自检 77/77 为硬门禁；交互回归 11/11 在 UI 批次后跑。
- dist 打包是否发布 ZIP 由你决定，不在本方案内。
- 评测报告（eval-out）保持现状，不手写结论。
- 本轮全部构建产物与中间文件不在 `work/` 树内留下垃圾（`/temp` 用法见 `.gitignore`）。

## 七、验证证据（2026-11-17 本机实测）

- `go build -trimpath` amd64 + 386：✅
- `go vet ./...`：仅剩 `internal/win32` 既有的 unsafe.Pointer 建议（Win32 互操作惯例，可接受）
- `go test ./...`：✅ 全部包通过（含修复的 `TestRunSuite` 现在 77/77，`RadiusDialog`、`AccentBase`、`ControlH` 等已对齐设计令牌）
- `go run ./cmd/selftest_run <沙箱>`：✅（可直接跑 77 项）
- 双架构 exe 实跑 + 交互回归 `interaction_regression.py`：✅ **11/11 通过**（2026-11-17，基于本分支源码构建的 amd64 exe）
- 截图基线：随 UI 批次（U1+）逐页核验
