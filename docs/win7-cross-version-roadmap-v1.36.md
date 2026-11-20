# Win7 最低基线、Win10/11 一等公民：v1.36 兼容性路线图

> 目标不是“做一个只能在 Win7 跑的老软件”，而是：
> **同一套源码、同一套二进制，在 Win7 SP1 上完整可用，在 Win10/11 上自动获得更好的体验。**
>
> Win7 是地板，不是天花板。

---

## 一、三条硬规则

1. **源码与构建工具链锁在 Go 1.20.14、CGO_ENABLED=0、GOOS=windows。**
   - Go 1.21+ 的二进制不能在 Win7 启动；不能为了用新语法/新标准库而抬高 go.mod。
   - 依赖准入继续由 `cmd/depguard` 把关：要求 Go>1.20 或强 copyleft 的直接拒绝。
   - 产出 `amd64` 与 `386` 两个 PE，Win7 SP1 ~ Win11 共用。

2. **Win7 是 API 地板：任何 Win8+/Win10+ API 只能“运行时探测 + 降级”，不能直接链接。**
   - 统一走 `internal/win32` 的 `LazyProc` + `pcall`/`SafeCall`，新增导出函数自动进 `SelfCheck`。
   - 需要新 DLL 时先 `Find()`，失败就返回零值/走旧路径；不允许启动即崩。
   - 典型：`SetProcessDpiAwarenessContext`、`GetDpiForWindow`、`GetSystemMetricsForDpi`、`AdjustWindowRectExForDpi`、`DwmSetWindowAttribute`、`WM_DPICHANGED`、`GetSystemTimePreciseAsFileTime`。
   - Win7 没有 `WM_DPICHANGED`（8.1+），继续用 `WM_SETTINGCHANGE`/`WM_DISPLAYCHANGE` 重探系统 DPI。

3. **Win10/11 只做渐进增强，不做“新版本专属功能”。**
   - 新能力必须有 Win7 回退：暗色标题栏失败就保持系统默认；每显示器 DPI 不可用就退回系统 DPI；Toast 不可用就托盘气泡。
   - 不允许 `if win10 { 只走新路径 }` 而没有 `else`。
   - 不允许用 Win10/11 的字体/图标作为唯一资源；Win7 缺字体时必须回退。

---

## 二、需要补的核心层：平台能力探测

新增 `internal/win32/caps.go`（或 `internal/core/platform.go`）统一回答：

```go
type Caps struct {
    OS            string // win7 / win8 / win10 / win11
    Build         int
    Arch          string
    IsWin7        bool
    PerMonitorDPI bool // Win8.1+ / Win10 1703+
    DarkTitleBar  bool // Win10 1809+ / Win11
    Toast         bool // Win10+（可选）
    SnapLayouts   bool // Win11（可选）
    SystemTheme   string // light / dark / high-contrast
    Session       string // console / rdp
}
```

使用原则：

- **启动时探测一次**，结果进诊断页与日志；窗口消息里只做增量更新。
- 功能判断用 `caps.PerMonitorDPI`，不要用 `build >= 7601` 这类魔法数散落各处。
- 所有探测失败都返回“不支持”，走 Win7 路径；探测本身不能 panic。

### Win7 / Win10 / Win11 差异清单

| 能力 | Win7 | Win10 | Win11 | 策略 |
| --- | --- | --- | --- | --- |
| 系统 DPI | `SetProcessDPIAware` + `WM_SETTINGCHANGE` | 每显示器 DPI v1/v2 | 每显示器 DPI v2 | 运行时升级，失败回退 |
| 暗色标题栏 | 无 | 1809+ `DWMWA_USE_IMMERSIVE_DARK_MODE` | 支持 | 失败保持默认 |
| 圆角/云母 | 无 | 无 | Win11 | 可选，不影响布局 |
| Toast | 托盘气泡 | Toast | Toast | 统一走通知抽象层 |
| 任务栏进度 | `ITaskbarList3` | 支持 | 支持 | Win7 起可用 |
| 字体 | Segoe UI / 微软雅黑 | Segoe UI | Segoe UI Variable | 多级回退 |
| 根证书 | 可能过期 | 较新 | 较新 | 系统池 + 内置 CA |
| 高对比 | 支持 | 支持 | 支持 | 统一读 `SPI_GETHIGHCONTRAST` |
| 文件对话框 | 旧式 | 新式 | 新式 | 用系统 API，不自绘 |
| 通知中心 | 无 | 有 | 有 | 可选增强 |

