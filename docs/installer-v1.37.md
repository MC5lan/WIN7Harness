# v1.37.0 安装器、便携模式与 U 盘密码

> v1.37.1 补丁：OpenCode Go 请求补 `x-opencode-session` 与真实 User-Agent，
> 修复四个模型全部 400（MissingSessionID）；400/401/403/404/429 附带
> 上游 message；系统模态对话框期间暂停 UI 看门狗，避免误报 ui-stall。
>
> v1.38.0：新增 `/doctor` 端点诊断、真实对话测试、Paint→Timer 动作队列、
> 安装形态缓存、工作区目录安全校验；新增
> `scripts/installer_regression.ps1`，安装器回归已自动跑通。
>
> v1.38.1：输入框文字对比度兜底；流式收尾按本帧 contentH 夹紧滚动并强制
> 整帧重绘，修复结束时发白/跳到顶部/需要滚轮才恢复。
>
> 安装器与文件布局不变。

本文给维护者看。终端用户说明在 `docs/使用说明.txt`。

## 1. 产物

`build/build-release.ps1` 生成：

```
dist/
  WIN7Harness-amd64.exe
  WIN7Harness-386.exe
  win7harness.ico
  README.txt
  version.txt
  make-portable.bat
  setup.bat                     # 旧版备用入口，不是主安装器
  WIN7Harness-v1.37.0-Setup.exe # 主安装包（Inno Setup 6.7.x）
```

安装包同时包含 amd64 / 386，按系统位数只落一个 `Win7Harness.exe`。

## 2. 构建

Windows（推荐，安装包必须在这里编译）：

```powershell
powershell -ExecutionPolicy Bypass -File build\build-release.ps1 -Test
```

依赖：

- Go 1.20.14（不能用 1.21+，产物无法在 Win7 启动）
- `rsrc.exe`（嵌图标）
- Inno Setup **6.7.x**（`ISCC.exe`）；7.x 不要用于 Win7 发布包
- 脚本会自动找 `_tools\go\bin\go.exe`、`_tools\bin\rsrc.exe`、
  `_tools\InnoSetup6\ISCC.exe`，也可用 `-Go` / `-Rsrc` / `-InnoDir` 指定

Linux 交叉编译 + 可选安装包：

```bash
ISCC=/path/to/ISCC ./build.sh
```

## 3. 安装器流程

1. Inno 自带“安装范围”对话框：当前用户 / 所有用户。
   - 当前用户：`%LOCALAPPDATA%\Programs\Win7Harness`，不弹 UAC。
   - 所有用户：`C:\Program Files\Win7Harness`，UAC，公共快捷方式。
2. 自定义页“选择安装方式”：标准 / 便携。
3. 标准目录页确认位置；便携模式先列出盘符、卷标、格式、剩余空间，
   再预填到目录页。
4. 标准模式显示快捷方式任务；便携模式跳过任务页。

关键实现：

- `AppId` 固定为 `{8E4C2F7A-3B1D-4E6A-9C2F-7D5A1B8E3C40}`，**不要改**；
  改了会被当成另一个产品，升级/卸载不连续。
- `Uninstallable=not IsPortableMode`：便携模式不生成卸载器。
- `CreateUninstallRegKey=not IsPortableMode`：便携模式不写“程序和功能”。
- `[Dirs] Permissions: users-modify`：NTFS U 盘换电脑时其他用户仍可写。
- `SaveStringToFile(..., '', False)` 创建 0 字节 `portable.flag`；
  已验证生成的是空文件。
- `CurStepChanged(ssPostInstall)` 写：
  - 标准：`install-mode.txt` = `standard-user` / `standard-all`
  - 便携：`install-mode.txt` = `portable`，`portable.flag`，`data/`
- 标准安装会清理旧 `setup.bat` 的程序文件（只删 exe/ico/README/
  uninstall.bat/shortcuts.txt），保留 `sessions`、日志和配置。
- 旧版程序正在运行时，`PrepareToInstall` 会中止并提示先退出。

测试用命令行：

```bat
:: 当前用户标准安装
WIN7Harness-v1.37.0-Setup.exe /VERYSILENT /SUPPRESSMSGBOXES /NORESTART ^
  /CURRENTUSER /SKIPLEGACY /DIR=D:\tmp\wh-user /TASKS=!desktopicon,!startmenu

:: 便携安装
WIN7Harness-v1.37.0-Setup.exe /VERYSILENT /SUPPRESSMSGBOXES /NORESTART ^
  /CURRENTUSER /SKIPLEGACY /PORTABLE /DIR=E:\WIN7Harness
```

