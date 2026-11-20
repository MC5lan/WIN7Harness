# v1.36 企业网络：PAC/WPAD、系统 CA 与子进程环境

Win7 企业环境最常见的问题不是“代码写错”，而是：

- IE/系统配了 PAC/WPAD，程序自己读注册表只看到空的 `ProxyServer`；
- 浏览器能打开内部/外部站点，但 `git clone`、`go mod download`、`npm install`、`pip install` 直连失败；
- 企业 MITM 代理的根证书只装进了 Windows 系统证书库，Node/Python/Git 各自不认。

v1.36 把这三段分别补上。

## 1. PAC / WPAD

`internal/win32/winhttp.go`：

- 读取 `HKCU\...\Internet Settings\AutoConfigURL` / `AutoDetect`；
- 用 WinHTTP `WinHttpGetProxyForUrl` 按目标 URL 解析 PAC/WPAD；
- 结果按 `scheme://host` 缓存 2 分钟；
- PAC 明确返回 DIRECT 时不强行走代理；
- 仍保留静态代理优先：`ProxyEnable=1 + ProxyServer` 存在时直接用静态配置。

应用自己的 LLM HTTP 客户端会按请求解析；`web_fetch` / `web_search` 的
`safeTransport` 也会优先走本机桥；不再出现“检测到 PAC 但不支持”的黑盒失败。

系统代理变化（`WM_SETTINGCHANGE` 的 `WininetSettings`）和睡眠恢复都会
重新读代理并刷新本机桥，不需要重启程序。本机回环目标（localhost/127.0.0.1/::1）
始终直连，不会被误送到公司代理。

## 2. 子进程代理环境

`internal/tools/netenv_windows.go` 在 `cmd.exe` 启动前注入：

- `HTTP_PROXY` / `HTTPS_PROXY`
- `NO_PROXY`（把 WinINET 的 `<local>`、`*.corp` 等转成 Go/Git/npm 认识的逗号列表）

静态代理直接使用；PAC 只解析一个代表目标（默认当前模型 BaseURL），因为
环境变量无法按域名分流。企业 PAC 如果对不同域名返回不同代理，建议：

- 把内部域名加入 `NO_PROXY`；或
- 使用网管提供的统一出口代理；或
- 手动在设置里配置代理。

## 3. 系统 CA 合并包

`internal/win32/certs.go` 枚举 Windows 的 `ROOT` + `CA` 证书库，
和内置 Mozilla 兜底、用户补充证书合并，去重后写到：

```
%LOCALAPPDATA%\WIN7Harness\ca-bundle.pem
```

子进程自动注入：

```
SSL_CERT_FILE
GIT_SSL_CAINFO
NODE_EXTRA_CA_CERTS
REQUESTS_CA_BUNDLE
CURL_CA_BUNDLE
PIP_CERT
AWS_CA_BUNDLE
```

这样 Git/Go/Node/Python/curl 与浏览器使用同一套信任链。

## 4. NTLM / Kerberos：本机 SSPI 转发桥

环境变量本身不能完成 NTLM/Negotiate 握手，所以 v1.36 增加了
`internal/netbridge`：

- 只在检测到环境变量/静态代理/PAC 时启动；
- 监听 `127.0.0.1:<随机端口>`，只接受本机连接；
- 子进程和 LLM 客户端都指向这个本机桥；
- 桥接层按目标解析 PAC/静态代理，用 `github.com/alexbrainman/sspi`
  获取**当前 Windows 登录用户**的 SSPI 凭据：代理提供 `Negotiate` 时优先走
  `sspi/negotiate`（Kerberos，SPN=`HTTP/<代理主机名>`），失败自动回退 NTLM；
- 不保存代理密码，不要求用户输入域账号密码；
- 支持 Basic 代理（URL 里带 userinfo 时）和直连回退；
- 支持 SOCKS5/SOCKS5H 上游（走 `golang.org/x/net/proxy`，不需要 NTLM）；
- CONNECT 与普通 HTTP 都支持；HTTPS 走标准 CONNECT 隧道。

Kerberos/Negotiate：代理同时发 `Negotiate` 和 `NTLM` 时优先选 `Negotiate`；
SPN 用 `HTTP/<代理主机名>`，所以代理配置建议写 FQDN，不要写 IP。KDC 不可达、
SPN 未注册或跨林信任不满足时，SSPI 会回退 NTLM；纯 Kerberos 现场仍建议
用 `klist` / `setspn -L <代理主机>` 先确认票据与 SPN。

安全边界：桥只绑定 loopback，不做额外鉴权；本机同一用户进程本来就能读
环境变量和内存，这是可接受的本地信任模型。

遇到 407 时，工具输出和 LLM 错误都会带明确提示；不要反复重试同一条命令。

## 5. 验收

- `internal/win32/certs_test.go`：系统证书库能导出 PEM。
- `internal/llm/ca_test.go`：合并 CA 包含证书，能原子导出。
- `internal/llm/proxy_windows_test.go`：`NO_PROXY` 转换正确。
- `internal/tools/netenv_windows_test.go`：子进程环境包含 `GIT_SSL_CAINFO` 且文件存在。
- Win7 SP1 实机：
  - `TestSystemRootsPEM` / `TestMergedCABundle` / `TestChildNetEnvExportsCABundle` 通过；
  - 本地测试 PAC 被 WinHTTP 拉取并解析到 `127.0.0.1:18080`；
  - 合并 CA 包约 227KB，子进程环境包含 `GIT_SSL_CAINFO` 且文件存在；
  - `internal/netbridge` 的直连 HTTP/HTTPS 与 **NTLM 代理握手**在 Win7
    amd64/386 上通过（用 `sspi/ntlm` 服务端构造的测试代理验证 Type1/Type2/Type3）；
  - **Negotiate/SPNEGO** 路径在 Win7 amd64 上用 `sspi/negotiate` 服务端
    构造的测试代理通过；
  - 最终 app selftest 双架构退出码 0。

真实代理现场验收（需要当前域用户已有代理权限）：

```bat
set WIN7H_TEST_PROXY=http://proxy.corp.example:8080
go test ./internal/netbridge -run TestLiveProxy -v
```

这条测试走本机桥、真实 CONNECT 到 `https://example.com/`，会实际触发
NTLM 或 Negotiate 握手；代理地址建议写 FQDN，Kerberos SPN 才能匹配。

## 6. 排障命令

```bat
:: 看当前进程的代理/证书环境
set | findstr /i "proxy cert ca_bundle"

:: 验证 Git 是否信任系统 CA
git ls-remote https://github.com/git/git.git HEAD

:: 验证 Go 模块代理
go env GOPROXY
go mod download -x

:: 验证 Node/Python（如果项目用到）
node -e "https.get('https://example.com',r=>console.log(r.statusCode))"
python -c "import requests; print(requests.get('https://example.com').status_code)"
```

如果只有某个工具失败，先看它是否忽略了 `SSL_CERT_FILE`/`GIT_SSL_CAINFO`，
再把 `%LOCALAPPDATA%\WIN7Harness\ca-bundle.pem` 配到该工具自己的 CA 设置里。
