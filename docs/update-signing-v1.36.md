# v1.36 密钥落盘与更新清单签名

## 1. API Key：DPAPI 加密落盘

`config.json` 不再保存明文 `api_key`：

- 内存里仍是明文，供模型客户端使用；
- 写盘时用 Windows DPAPI（`CryptProtectData`，当前用户 scope）加密，
  写成 `api_key_enc: "dpapi:v1:<base64>"`；
- 旧配置里的明文 `api_key` 仍可加载，下一次保存会自动迁移成密文；
- 把配置拷到另一个 Windows 用户/机器上，DPAPI 解不开，程序会清空该 Key 并提示重填；
- 支持包里的 `config.redacted.json` 同时清空 `api_key` 与 `api_key_enc`。

注意：DPAPI 保护的是“配置被同步盘/备份/误发”这类场景，不防本机当前用户下的
恶意进程（同用户本来就能读内存/调试进程）。

## 2. 更新清单：Ed25519 签名

清单新增：

```json
{
  "version": "1.36.0",
  "notes": "…",
  "files": {
    "amd64": {"url": "…", "sha256": "…", "size": 123},
    "386":   {"url": "…", "sha256": "…", "size": 123}
  },
  "signature": "base64(Ed25519(canonical payload))"
}
```

被签名的 canonical payload 只包含：

- `version`
- `notes`
- `page_url`
- `published_at`
- `files`（按架构 key 排序；每个文件的 `url` / `sha256` / `size`）

`signature` 本身不参与签名。JSON 序列化由 Go 的 `encoding/json` 完成，
map key 自动排序，因此发布端与客户端字节一致。

### 生成密钥

```bash
go run ./cmd/updatesign -gen -dir keys
```

产物：

- `keys/update_private.key`：私钥，**离线备份，绝不提交**（已加入 .gitignore）
- `keys/update_public.key`：公钥，构建时注入

### 给清单签名

```bash
go run ./cmd/updatesign \
  -key keys/update_private.key \
  -in latest.json \
  -out latest.json
```

签名后再改 `version`、`notes`、`files`、`sha256` 任一字段，验签都会失败。
发布流程必须先改清单、最后签名。

### 构建时注入公钥

```bash
UPDATE_PUBKEY=$(cat keys/update_public.key) ./build.sh
# 发布流水线建议再加一道硬门：
REQUIRE_SIGNED=1 UPDATE_PUBKEY=$(cat keys/update_public.key) ./build.sh
```

- 注入了公钥：`CheckUpdate` 强制验签；清单缺 `signature` 或签名不匹配直接拒绝。
- 未注入公钥：仅开发构建，跳过验签并在日志里写一次：
  `update: 未配置 UpdatePublicKey，跳过清单验签（仅开发构建；正式发布请注入公钥）`。
- 公钥通过 `-ldflags -X win7harness/internal/core.UpdatePublicKey=...` 写入二进制；
  换密钥必须重新发布客户端，不能让清单自己带一个“信任哪个公钥”的字段。

### 下载链路

- 清单验签通过后，`sha256` 才被视为可信；
- `DownloadUpdate` 继续校验文件 SHA256，失败删除半成品；
- 架构不匹配不会“善意降级”下载另一个架构；
- 不在运行时自动替换正在执行的 exe，仍由用户决定何时替换。

## 3. 验证

- `internal/core/config_secret_test.go`：
  - 保存后 config.json 不含明文 Key；
  - 旧明文配置保存后迁移；
  - 支持包不含密文/明文。
- `internal/core/update_sign_test.go`：
  - 正确签名可更新；
  - 篡改版本被拒；
  - 配了公钥但清单缺签名被拒；
  - 非法公钥被拒。
- Win7 SP1 VM 上 `go test ./internal/core` 与 386/amd64 全量测试通过后，
  再用真实 DPAPI 路径复验一次保存/加载。