`/SKIPLEGACY` 只用于自动化测试，避免碰到真实 `%LOCALAPPDATA%\Win7Harness`。

## 4. 便携模式 U 盘密码

- 字段：`config.json` 的 `portable_secrets`。
- 格式：`pwd:v1:<iter>:<salt-b64>:<nonce-b64>:<ciphertext-b64>`。
- KDF：PBKDF2-HMAC-SHA256，150000 次；只用标准库，不引入 `x/crypto`。
- 加密：AES-256-GCM；salt 16B，nonce 12B，key 32B。
- 明文是 `{"v":1,"keys":{"provider-id":"sk-..."}}`。
- 密码不落盘；每次启动输入一次。点“稍后”可以看会话，但模型调用前要解锁。
- 忘了密码：解锁框“忘记密码”清空密文，重新填 Key。
- 支持包 `config.redacted.json` 会清空 `portable_secrets`。
- 标准模式继续用 DPAPI；两种密文互不覆盖。
- 安全边界：只保护 API Key，不保护会话/代码。整盘防丢建议 BitLocker To Go
  或 VeraCrypt。

## 5. 所有用户安装与更新

- 程序在 Program Files，普通用户不可写。
- `cmdUpdateApply` 在下载前调用 `core.InstallDirWritable()`；
  不可写时提示“请运行新版安装包升级”，不尝试替换 exe。
- 当前用户/便携安装仍可应用内替换。
- 所有用户卸载需要管理员；数据在各用户自己的目录，卸载不删。

## 6. 本次验证记录（2026-11-20，Windows 11 构建机）

已验证：

- `go test ./...` 全绿。
- `go build ./...`（amd64 / 386）通过。
- v1.37.0 amd64 与 386 `-selftest --offline` 均 76 通过 / 1 跳过。
- 标准/当前用户静默安装：文件、`install-mode.txt=standard-user`、
  卸载器、HKCU 卸载项正确；卸载后 `install-mode.txt` 被删除，数据目录未动。
- 便携静默安装：`portable.flag`（0 字节）、`install-mode.txt=portable`、
  `data/` 正确；**没有**卸载器，**没有** HKCU 卸载项。
- 安装包 `/DIR` 参数被正确尊重（模式页不会覆盖命令行目录）。
- 便携 GUI：启动弹出“解锁 U 盘密钥”，密码框始终遮罩；
  粘贴正确密码后 Toast “API Key 已解锁”，进入主界面。
- 便携 GUI：设置页填 Key 并保存，弹出“设置 U 盘密码”；
  两次输入后配置写入 `portable_secrets`，无明文 Key。
- NTFS 便携目录 ACL 含 `BUILTIN\Users: Modify`。
- 旧的 `%LOCALAPPDATA%\Win7Harness\Win7Harness.exe` 未被测试安装器触碰。

未验证（需要真机/VM 补）：

- Win7 SP1 x86 / x64 实机安装、U 盘热插拔/换盘符。
- “所有用户”UAC 流程、Program Files 安装、公共快捷方式、卸载。
- 旧 `setup.bat` 实际迁移（为避免删除用户真实旧 exe，只做了代码审查和
  `/SKIPLEGACY` 测试）。
- Win7 125% / 150% DPI 下的安装器自定义页；坐标已改用
  `ScaleX` / `ScaleY`，但仍需真机目测。
- 代码签名、SmartScreen/杀软兼容性。

## 7. 发布检查单

1. `build-release.ps1 -Test` 通过。
2. `powershell -File scripts\installer_regression.ps1` 通过。
3. Win7 SP1 x86/x64 各装一次标准/当前用户、便携；跑 `-selftest --offline`。
4. 至少一台机器验证“所有用户”安装 + 卸载。
5. 从旧 `setup.bat` 安装升级一次，确认会话/配置保留、旧快捷方式/卸载项消失。
6. U 盘从 E: 换到 F:，确认配置/会话仍可读；API Key 需重新输入密码。
7. 计算 Setup.exe SHA256 并随发布说明给出。
8. 若要公开分发，考虑代码签名；Inno Setup 商业使用按官网建议购买授权。
