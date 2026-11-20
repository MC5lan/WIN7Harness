param(
  [string]$Exe = (Join-Path $PSScriptRoot '..\dist\WIN7Harness-amd64.exe'),
  [string]$OutDir = ''
)

# 生命周期消息独立验收：WM_QUERYENDSESSION / 挂起 / 恢复。
# 与 ui_regression.ps1 分开，是因为它必须在窗口完成 Attach 后同步 SendMessage，
# 混在页面回归的启动轮询里容易受首帧/会话补全时序影响。

$ErrorActionPreference = 'Stop'
Add-Type @'
using System;
using System.Runtime.InteropServices;
public static class W7Life {
  [DllImport("user32.dll")] public static extern IntPtr SendMessage(IntPtr h, uint msg, IntPtr wp, IntPtr lp);
  [DllImport("user32.dll")] public static extern bool PostMessage(IntPtr h, uint msg, IntPtr wp, IntPtr lp);
  [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr h);
}
'@

function LogText([string]$path) {
  if (!(Test-Path $path)) { return '' }
  $fs = [IO.File]::Open($path, [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::ReadWrite)
  try {
    $sr = New-Object IO.StreamReader($fs, [Text.Encoding]::UTF8)
    return $sr.ReadToEnd()
  } finally { $fs.Dispose() }
}

if ($OutDir -eq '') {
  $OutDir = Join-Path $env:TEMP ('win7h-lifecycle-' + (Get-Date -Format 'yyyyMMdd-HHmmss'))
}
$testHome = Join-Path $OutDir 'home'
New-Item -ItemType Directory -Path $testHome -Force | Out-Null
$logPath = Join-Path $testHome 'harness.log'
$env:WIN7HARNESS_HOME = $testHome
$env:WIN7H_PROFILE = '1'

$pass = 0
$fail = 0
function Check([string]$name, [bool]$ok, [string]$detail = '') {
  if ($ok) { $script:pass++; Write-Output ("PASS " + $name + " " + $detail) }
  else { $script:fail++; Write-Output ("FAIL " + $name + " " + $detail) }
}

$p = $null
try {
  $p = Start-Process -FilePath $Exe -PassThru
  Start-Sleep -Seconds 6
  $proc = Get-Process -Id $p.Id -ErrorAction SilentlyContinue
  if (!$proc -or $proc.MainWindowHandle -eq 0) { throw 'window not found' }
  $h = [IntPtr]$proc.MainWindowHandle
  [void][W7Life]::SetForegroundWindow($h)
  Start-Sleep -Milliseconds 500

  $ret = [W7Life]::SendMessage($h, 0x0011, [IntPtr]::Zero, [IntPtr]::Zero)
  Start-Sleep -Milliseconds 800
  $log = LogText $logPath
  Check 'query-end-session' (($ret -ne [IntPtr]::Zero) -and ($log -match 'lifecycle: query-end-session')) ("ret=" + $ret)

  [void][W7Life]::PostMessage($h, 0x0218, [IntPtr]4, [IntPtr]::Zero)
  Start-Sleep -Milliseconds 800
  $log = LogText $logPath
  Check 'power-suspend' ($log -match 'lifecycle: suspend') ''

  [void][W7Life]::PostMessage($h, 0x0218, [IntPtr]0x12, [IntPtr]::Zero)
  Start-Sleep -Milliseconds 800
  $log = LogText $logPath
  Check 'power-resume' ($log -match 'lifecycle: resume') ''

  [void][W7Life]::PostMessage($h, 0x0010, [IntPtr]::Zero, [IntPtr]::Zero)
  $exited = $p.WaitForExit(15000)
  Check 'clean-exit' ($exited -and $p.ExitCode -eq 0) ("exit=" + $p.ExitCode)
} catch {
  Check 'exception' $false ($_.Exception.Message + ' @ ' + $_.InvocationInfo.PositionMessage)
} finally {
  if ($p -and !$p.HasExited) { Get-Process -Id $p.Id -ErrorAction SilentlyContinue | Stop-Process -Force }
}

Write-Output ("RESULT pass=" + $pass + " fail=" + $fail + " out=" + $OutDir)
if ($fail -gt 0) { exit 1 }
