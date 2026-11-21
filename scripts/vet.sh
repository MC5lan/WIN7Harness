#!/usr/bin/env bash
# 静态检查：unsafeptr 检查按 docs/win32-api-notes.md 第 6 节说明关闭。
# 其余 vet 检查保持开启。
set -euo pipefail
cd "$(dirname "$0")/.."
GO=${GO:-go}
GOOS=windows "$GO" vet -unsafeptr=false ./...
echo "[vet] OK（unsafeptr 检查已按 docs/win32-api-notes.md 说明关闭）"
