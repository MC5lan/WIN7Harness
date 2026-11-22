# 发布流程

标签推送后由 GitHub Actions 自动构建并发布。发布链路的三个产物：

- `WIN7Harness-v<ver>-win7.zip`：安装包 + 双架构 exe + 说明文件
- `latest.json`：自更新清单（Ed25519 签名）
- `SHA256SUMS.txt`：所有产物的 SHA256

## 1. 改版本号

只改一处：`internal/core/config.go` 的

```go
const Version = "1.40.0"
```

`build.sh`、Release workflow 与 `-version` 输出都从这一行提取。不要改成
`const Version  = ...` 或分组 const，`sed` 规则会失效。

同时更新 `CHANGELOG.md` 的对应版本条目。

## 2. 本地自检

```bash
GO=/path/to/go1.20.14/bin/go ./build.sh
./dist/WIN7Harness-amd64.exe -selftest --offline
go test ./... -count=1
```

有 Windows 环境时再跑一遍：

```powershell
powershell -ExecutionPolicy Bypass -File scripts\installer_regression.ps1
```

## 3. 打 tag

```bash
git tag v1.40.0
git push origin v1.40.0
```

tag 必须与 `const Version` 完全一致，否则 workflow 第一步就会失败。

## 4. 等待 workflow

`.github/workflows/release.yml` 会：

1. 校验 tag 与源码版本
2. 跑 `cmd/depguard`
3. 在 Windows runner 上安装 `rsrc` 与 Inno Setup 6.7.x
4. 用 `build-release.ps1` 编译双架构 exe + 安装包，并通过 `UPDATE_PUBKEY` 注入验签公钥
5. 用 `UPDATE_PRIVATE_KEY` 生成并签名 `latest.json`
6. 打包完整 zip，生成 `SHA256SUMS.txt`
7. 上传 zip / `latest.json` / `SHA256SUMS.txt` 到 Release

缺少 `UPDATE_PUBKEY` 或 `UPDATE_PRIVATE_KEY` 时构建会**故意失败**，
不要通过删除 `REQUIRE_SIGNED` 或跳过签名来绕过。

## 5. 验证 `/update`

在 Win7/Win10 机器上启动已注入公钥的版本，执行：

```text
/update
```

应能拉取 `latest.json`、通过 Ed25519 验签、下载对应架构并校验 SHA256。
也可以直接检查地址：

```bash
curl -sI https://github.com/MC5lan/WIN7Harness/releases/latest/download/latest.json
```

## 密钥保管与轮换

- `UPDATE_PRIVATE_KEY` 只存放在 GitHub Actions secrets 与离线备份中，绝不提交。
- 本地生成密钥：

  ```bash
  go run ./cmd/updatesign -gen -dir keys
  # keys/update_private.key 私钥，离线保管
  # keys/update_public.key  公钥，注入构建
  ```

- 公钥通过环境变量注入：

  ```bash
  UPDATE_PUBKEY=$(cat keys/update_public.key) ./build.sh
  ```

- 轮换步骤：生成新密钥对 → 更新 `UPDATE_PUBKEY` / `UPDATE_PRIVATE_KEY` secrets →
  用新公钥重新构建并发布一个版本。旧版本无法验证新清单，这是 Ed25519 的预期行为；
  轮换前在 Release notes 里说明“需要先升级到 vX.Y.Z”。
- 私钥只能通过环境变量传给 `tools/make_manifest`（`-key-from-env`），不要写进命令行参数、
  文件或日志。

## 常见问题

| 现象 | 原因 | 处理 |
| --- | --- | --- |
| workflow 在“校验 tag”失败 | tag 与 `const Version` 不一致 | 改版本号或删 tag 重打 |
| workflow 在“构建”失败 | 未配置 `UPDATE_PUBKEY` | 配置 secret；这是有意设计 |
| workflow 在“生成更新清单”失败 | 未配置 `UPDATE_PRIVATE_KEY`，或私钥不是 64 字节 base64 | 配置 secret；用 `cmd/updatesign -gen` 重新生成 |
| `/update` 提示验签失败 | 客户端公钥与清单私钥不匹配，或清单被改 | 用同一对密钥重新构建/发布 |
| `/update` 404 | Release 还没有 `latest.json` asset | 等 workflow 完成，或检查 assets 列表 |
