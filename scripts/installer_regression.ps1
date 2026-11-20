# WIN7 Harness 安装器回归：标准安装 / 便携安装 / 覆盖升级 / 卸载 / 自检
#
# 用法（仓库根目录）：
#   powershell -ExecutionPolicy Bypass -File scripts\installer_regression.ps1
#   powershell -ExecutionPolicy Bypass -File scripts\installer_regression.ps1 -Keep
#
# 只使用临时目录和 /SKIPLEGACY，不会碰真实的 %LOCALAPPDATA%\Win7Harness 数据。

[CmdletBinding()]
param(
    [string]$Setup = "",
    [string]$Root = "",
    [switch]$Keep
)

$ErrorActionPreference = "Stop"
$repo = Split-Path -Parent $PSScriptRoot
$version = if (Test-Path (Join-Path $repo "dist\version.txt")) {
    (Get-Content (Join-Path $repo "dist\version.txt") -Raw).Trim()
} else {
    $m = Select-String -Path (Join-Path $repo "internal\core\config.go") -Pattern '^const Version = "([^"]+)"' | Select-Object -First 1
    if (-not $m) { throw "读取不到版本号" }
    $m.Matches[0].Groups[1].Value
}
if (-not $Setup) { $Setup = Join-Path $repo "dist\WIN7Harness-v$version-Setup.exe" }
if (-not (Test-Path $Setup)) { throw "找不到安装包：$Setup" }
if (-not $Root) {
    # 便携安装不能落在系统盘 C:；默认用 D:，没有 D: 时退回 TEMP（需手动传 -Root 到非系统盘）
    $base = if (Test-Path "D:\") { "D:\" } else { $env:TEMP }
    $Root = Join-Path $base ("win7h-installer-regression-" + (Get-Date -Format "yyyyMMdd-HHmmss"))
}
New-Item -ItemType Directory -Force -Path $Root | Out-Null

$guid = "{8E4C2F7A-3B1D-4E6A-9C2F-7D5A1B8E3C40}_is1"
$uninstallKey = "HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\$guid"
$failed = 0

function Check($ok, $name) {
    if ($ok) {
        Write-Host "  [通过] $name" -ForegroundColor Green
    } else {
        Write-Host "  [失败] $name" -ForegroundColor Red
        $script:failed++
    }
}

function Invoke-Setup([string]$dir, [string[]]$extra) {
    $setupArgs = @(
        "/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART",
        "/CURRENTUSER", "/SKIPLEGACY", "/DIR=$dir",
        "/TASKS=!desktopicon,!startmenu"
    ) + $extra
    $p = Start-Process -FilePath $Setup -ArgumentList $setupArgs -Wait -PassThru
    if ($p.ExitCode -ne 0) { throw "安装失败 exit=$($p.ExitCode) dir=$dir" }
}

function Invoke-Selftest([string]$exe, [string]$testHome) {
    $old = $env:WIN7HARNESS_HOME
    $env:WIN7HARNESS_HOME = $testHome
    try {
        $p = Start-Process -FilePath $exe -ArgumentList "-selftest", "--offline" -Wait -PassThru -NoNewWindow
        return $p.ExitCode
    } finally {
        if ($null -eq $old) { Remove-Item Env:WIN7HARNESS_HOME -ErrorAction SilentlyContinue }
        else { $env:WIN7HARNESS_HOME = $old }
    }
}

Write-Host "=== 安装器回归 v$version ===" -ForegroundColor Cyan
Write-Host "Setup: $Setup"
Write-Host "临时根: $Root"

try {
    # ---------- 1. 标准 / 当前用户 ----------
    Write-Host "[1/3] 标准安装（当前用户）" -ForegroundColor Yellow
    $std = Join-Path $Root "standard"
    Invoke-Setup $std @()
    Check (Test-Path (Join-Path $std "Win7Harness.exe")) "exe 已安装"
    Check (Test-Path (Join-Path $std "install-mode.txt")) "install-mode.txt 存在"
    Check ((Get-Content (Join-Path $std "install-mode.txt") -Raw).Trim() -eq "standard-user") "install-mode=standard-user"
    Check (-not (Test-Path (Join-Path $std "portable.flag"))) "标准安装没有 portable.flag"
    Check (Test-Path (Join-Path $std "unins000.exe")) "标准安装有卸载器"
    Check (Test-Path $uninstallKey) "HKCU 卸载项存在"

    $code = Invoke-Selftest (Join-Path $std "Win7Harness.exe") (Join-Path $Root "std-home")
    Check ($code -eq 0) "标准安装自检 exit=0（实际 $code）"

    $u = Start-Process -FilePath (Join-Path $std "unins000.exe") -ArgumentList "/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART" -Wait -PassThru
    Check ($u.ExitCode -eq 0) "标准安装卸载 exit=0"
    Check (-not (Test-Path (Join-Path $std "install-mode.txt"))) "卸载后 install-mode.txt 已删"
    Check (-not (Test-Path $uninstallKey)) "卸载后 HKCU 卸载项已删"

    # ---------- 2. 便携 / 覆盖升级 ----------
    Write-Host "[2/3] 便携安装与覆盖升级" -ForegroundColor Yellow
    $port = Join-Path $Root "portable"
    Invoke-Setup $port @("/PORTABLE")
    Check (Test-Path (Join-Path $port "Win7Harness.exe")) "便携 exe 已安装"
    Check (Test-Path (Join-Path $port "portable.flag")) "portable.flag 存在"
    Check (((Get-Item (Join-Path $port "portable.flag")).Length) -eq 0) "portable.flag 是 0 字节"
    Check ((Get-Content (Join-Path $port "install-mode.txt") -Raw).Trim() -eq "portable") "install-mode=portable"
    Check (-not (Test-Path (Join-Path $port "unins000.exe"))) "便携安装没有卸载器"
    Check (-not (Test-Path $uninstallKey)) "便携安装没有 HKCU 卸载项"
    Check (Test-Path (Join-Path $port "data")) "便携 data 目录存在"

    $keepFile = Join-Path $port "data\keep.txt"
    Set-Content -Path $keepFile -Value "keep-me" -Encoding ASCII
    Invoke-Setup $port @("/PORTABLE")
    Check (Test-Path $keepFile) "覆盖升级保留 data\keep.txt"
    Check ((Get-Content $keepFile -Raw).Trim() -eq "keep-me") "覆盖升级数据内容未变"

    # ---------- 3. 版本文件 ----------
    Write-Host "[3/3] 版本与文档" -ForegroundColor Yellow
    $v = (Get-Content (Join-Path $port "version.txt") -Raw).Trim()
    Check ($v -eq $version) "version.txt=$version（实际 $v）"
    Check (Test-Path (Join-Path $port "README.txt")) "README.txt 已安装"
} finally {
    if ($Keep) {
        Write-Host "保留临时目录：$Root" -ForegroundColor DarkGray
    } else {
        if (Test-Path $Root) { Remove-Item -Recurse -Force $Root }
    }
}

Write-Host ""
if ($failed -gt 0) {
    Write-Host "安装器回归失败：$failed 项" -ForegroundColor Red
    exit 1
}
Write-Host "安装器回归通过：标准安装 / 卸载 / 便携 / 覆盖升级" -ForegroundColor Green
