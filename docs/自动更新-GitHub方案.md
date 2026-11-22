# 自动更新：GitHub Releases + SHA256（v1.40.1）

## 选型

用户选择「GitHub API + SHA256」而不是签名清单。安全取舍如下：

- GitHub Releases API 只负责**发现**：仓库、最新 tag、资产列表、发布说明；
- `SHA256SUMS.txt` 负责**完整性**：自动下载前必须校验，缺失即 fail-closed；
- 下载后还做 PE 架构头校验（386/amd64 不能混装）；
- **不自动替换正在运行的 exe**：Win7 上文件被锁，外部替换脚本中途失败会留下坏包。
  新包只暂存到 `updates/`，用户重启后用 `/update apply` 安装，失败可 `/update revert` 回滚。

> 风险提示：SHA256 来自同一个 Release，如果仓库本身被攻陷，攻击者可以同时换包和哈希。
> 因此这个模式适合「你自己掌控的仓库 + GitHub 账号安全」的场景；
> 更高安全要求可切回「签名清单」源（Ed25519 公钥随 exe 注入）。

## 配置

`config.json` 新增字段：

```json
{
  "check_update_on_start": true,
  "update_source": "github",
  "update_repo": "MC5lan/WIN7Harness",
  "auto_download_update": true
}
```

- `update_source`：`github`（默认）或 `manifest`。
- `update_repo`：只接受 `owner/repo`（字母/数字/下划线/点/划线），避免 URL 注入。
- `auto_download_update`：只自动下载 + 校验 + 暂存；不会静默覆盖当前程序。

## 界面

设置 → 网络与代理 → 自动更新：

- 更新源：GitHub Releases / 签名清单；
- GitHub 仓库输入框；
- 启动时检查、发现新版本时自动下载两个开关；
- 「立即检查更新」「安装已下载更新」按钮。

发现新版本时通过托盘气泡和通知中心提示；托盘图标现在也保证在 Win7 右下角稳定出现。

## 代码位置

- `internal/core/update_github.go`：Release 发现、仓库校验、SHA256SUMS 解析、
  版本比较、统一的 `CheckUpdateAny`。
- `internal/app/v127.go`：启动检查与后台自动下载/暂存。
- `internal/app/cmd_v129.go`：`/update apply` 走统一更新源。
- `internal/app/settings_page.go`、`pages.go`：设置界面与保存。
- `internal/core/update_github_test.go`：URL 注入、哈希解析、版本比较、fail-closed 单测。
