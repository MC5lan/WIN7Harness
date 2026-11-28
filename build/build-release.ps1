# WIN7 Harness Windows 一键构建：双架构 exe + Inno Setup 安装包
#
# 用法（在仓库根目录）：
#   powershell -ExecutionPolicy Bypass -File build\build-release.ps1
#   powershell -ExecutionPolicy Bypass -File build\build-release.ps1 -Test
#
# 依赖：
#   - Go 1.20.14（Win7 支持的最后一版）
#   - rsrc.exe（嵌入图标）
#   - Inno Setup 6.7.x（ISCC.exe）
#
# 注意：安装包必须在 Windows 上编译；exe 本身仍可用 build.sh 交叉编译。

[CmdletBinding()]
param(
    [string]$Version = "",
    [string]$Dist = "",
    [string]$Go = "",
    [string]$Rsrc = "",
    [string]$InnoDir = "",
    [switch]$Test,
    [switch]$SkipInstaller
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
$workspace = Split-Path -Parent $root
$top = Split-Path -Parent $workspace

if (-not $Dist) { $Dist = Join-Path $root "dist" }

# ---------- 版本 ----------
if (-not $Version) {
    $cfg = Join-Path $root "internal\core\config.go"
    $m = Select-String -Path $cfg -Pattern '^const Version = "([^"]+)"' | Select-Object -First 1
    if (-not $m) { throw "无法从 internal\core\config.go 读取 Version" }
    $Version = $m.Matches[0].Groups[1].Value
}
Write-Host "=== WIN7 Harness v$Version ===" -ForegroundColor Cyan

# ---------- 工具链 ----------
if (-not $Go) {
    $cmd = Get-Command go.exe -ErrorAction SilentlyContinue
    if ($cmd) { $Go = $cmd.Source }
    elseif (Test-Path (Join-Path $top "_tools\go\bin\go.exe")) {
        $Go = Join-Path $top "_tools\go\bin\go.exe"
    }
}
if (-not $Go -or -not (Test-Path $Go)) { throw "找不到 go.exe；请安装 Go 1.20.14 或传 -Go <路径>" }

$goVersion = & $Go version
if ($goVersion -notmatch 'go1\.20\.') {
    throw "必须用 Go 1.20.x（当前：$goVersion）；Go 1.21+ 的产物无法在 Win7 启动"
}
Write-Host "[0/5] Go: $goVersion"

if (-not $Rsrc) {
    $cmd = Get-Command rsrc.exe -ErrorAction SilentlyContinue
    if ($cmd) { $Rsrc = $cmd.Source }
    elseif (Test-Path (Join-Path $top "_tools\bin\rsrc.exe")) {
        $Rsrc = Join-Path $top "_tools\bin\rsrc.exe"
    }
}
if (-not $Rsrc -or -not (Test-Path $Rsrc)) { throw "找不到 rsrc.exe；请安装 rsrc 或传 -Rsrc <路径>" }

# 环境变量只影响本进程；构建结束不污染系统。
$env:CGO_ENABLED = "0"
$env:GOOS = "windows"
$env:GOPROXY = if ($env:GOPROXY) { $env:GOPROXY } else { "https://goproxy.cn,direct" }
$env:GOSUMDB = "off"

# ---------- 清理输出 ----------
if (Test-Path $Dist) { Remove-Item -Recurse -Force $Dist }
New-Item -ItemType Directory -Force -Path $Dist | Out-Null

# ---------- 测试（可选） ----------
if ($Test) {
    Write-Host "[1/5] go test ./... -count=1 -timeout 15m" -ForegroundColor Yellow
    Push-Location $root
    try {
        & $Go test ./... -count=1 -timeout 15m
        if ($LASTEXITCODE -ne 0) { throw "测试失败，停止构建" }
    } finally {
        Pop-Location
    }
} else {
    Write-Host "[1/5] 跳过测试（发布前请加 -Test）"
}

# ---------- 图标 + 双架构编译 ----------
Write-Host "[2/5] 编译 amd64 / 386" -ForegroundColor Yellow
$buildTime = Get-Date -Format "yyyyMMdd-HHmm"
$ldflags = "-s -w -H=windowsgui -X main.buildTime=$buildTime"
if ($env:UPDATE_PUBKEY) {
    $ldflags += " -X win7harness/internal/core.UpdatePublicKey=$($env:UPDATE_PUBKEY)"
    Write-Host "      更新清单验签：已注入公钥"
} elseif ($env:REQUIRE_SIGNED -eq "1") {
    throw "REQUIRE_SIGNED=1 但未设置 UPDATE_PUBKEY：拒绝构建未验签版本"
} else {
    Write-Host "      !! 未设置 UPDATE_PUBKEY：本次构建跳过更新清单验签（仅开发）"
}

Push-Location $root
try {
    $syso = Join-Path $root "cmd\win7harness\rsrc.syso"
    foreach ($arch in @("amd64", "386")) {
        & $Rsrc -arch $arch -ico (Join-Path $root "build\win7harness.ico") -o $syso
        if ($LASTEXITCODE -ne 0) { throw "rsrc 生成图标资源失败（$arch）" }

        $env:GOARCH = $arch
        $out = Join-Path $Dist "WIN7Harness-$arch.exe"
        & $Go build -trimpath -buildvcs=false -ldflags $ldflags -o $out ./cmd/win7harness
        if ($LASTEXITCODE -ne 0) { throw "go build 失败（$arch）" }
        $size = (Get-Item $out).Length / 1MB
        Write-Host ("      WIN7Harness-{0}.exe  {1:N2} MB" -f $arch, $size)
    }
} finally {
    Remove-Item (Join-Path $root "cmd\win7harness\rsrc.syso") -Force -ErrorAction SilentlyContinue
    Pop-Location
}

# ---------- 附带文件 ----------
Write-Host "[3/5] 附带文件" -ForegroundColor Yellow
Copy-Item (Join-Path $root "build\win7harness.ico") (Join-Path $Dist "win7harness.ico") -Force
Copy-Item (Join-Path $root "build\make-portable.bat") (Join-Path $Dist "make-portable.bat") -Force
# 许可证与归属文件必须随二进制分发（Apache-2.0 §4）。
foreach ($doc in @("LICENSE", "NOTICE", "COMMERCIAL.md", "THIRD_PARTY_NOTICES.md", "README.md")) {
    $src = Join-Path $root $doc
    if (Test-Path $src) { Copy-Item $src (Join-Path $Dist $doc) -Force }
}

[IO.File]::WriteAllText((Join-Path $Dist "version.txt"), "$Version`r`n", [Text.Encoding]::ASCII)

$readmeSrc = Join-Path $root "docs\使用说明.txt"
if (Test-Path $readmeSrc) {
    $text = [IO.File]::ReadAllText($readmeSrc, [Text.Encoding]::UTF8)
    $text = $text -replace "`r?`n", "`r`n"
    [IO.File]::WriteAllText((Join-Path $Dist "README.txt"), $text, (New-Object System.Text.UTF8Encoding -ArgumentList $true))
    Write-Host "      README.txt"
}

$setupBase = [IO.File]::ReadAllText((Join-Path $root "build\setup.bat"), [Text.Encoding]::ASCII)
$setupBase = $setupBase.Replace("__VERSION__", $Version)
[IO.File]::WriteAllText((Join-Path $Dist "setup.bat"), $setupBase, [Text.Encoding]::ASCII)
Write-Host "      setup.bat（旧版备用入口）"

# ---------- 安装包 ----------
if (-not $SkipInstaller) {
    Write-Host "[4/5] Inno Setup 安装包" -ForegroundColor Yellow
    if (-not $InnoDir) {
        $candidates = @(
            (Join-Path $top "_tools\InnoSetup6")
        )
        $pf86 = ${env:ProgramFiles(x86)}
        if ($pf86) { $candidates += (Join-Path $pf86 "Inno Setup 6") }
        if ($env:ProgramFiles) { $candidates += (Join-Path $env:ProgramFiles "Inno Setup 6") }
        foreach ($c in $candidates) {
            if ($c -and (Test-Path (Join-Path $c "ISCC.exe"))) { $InnoDir = $c; break }
        }
    }
    $iscc = if ($InnoDir) { Join-Path $InnoDir "ISCC.exe" } else { $null }
    if ($iscc -and (Test-Path $iscc)) {
        & $iscc "/DMyAppVersion=$Version" "/DMySourceDir=$Dist" "/DMyOutputDir=$Dist" (Join-Path $root "build\installer.iss")
        if ($LASTEXITCODE -ne 0) { throw "ISCC 编译失败" }
    } else {
        Write-Warning "未找到 Inno Setup 6 的 ISCC.exe，跳过安装包。"
        Write-Warning "把 Inno Setup 6.7.x 装到默认位置，或传 -InnoDir <目录>。"
    }
} else {
    Write-Host "[4/5] 跳过安装包（-SkipInstaller）"
}

# ---------- 结果 ----------
Write-Host "[5/5] 产物" -ForegroundColor Yellow
Get-ChildItem $Dist -File | Sort-Object Name | ForEach-Object {
    "{0,-36} {1,10:N2} MB" -f $_.Name, ($_.Length / 1MB)
}
$setupExe = Join-Path $Dist "WIN7Harness-v$Version-Setup.exe"
if (Test-Path $setupExe) {
    $hash = (Get-FileHash $setupExe -Algorithm SHA256).Hash
    Write-Host "SHA256 $hash" -ForegroundColor DarkGray
}
Write-Host "=== 完成 ===" -ForegroundColor Green
