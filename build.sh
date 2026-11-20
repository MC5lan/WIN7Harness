#!/usr/bin/env bash
# WIN7 Harness 一键构建：图标 → 双架构交叉编译 → PE 校验 → 打包 ZIP
#
# 依赖：Go 1.20.14（Win7 支持的最后版本）、rsrc、python3
# 用法：
#   ./build.sh              # 全量构建
#   FORCE_ICON=1 ./build.sh # 强制重绘图标
#   OUT_DIR=/path ./build.sh # 指定 ZIP 输出目录
#   UPDATE_PUBKEY=... ./build.sh # 注入更新清单 Ed25519 公钥（正式发布必填）
#   REQUIRE_SIGNED=1 ./build.sh  # 未注入公钥则拒绝构建（发布流水线用）
set -euo pipefail
cd "$(dirname "$0")"

export PATH="/home/marvis/go1.20/go/bin:/home/marvis/go1.20/bin:$PATH"
GO=${GO:-go}
RSRC=${RSRC:-rsrc}
DIST=dist
VERSION=$(sed -n 's/^const Version = "\(.*\)"/\1/p' internal/core/config.go)
echo "=== WIN7 Harness v${VERSION} ==="

# ---------- 0. 前置检查 ----------
gov=$($GO version | awk '{print $3}')
echo "[0/5] Go: $gov"
case "$gov" in
  go1.20*) ;;
  *) echo "!! 必须用 Go 1.20.x，否则产物无法在 Win7 启动"; exit 1 ;;
esac

# 依赖准入：新增依赖若要求 Go>1.20 或带强 copyleft 许可证，直接失败
echo "      依赖准入审查"
GO="$GO" "$GO" run ./cmd/depguard || exit 1

# ---------- 1. 品牌图标 ----------
echo "[1/5] 图标"
if [ ! -f build/win7harness.ico ] || [ "${FORCE_ICON:-0}" = "1" ]; then
  # 纯 Go 生成器：与界面 DrawLogo 同一套珊瑚 >_ 设计，不再依赖 Python/Pillow。
  $GO run ./tools/gen_icon.go
else
  echo "      复用 build/win7harness.ico"
fi
cp -f build/win7harness.ico internal/ui/icon.ico
ls -l build/win7harness.ico | awk '{print "      "$5" bytes"}'

# ---------- 2. 交叉编译 ----------
echo "[2/5] 交叉编译"
rm -rf "$DIST"
mkdir -p "$DIST"
BUILD_TIME=$(date +%Y%m%d-%H%M)
LDFLAGS="-s -w -H=windowsgui -X main.buildTime=$BUILD_TIME"
if [ -n "${UPDATE_PUBKEY:-}" ]; then
  LDFLAGS="$LDFLAGS -X win7harness/internal/core.UpdatePublicKey=$UPDATE_PUBKEY"
  echo "      更新清单验签：已注入公钥"
else
  if [ "${REQUIRE_SIGNED:-0}" = "1" ]; then
    echo "!! REQUIRE_SIGNED=1 但未设置 UPDATE_PUBKEY：拒绝构建未验签版本"
    exit 1
  fi
  echo "      !! 未设置 UPDATE_PUBKEY：本次构建跳过更新清单验签（仅开发）"
fi
for pair in "amd64:WIN7Harness-amd64.exe" "386:WIN7Harness-386.exe"; do
  arch=${pair%%:*}
  out=${pair##*:}
  $RSRC -arch "$arch" -ico build/win7harness.ico -o cmd/win7harness/rsrc.syso
  CGO_ENABLED=0 GOOS=windows GOARCH="$arch" \
    $GO build -trimpath -buildvcs=false \
    -ldflags "$LDFLAGS" \
    -o "$DIST/$out" ./cmd/win7harness
  echo "      $out  $(stat -c%s "$DIST/$out" | awk '{printf "%.2f MB", $1/1048576}')"
done
# rsrc.syso 只对单架构有效，构建后清掉避免污染本地开发
rm -f cmd/win7harness/rsrc.syso

# ---------- 3. 产物校验 ----------
echo "[3/5] 校验 PE 与图标资源"
for f in "$DIST"/*.exe; do
  python3 tools/verify_exe.py "$f"
done

# ---------- 4. 附带文件 ----------
echo "[4/5] 附带文件"
cp -f build/win7harness.ico "$DIST/win7harness.ico"
cp -f build/make-portable.bat "$DIST/make-portable.bat"
printf '%s\r\n' "$VERSION" > "$DIST/version.txt"
printf '      version.txt (%s)\r\n' "$VERSION"
python3 - "$DIST" "$VERSION" <<'PY'
import sys, os
dist, ver = sys.argv[1], sys.argv[2]
raw = open('build/setup.bat', 'rb').read().decode('ascii')
raw = raw.replace('__VERSION__', ver)
open(os.path.join(dist, 'setup.bat'), 'wb').write(raw.encode('ascii'))
print('      setup.bat (%d bytes, CRLF, ASCII)' % os.path.getsize(os.path.join(dist, 'setup.bat')))
PY
python3 - "$DIST" <<'PY'
import sys, os
dist = sys.argv[1]
src = os.path.join('docs', '使用说明.txt')
raw = open(src, encoding='utf-8-sig').read()  # 说明文档本身带 BOM，这里先剥掉避免重复
# Win7 记事本对无 BOM 的 UTF-8 会按 ANSI 解码导致乱码，这里补 BOM。
open(os.path.join(dist, 'README.txt'), 'wb').write(b'\xef\xbb\xbf' + raw.replace('\n', '\r\n').encode('utf-8'))
print('      README.txt (%d bytes, UTF-8 BOM + CRLF)' % os.path.getsize(os.path.join(dist, 'README.txt')))
PY

# ---------- 5. 打包 ----------
echo "[5/5] 打包"
OUT_DIR=${OUT_DIR:-$DIST}
python3 - "$DIST" "$OUT_DIR" "$VERSION" <<'PY'
import sys, os, zipfile
dist, out_dir, ver = sys.argv[1], sys.argv[2], sys.argv[3]
os.makedirs(out_dir, exist_ok=True)
zp = os.path.join(out_dir, 'WIN7Harness-v%s-win7.zip' % ver)
files = ['WIN7Harness-amd64.exe', 'WIN7Harness-386.exe', 'win7harness.ico', 'README.txt', 'make-portable.bat', 'setup.bat', 'version.txt']
with zipfile.ZipFile(zp, 'w', zipfile.ZIP_DEFLATED, compresslevel=9) as z:
    for f in files:
        p = os.path.join(dist, f)
        # 全部用 ASCII 文件名，避免 Win7 自带解压对 UTF-8 文件名的兼容问题
        z.write(p, arcname=f)
print('      %s  %.2f MB' % (zp, os.path.getsize(zp) / 1048576))
PY

# ---------- 6. 安装包（可选） ----------
if [ -n "${ISCC:-}" ] && [ -f "$ISCC" ]; then
  echo "[安装包] $ISCC"
  "$ISCC" "/DMyAppVersion=$VERSION" "/DMySourceDir=$DIST" "/DMyOutputDir=$DIST" build/installer.iss
else
  echo "[安装包] 未设置 ISCC，跳过（Windows 上运行 build/build-release.ps1）"
fi

echo "=== 完成 ==="