---

## 三、工具链策略：Win7 锁定，Win10/11 用本机

### Win7（本机）

| 工具 | 上限 | 说明 |
| --- | --- | --- |
| Go | 1.20.14 | 最后一个能在 Win7 启动的版本 |
| gopls | v0.15.3 | 最后一个支持 Go 1.20 构建/运行的版本；官方新二进制用 Go 1.21+ 构建，Win7 起不来 |
| Git for Windows | 2.46.2 | 2.47 起依赖 MSYS2/Cygwin 新版，Win7 不支持 |
| Git 32 位 | 2.40.1 | 完整 32 位支持的最后版本 |
| Node.js | 13.x | 14 起有平台检查；13.14.0 是最后可安装包 |
| Python | 3.8.10 | 最后一个带 Win7 安装包的版本 |
| Rust | 1.77.2 | 1.78 起 Tier 1 要求 Win10 |

### Win10/11（本机）

- 优先使用用户已安装的 Go/Git/Node/Python；不强行降级到 Win7 版本。
- 允许 Go 1.21+ 的 `GOTOOLCHAIN` 自动下载，但要有网络/代理失败提示。
- gopls 不设上限，但仍要检查 `gopls version` 是否能启动。
- 工具链选择按“当前 OS + 项目 go.mod”决定，不按应用自身构建版本决定。

### 统一预检

新增 `toolchain` 工具与 `verify/runtests/diagnostics` 的统一前置检查：

- 解析 `go.mod` 的 `go` / `toolchain`，以及 `go.work`。
- Win7 上 `go > 1.20` 或 `toolchain > 1.20`：直接返回“本机无法构建/测试”，不跑 300 秒。
- Win10/11 上 `go` 版本低于项目要求：提示升级或开 `GOTOOLCHAIN=auto`。
- `gopls` 在 Win7 上版本过高：提示锁定版本，不要让 LSP 反复崩溃。
- 结果写进诊断页和启动日志，模型也能通过 `toolchain` 工具主动查询。

---

## 四、P0 / P1 / P2

### P0（不做会在真实 Win7 上出事）

1. **工具链预检**：`go.mod`/`go.work` + Go/gopls/Git 版本矩阵；Win7 不支持的仓库快速失败。
2. **文本编码与换行**：UTF-8 BOM / UTF-16LE/BE / GB18030 / CP1252；保留 BOM、EOL、脚本格式；二进制拒绝。
3. **生命周期**：`WM_QUERYENDSESSION` / `WM_ENDSESSION` / `WM_POWERBROADCAST` / `WM_WTSSESSION_CHANGE`；注销/睡眠/锁屏/RDP 前落盘，恢复后重探网络/代理/DPI。
4. **32 位内存与子进程限额**：`debug.SetMemoryLimit`、GOGC 档位、Job Object 内存/进程数/优先级。
5. **信任链**：DPAPI 存 API Key；更新清单 Ed25519 签名；exe SHA-256 代码签名 + 时间戳；支持包脱敏。

### P1（决定好不好用）

