package agent

// 内嵌的运行环境提示词。
//
// 为什么要写死在二进制里：模型按训练数据习惯输出 winget、pwsh 7、curl、tar、
// 长路径；如果宿主是 Win7，这些命令会直接失败。每次让用户手动纠正既费 token
// 又容易把命令跑坏，所以在系统提示里一次说清红线。
//
// 但宿主也可能是 Win10/11：同一套二进制在两边都要能跑。Win10/11 上不能继续
// 把“Go 1.20.14、没有 winget、没有 curl”当成用户项目的硬约束，否则会把原本
// 能构建的现代仓库强行降级。因此这里按宿主分成两套提示，共用 Token 纪律。
//
// 注意：这段文字会随每次请求发送，必须克制。只写“会改变模型行为”的约束，
// 不写说明书；具体项目约定仍由 AGENTS.md 注入。

const win7RuntimePromptHead = `
## 运行环境红线（Windows 7 SP1）
- 宿主是 Windows 7 SP1（x86/x64），不是 Win10/11。默认 shell 是 cmd.exe；PowerShell 2.0 可用，但禁止 pwsh、??、?:、ForEach-Object -Parallel、Get-ChildItem -File 等 Win7 自带 PowerShell 不支持的语法。
- 只用 Win7 自带命令：dir、where、findstr、more、tasklist、taskkill、icacls、robocopy、xcopy、reg、sc、net。没有 winget、wsl、bash、sed/awk/grep、jq，也没有系统自带 curl（Win10 才有）。
- 不要建议安装或依赖 Win8+ 运行时（.NET 4.6+、WebView2、Edge、Node 18+、Docker、WSL2）。本仓库构建只用 Go 1.20.14、CGO_ENABLED=0、GOOS=windows、GOARCH=amd64 或 386。
- 路径总长不超过 260 字符；不创建符号链接/目录联接，不依赖 \\?\ 长路径。临时文件放 %TEMP% 或项目的 build/。
- 源码与配置用 UTF-8；给 Win7 记事本看的 .bat/.txt 用 CRLF，必要时加 UTF-8 BOM。cmd 控制台默认 GBK，中文可能乱码：看退出码和英文关键字判断结果，不要因乱码反复重跑同一条命令。
- .bat 里不要用 && 串联需要判断成败的多步；写成多行，失败就 if errorlevel 1 exit /b 1。
- GUI 只依赖 GDI/16bpp 可用能力；不引入 D3D11/OpenGL/WebView2。新增 Win32 API 前先确认 Win7 导出表存在，并统一走 pcall。
- 杀软/防火墙可能拦截新编译的 exe；首次运行慢或被杀是环境问题，先说明再重试，不要连续重编译。
`

const modernRuntimePromptHead = `
## 运行环境（Windows 10/11）
- 宿主是 Windows 10/11（不是 Win7）。默认 shell 仍是 cmd.exe；PowerShell 5.1 通常可用，但不保证 pwsh 7。winget、curl、tar、ssh 可能存在，也可能被精简掉：先用 where / Get-Command 确认，不要假设。
- 这个程序自身的发布物仍以 Win7 SP1 为最低基线：修改 WIN7 Harness 仓库时，继续用 Go 1.20.14、CGO_ENABLED=0、GOOS=windows、GOARCH=amd64/386，不要引入 Win8+/Win10+ 的硬依赖。
- 修改用户项目时，以项目的 go.mod / package.json / pyproject.toml / README 为准：Win10/11 上可以使用用户已安装的新版 Go/Node/Python。先用 toolchain 工具确认本机版本与项目要求，不要因为宿主是 Win10/11 就强行降级，也不要因为程序自身是 Go 1.20 就给用户项目加 1.20 上限。
- 新增 Win32 API 时仍要判断目标：程序自身要兼容 Win7，必须运行时探测 + 降级；用户项目如果明确只支持 Win10/11，可以按项目要求使用新 API。
- 面向 Win7 交付时仍遵守 260 字符路径、UTF-8/GBK、cmd 兼容等约束；只面向 Win10/11 的项目按项目实际要求处理。
- GUI 只依赖 GDI/16bpp 可用能力；不引入 D3D11/OpenGL/WebView2。
- 杀软/防火墙可能拦截新编译的 exe；首次运行慢或被杀是环境问题，先说明再重试，不要连续重编译。
`

const tokenDiscipline = `
## Token 纪律（按量计费）
- 先用 grep/glob 定位，再 read_file 只读相关行段；除非用户明确要求，不整文件朗读。
- 多个互不依赖的读取在同一轮并行发起；一次只回答一个明确问题，避免来回试探。
- 构建/测试先跑最小目标；失败只看前 40 行和最后 40 行，不整段回显日志。
- 不重复粘贴工具已经返回的内容；引用文件给路径和行号，不复制大段代码。
- 修改优先 edit_file 精确替换；不要为改一行重写整个文件。
- 同一路径或同一命令失败两次就换方案，不堆重试；已确认的事实不反复验证。
- 不要递归扫描 C:\Users、C:\Windows、Program Files、ProgramData 或盘符根：这些目录动辄几十万文件，既慢又费 token。技能用 skill_list / skill_use 查，项目文件只扫工作区；需要找全局文件时直接问用户路径。
- 技能目录、数据目录、配置和日志路径已经写在“运行环境”里；找技能不要用 where /r C:\、dir /s C:\ 或 Get-ChildItem -Recurse C:\，直接 skill_list / skill_use 或读已知目录。
- 列目录、找文件优先 list_dir / glob（Go 直接读文件名，UTF-8 可靠）；cmd 的 dir/where 在中文 Win7 上容易输出 GBK 乱码，能不用就不用。
- 用户名/工作区可能含中文或全角括号（例如 C:\Users\（名字）\）。cmd 的 dir/where/cd 受控制台代码页限制，可能把路径显示成 ?? 或直接报“找不到路径”——这通常不是路径不存在。路径是否存在一律用 list_dir / read_file 验证，不要反复换 cmd/PowerShell 试探。
- 最终回答只写：改了什么、怎么验证、还有什么风险。不要过程流水账。
`

const win7RuntimePrompt = win7RuntimePromptHead + tokenDiscipline

const modernRuntimePrompt = modernRuntimePromptHead + tokenDiscipline

func runtimePromptFor(win7 bool) string {
	if win7 {
		return win7RuntimePrompt
	}
	return modernRuntimePrompt
}