6. **脏矩形与等待式消息循环（v1.36 已落地）**：`InvalidateRect` 脏区、canvas 场景差分、`FlushRect`、`MsgWaitForMultipleObjectsEx`；低色深量化按脏区。
7. **企业网络**：PAC/WPAD、NTLM 代理、子进程代理/CA 注入、VPN split DNS、网络变化通知、CMOS 时间偏差提示。
8. **文件系统（拆分/重试已落地）**：`%APPDATA%` 只留配置/技能/命令，`%LOCALAPPDATA%` 放会话/快照/索引/日志/附件/崩溃报告；首次启动自动迁移旧目录（跨盘复制兜底）。原子写替换文件时对 `ERROR_SHARING_VIOLATION/ERROR_LOCK_VIOLATION/ERROR_ACCESS_DENIED` 做 8 次退避重试，覆盖杀软扫描和共享盘短暂占用。文件工具拒绝 NTFS ADS、`CON/NUL/COM1` 等保留设备名、`\\.\`/`\??\` 设备命名空间和尾随点/空格；写/改/删前检测仅大小写不同的同名文件，避免 Windows 静默覆盖 Linux 仓库里共存的 `README.md`/`readme.md`。长路径交给 Go 1.20 的 `\\?\` 规范化，并有 >360 字符路径回归测试。共享盘 DACL 精细调整仍待补。
9. **Win7 交互（部分落地）**：高对比自动跟随（`SPI_GETHIGHCONTRAST` + `WM_SETTINGCHANGE/WM_THEMECHANGED`）已完成；amd64 上 `WM_GETOBJECT` 返回虚拟 MSAA 树，含对话正文/输入框/发送按钮的名称、角色、值、位置和默认动作（输入框可经辅助工具聚焦，发送/停止用真实鼠标路径触发）；386 回退标准窗口级 IAccessible；启动窗口按鼠标所在显示器工作区居中，`WM_DISPLAYCHANGE` 自动把越界窗口拉回可见区；生成中任务栏显示不确定进度。IME 实机矩阵、字体回退深度验收仍待人工完成。
10. **可支持性**：`GetLastError` 环、原生 minidump、一键支持包、帧/内存/句柄预算。

### P2（长期）

11. **三系统验收矩阵**：Win7 SP1 x86/x64、Win10 22H2、Win11 23H2/24H2；100/125/150/200% DPI；浅色/深色/高对比；中文/英文；非管理员；RDP；睡眠恢复；PAC/NTLM；断网；时间偏差。
12. **能力清单与文档**：`capabilities.json` + 支持矩阵；明确哪些仓库能在 Win7 本机构建，哪些需要 Win10/11。
13. **可选远程 worker**：仅当用户仓库要求 Go 1.21+/Node 18+ 时，把构建/测试/LSP 交给 Win10/Linux；产品本身仍是三系统原生。

---

## 五、验收矩阵（最低要求）

| 维度 | 必测 |
| --- | --- |
| OS | Win7 SP1 x86、Win7 SP1 x64、Win10 22H2、Win11 23H2+ |
| 硬件 | 2GB/双核/HDD/1366×768（参考机）；4GB/SSD/1080p |
| DPI | 100%、125%、150%、200%；Win7 用 `WIN7H_FORCE_DPI` 补 |
| 主题 | 浅色、深色（Win10/11）、高对比（三系统） |
| 输入 | 微软拼音 2010、搜狗、QQ 拼音；中英文混输；全角标点 |
| 显示 | 单屏、双屏、拔插副屏、RDP 断开/重连 |
| 电源 | 睡眠/休眠/合盖/注销/关机；电池与市电 |
| 网络 | 直连、静态代理、PAC、NTLM、VPN split DNS、断网 |
| 权限 | 非管理员、UAC 开/关、Program Files 只读、便携模式 |
| 安全 | 360/MSE/Defender、SmartScreen、未签名/签名包 |
| 文件 | GBK+CRLF、UTF-16LE BOM、UTF-8 BOM、UNC、映射盘、>260 字符、只读/隐藏 |

性能预算（参考机）：

- 冷启动窗口可见 < 2s（HDD）。
- 聊天滚动 p95 paint < 8ms（1366×768，2 核）。
- 空闲 CPU < 0.5%（单核）。
- 稳态内存 < 300MB；386 峰值 < 1.3GB。
- 8 小时 GDI/USER handle 无增长；1000 轮会话不 OOM。

---

## 六、非目标

- 不支持 Win7 RTM；必须 SP1 + KB2533623/KB4474419/KB4490628/KB3140245。
- 不引入 WebView2/Electron/Node/.NET 作为 UI 运行时。
- 不要求管理员权限；不以管理员身份运行 agent。
- 不做 Win7 的每显示器 DPI（系统不支持），只做系统 DPI + 重探。
- 不追求 Win11 圆角/云母等纯装饰效果；布局与可读性优先。
- 不自动安装系统级工具链；缺工具就明确报告。
- 不为现代仓库在 Win7 上“硬凑”构建；超出工具链上限时明确提示或走可选 worker。

---

## 七、近期落地顺序

1. **第 1 周**：`toolchain` 预检 + 文本编码层（本文件配套代码）。
2. **第 2 周**：生命周期消息 + 内存/Job 限额 + 脏矩形/等待式消息循环。
3. **第 3 周**：DPAPI + Ed25519 + 代码签名 + PAC/CA 桥 + 支持包。
4. **第 4 周**：三系统 VM 矩阵跑通，接入 `scripts/ui_regression.ps1`。

---

## 八、本次已落地的配套代码

### 1. 工具链预检

- 新增 `internal/tools/toolchain.go`，提供 `toolchain` 工具与 `CheckToolchain()`。
- 读取 `go.mod` 的 `go` / `toolchain` 与 `go.work` 的 `go`。
- Win7：`go > 1.20` 或工具链要求 >1.20 时直接判定“本机不可构建/测试”，
  不再让 `verify` / `run_tests` / `diagnostics` 跑满 300 秒后把环境问题误判成代码问题。
- Win10/11：优先用本机 Go；项目要求高于本机时，Go 1.21+ 提示
  `GOTOOLCHAIN=auto` 可自动下载，Go 1.20 则明确要求升级。
- Win7 上 gopls > v0.15.3 只告警，不阻断构建（语言服务可降级）。
- 支持 `W7H_GO` 指定 go.exe，适配“Go 不在 PATH”的绿色版/测试环境。

### 2. 文本编码层

- 新增 `internal/tools/textcodec.go`，统一 `read_file` / `write_file` /
  `edit_file` / `apply_patch` 的编码与换行。
- 识别：UTF-8（含 BOM）、UTF-16LE/BE（含 BOM 与 ASCII 特征）、GB18030、
  Windows-1252；二进制文件明确拒绝。
- 保留：原编码、BOM、CRLF/LF；`edit_file` 和 `apply_patch` 用 LF 匹配，
  写回时按原 EOL 还原，所以 GBK+CRLF 老文件只改目标行。
- 新建文件默认：`.bat/.cmd` = GB18030+CRLF；`.ps1` = UTF-8 BOM+CRLF；
  `.reg` = UTF-16LE BOM+CRLF；`.go/.c/.js/...` = UTF-8+LF；
  `.txt/.md/.log` = UTF-8+跟随内容 EOL；不默认加 BOM，避免污染配置与断言。
- 测试覆盖：GBK+CRLF 编辑、UTF-16LE BOM、UTF-8 BOM、CP1252、
  二进制拒绝、中文 `.bat`、补丁保留 GBK+CRLF。

### 3. 系统生命周期

- `WM_QUERYENDSESSION`：只快速落盘（2 秒），不停止队列；用户可能取消关机。
- `WM_ENDSESSION(wParam!=0)`：停止生成、停止会话队列、关 MCP、写干净退出标记。
- `WM_POWERBROADCAST`：挂起前 flush；恢复后重探代理并刷新页面数据。
- `WM_WTSSESSION_CHANGE`：锁屏/解锁、RDP 断开/重连走同一套 Suspend/Resume。
- `SetThreadExecutionState`：长任务且接电时阻止自动睡眠；电池供电不硬撑。
- 窗口销毁时注销 WTS 通知并恢复电源策略。

### 4. 内存与子进程限额

- 启动早期设置 `debug.SetMemoryLimit`：386 约 1.2GB 上限，2GB/4GB 机器按比例收紧；
  amd64 按物理内存 65%，封顶 6GB；`GOMEMLIMIT` 环境变量优先。
- 386 或 2GB 档把 GOGC 收到 70，降低峰值。
- 工具子进程 Job Object 增加：单进程内存上限、Job 总内存上限、活动进程数上限（64）、
  BelowNormal 优先级；设置失败自动退回 KILL_ON_JOB_CLOSE。
- 修复 386 专属问题：Windows 的 `JOBOBJECT_EXTENDED_LIMIT_INFORMATION` 在 32 位上
  要求 `IO_COUNTERS` 8 字节对齐，Go 结构少 4 字节填充会返回 `ERROR_BAD_LENGTH`；
  已按 386/amd64 分文件定义结构。
- 若应用本身已被启动器放进另一个 Job（Win7 不支持嵌套 Job），Assign 会失败；
  此时日志明确提示，并继续用 `taskkill /T` 兜底，不假装限额生效。

### 5. 密钥与更新验签

- `Provider.APIKey` 只在内存保留明文；落盘改为 DPAPI `api_key_enc`（当前用户 scope）。
- 旧明文配置自动迁移；拷到其他 Windows 用户/机器解不开时清空并提示重填。
- 支持包的 `config.redacted.json` 同时清空 `api_key` 与 `api_key_enc`。
- 更新清单新增 Ed25519 `signature`；`UpdatePublicKey` 由 `build.sh` 的
  `UPDATE_PUBKEY` 注入。发布构建强制验签，未注入公钥的开发构建只警告一次。
- `cmd/updatesign` 支持生成密钥与签名清单；私钥文件已加入 `.gitignore`。
- 详细步骤：`docs/update-signing-v1.36.md`。

### 6. 脏矩形与等待式消息循环

- `Canvas.PrepareFrame` 复用 canvas 后端的场景差分，返回本帧真实变化的物理区域；
  `WM_PAINT` 以 `PAINTSTRUCT.RcPaint` 为上限，只在“自失效区域包含 Windows 无效区”
  时才用差分脏区贴屏；窗口被遮挡/激活/改尺寸/DPI 变化时强制整块重贴。
- 自失效区域单独记账（`Window.selfInvalid`），避免把系统补的无效区误当成差分范围。
- GDI 后端仍整块贴屏（没有场景差分），canvas 后端默认只 BitBlt 脏区；
  16bpp 量化也改成按脏区做，不再每次重算整张 DIB。
- 消息循环从 `PeekMessage + Sleep(8ms)` 改为 `MsgWaitForMultipleObjectsEx`：
  有消息立即处理，空闲等下一拍；动画/流式 33fps，静止 10fps。
- 修复了新消息时序暴露的首帧空指针：`Cursor/MouseMove/MouseDown/MouseUp/
  KeyDown/Char/DoubleClick` 在 `a.ui` 建立前直接返回，不再写 wndproc-panic。
- 生命周期消息验收拆到 `scripts/lifecycle_check.ps1`，避免和页面回归的启动
  时序互相影响。

### 8. 企业网络代理与 CA 桥

- `internal/win32/winhttp.go`：WinHTTP `WinHttpGetProxyForUrl` 解析 PAC/WPAD，
  按目标 host 缓存 2 分钟；PAC 返回 DIRECT 时直连；静态代理仍优先。
- `internal/tools/netenv_windows.go`：给 cmd 子进程注入 `HTTP_PROXY/HTTPS_PROXY/NO_PROXY`，
  让 git/go/npm/pip/curl 不再绕过系统代理。
- `internal/win32/certs.go` + `internal/llm/ca*.go`：枚举 Windows `ROOT`/`CA`
  证书库，与内置 CA、用户补充 CA 合并去重，导出 `ca-bundle.pem`；子进程注入
  `SSL_CERT_FILE/GIT_SSL_CAINFO/NODE_EXTRA_CA_CERTS/REQUESTS_CA_BUNDLE/PIP_CERT` 等。
- `internal/netbridge`：本机 127.0.0.1 转发桥，用当前 Windows 用户 SSPI
  凭据完成 407 握手；代理提供 `Negotiate` 时优先 `sspi/negotiate`（Kerberos，
  SPN=`HTTP/<代理 FQDN>`），失败回退 NTLM。LLM 与 git/go/npm/pip/curl 都可
  通过它访问需要认证的上游代理。支持 CONNECT/HTTP、Basic/SOCKS/直连回退、
  按目标 PAC。
- **边界**：纯 Kerberos 需要代理 FQDN、已注册 SPN 和可达 KDC；IP 或跨林
  信任不满足时会回退 NTLM。真实企业代理仍需现场验收。桥只监听 loopback，
  不额外鉴权。
- 详细说明：`docs/win7-enterprise-network-v1.36.md`。

### 9. 验证

**开发机（Windows 11 + Go 1.20.14）**

- `go test ./...` 全绿；`CGO_ENABLED=0 GOOS=windows GOARCH=amd64/386 go build ./...` 均通过。
- 自检 77 项：通过 76，跳过 1（模型连通性），0 失败。

**真实 Win7 SP1 x64 VM（4GB/4CPU，英文系统，OEM 437）**

- 工具链：Go 1.20.14 + Git for Windows 2.46.2（Win7 最后一个支持版本）。
- `go test ./...`（amd64）**全部包 ok**，包括 `internal/tools`（textcodec/toolchain）、
  `internal/core`、`internal/eval`、`internal/selftest`、`internal/evolve`。
- `GOARCH=386 go test ./...` **全部包 ok**（Win7 x64 的 WOW64）。
- `WIN7Harness-amd64.exe -selftest --offline`：75 通过 / 0 失败 / 2 跳过
  （模型连通性、Git 未安装；装 Git 2.46.2 后全量测试中的 Git 用例通过）。
- `WIN7Harness-386.exe -selftest --offline`：75 通过 / 0 失败 / 2 跳过。
- 生命周期消息实测：`WM_QUERYENDSESSION`/`WM_POWERBROADCAST(挂起/恢复)` 后日志出现
  `lifecycle: query-end-session`、`lifecycle: suspend`、`lifecycle: resume`，应用继续运行
  并可正常 WM_CLOSE 退出（host 同会话 PostMessage 验证）。
- 386 Job Object：修复结构对齐后 `SetInformationJobObject` 不再报 `ERROR_BAD_LENGTH`；
  若进程已在别的 Job 中，`AssignProcessToJobObject` 仍会失败，按设计走 taskkill 兜底。
- DPAPI / 更新验签：Win7 x64 上 amd64 与 386 的 `internal/core` 全部通过；
  验证了“落盘无明文 Key、旧明文自动迁移、支持包无密文、签名清单通过、
  篡改版本/缺签名被拒”。
- 注入测试公钥构建的 amd64 包也能通过 `-selftest --offline`（ST58 内部临时清空公钥，
  签名路径由 `internal/core/update_sign_test.go` 覆盖）。
- 渲染/消息循环：host `scripts/ui_regression.ps1` 15/15 通过；
  `scripts/lifecycle_check.ps1` 4/4 通过。
- DPI 矩阵（host，`WIN7H_FORCE_DPI`）：1.0 15/15、1.5 15/15；1.25 有
  `lazy-session-open` 一项受“后台预水合/固定点击坐标”时序影响未触发
  `session-loaded`，其余 14 项通过。该项继续在真机矩阵确认，不当作布局崩溃。
- Win7 启动 + 空闲：无 `wndproc-panic`；`WIN7H_PROFILE=1` 下静止约 12 帧/3 秒，
  其中 11 帧跳过、脏区 0%、全文重绘 0；10 秒空闲 CPU 时间增量约 0。
- 企业网络：Win7 上 `TestSystemRootsPEM`/`TestMergedCABundle`/
  `TestChildNetEnvExportsCABundle` 通过；本地测试 PAC 由 WinHTTP 拉取并解析到
  `127.0.0.1:18080`；CA 合并包约 227KB。
- 代理认证桥：Win7 amd64/386 上用 `sspi/ntlm` 服务端构造的 NTLM 代理完成
  Type1/Type2/Type3 全握手；amd64 上 `sspi/negotiate` 服务端构造的
  Negotiate/SPNEGO 代理也完成握手；直连 HTTP/HTTPS 隧道通过。
- 本地数据拆分：Win7 上 `TestMigrateLocalDataMovesSessions` 通过；
  `%APPDATA%` 旧会话目录首次启动迁到 `%LOCALAPPDATA%`，配置仍留在漫游目录。
- Win7 辅助功能：`TestUTF16PtrToString`、`TestHighContrastQueryDoesNotPanic`
  与 `TestStdAccessibleDesktop` 在 amd64/386 通过；amd64 `TestMSAAObjectMarshals`
  还通过 `LresultFromObject`/`ObjectFromLresult` 实际取回代理，验证了
  中文 BSTR 名称与虚拟子项值（chat/input/button）的 COM 编组。
- `TestAccessibleItemsExposeChatInputAndButton` 验证 App 快照至少包含
  对话正文、输入框、发送按钮三项，且中文正文/输入值完整。
- 子进程环境：`TestMergeEnvWindowsLastWinsCaseInsensitive` 通过；本机桥存在时
  覆盖父进程旧 `HTTPS_PROXY/ALL_PROXY`，避免 NTLM 子进程绕过桥。
- 路径/任务栏/多显示器：`TestValidateWindowsPathSyntax`、`TestCaseInsensitiveCollision`、
  `TestLongPathRoundTrip`（>360 字符）通过；Win7 amd64/386 上
  `TestMonitorWorkAreaAtPrimary`、`TestTaskbarProgressDoesNotPanic` 通过；
  UI 启动窗口按鼠标所在显示器居中，`WM_DISPLAYCHANGE` 会拉回越界窗口。
- 本机回环（localhost/127.0.0.1/::1）不走上游代理，`TestResolveProxyLoopback...`
  在 Windows 上通过。
- 加入 netbridge、本地数据拆分、高对比/MSAA、原子写重试后的最终构建：
  host `go test ./...` 全绿，`ui_regression.ps1` 15/15、`lifecycle_check.ps1` 4/4；
  Win7 amd64/386 `-selftest --offline` 均退出码 0。
- GUI 启动截图：`docs/win7-v136-gui.png`（Win7 Aero 下正常显示 onboarding）。
- 已知边界：英文 Win7 的 OEM 代码页 437 无法表示中文，`cmd echo 中文` 会变成 `?`；
  中文控制台用例在非 936 代码页自动 skip。中文路径应走 `list_dir/read_file`（Go 直接读
  Unicode 文件名），不要用 `dir/where` 试探。这不是解码 bug，是 Win7 控制台代码页限制。

**仍待人工验收**

- Win7 真机/VM 的交互矩阵：125%/150% DPI、多显示器、IME（搜狗/QQ/微软拼音 2010）、
  高对比、RDP 断开重连、睡眠恢复、PAC/NTLM 代理、非管理员、360/MSE。
- 本轮只验证了 GUI 能启动和 selftest 全绿，不等于所有视觉细节已在 Win7 上验收。
- MSAA 富对象目前只在 amd64 启用（对话/输入/发送三项）；386 因 VARIANT
  按值 ABI 的 OLE 代理首轮 BSTR 不稳定，回退窗口级标准 `IAccessible`。
  后面若要把 386 也补齐，需要单独实现/验证 386 的 IMarshal 或改用 UIA。
