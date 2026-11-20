param(
  [string]$Exe = (Join-Path $PSScriptRoot '..\dist\WIN7Harness-amd64.exe'),
  [string]$OutDir = '',
  [double]$DPI = 1.0
)

# WIN7 Harness UI regression (PowerShell 5.1 / Win7 compatible, no Python).
# Drives real WM_* messages through the app: page navigation, wheel scrolling,
# long-session virtual scrolling, lazy session opening, kit top-bar back button.
# Fails on missing window, non-responsive process, UI stalls or panic logs.

$ErrorActionPreference = 'Stop'
$Scale = $DPI
$utf8 = New-Object System.Text.UTF8Encoding($false)

Add-Type -AssemblyName System.Drawing
Add-Type @'
using System;
using System.Runtime.InteropServices;
public static class W7 {
  [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr h);
  [DllImport("user32.dll")] public static extern bool SetWindowPos(IntPtr h, IntPtr after, int x, int y, int cx, int cy, uint flags);
  [DllImport("user32.dll")] public static extern bool PostMessage(IntPtr h, uint msg, IntPtr wp, IntPtr lp);
  [DllImport("user32.dll")] public static extern void keybd_event(byte bVk, byte bScan, uint flags, UIntPtr extra);
  [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr h, out RECT r);
  [DllImport("user32.dll")] public static extern bool GetClientRect(IntPtr h, out RECT r);
  [DllImport("user32.dll")] public static extern bool ClientToScreen(IntPtr h, ref POINT p);
  [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr h);
  [StructLayout(LayoutKind.Sequential)] public struct RECT { public int Left, Top, Right, Bottom; }
  [StructLayout(LayoutKind.Sequential)] public struct POINT { public int X, Y; }
}
'@

$script:checks = New-Object System.Collections.ArrayList
function Check([string]$name, [bool]$ok, [string]$detail = '') {
  [void]$script:checks.Add([pscustomobject]@{ name = $name; ok = $ok; detail = $detail })
  if ($ok) { Write-Host "PASS $name $detail" } else { Write-Host "FAIL $name $detail" }
}
function PackPoint([int]$x, [int]$y) { return [IntPtr](($x -band 0xFFFF) -bor (($y -band 0xFFFF) -shl 16)) }
function SX([int]$v) { return [int][Math]::Round($v * $Scale) }
function ClickAt([IntPtr]$h, [int]$x, [int]$y) {
  $px = SX $x; $py = SX $y
  [void][W7]::PostMessage($h, 0x0200, [IntPtr]::Zero, (PackPoint $px $py))
  Start-Sleep -Milliseconds 80
  [void][W7]::PostMessage($h, 0x0201, [IntPtr]1, (PackPoint $px $py))
  Start-Sleep -Milliseconds 50
  [void][W7]::PostMessage($h, 0x0202, [IntPtr]::Zero, (PackPoint $px $py))
  Start-Sleep -Milliseconds 250
}
function MoveAt([IntPtr]$h, [int]$x, [int]$y) {
  [void][W7]::PostMessage($h, 0x0200, [IntPtr]::Zero, (PackPoint (SX $x) (SX $y)))
  Start-Sleep -Milliseconds 250
}
function WheelAt([IntPtr]$h, [int]$x, [int]$y, [int]$notches) {
  $d = -120 * $notches
  if ($d -lt 0) { $d = $d -band 0xFFFF }
  $wp = [IntPtr]($d -shl 16)
  [void][W7]::PostMessage($h, 0x020A, $wp, (PackPoint (SX $x) (SX $y)))
}
function OpenSettings([IntPtr]$h) {
  # Ctrl+,（VK_OEM_COMMA）。用 keybd_event 让 GetKeyState 看到 Ctrl 按下，
  # 只 PostMessage 的话修饰键状态不会更新。
  [void][W7]::SetForegroundWindow($h)
  [W7]::keybd_event(0x11, 0, 0, [UIntPtr]::Zero)
  Start-Sleep -Milliseconds 80
  [void][W7]::PostMessage($h, 0x0100, [IntPtr]0xBC, [IntPtr]0)
  Start-Sleep -Milliseconds 80
  [void][W7]::PostMessage($h, 0x0101, [IntPtr]0xBC, [IntPtr]0)
  [W7]::keybd_event(0x11, 0, 2, [UIntPtr]::Zero)
  Start-Sleep -Milliseconds 700
}
function Shot([IntPtr]$h, [string]$name) {
  $r = New-Object W7+RECT
  [void][W7]::GetWindowRect($h, [ref]$r)
  $w = $r.Right - $r.Left; $hh = $r.Bottom - $r.Top
  if ($w -le 0 -or $hh -le 0) { return }
  $bmp = New-Object System.Drawing.Bitmap $w, $hh
  $g = [System.Drawing.Graphics]::FromImage($bmp)
  $g.CopyFromScreen($r.Left, $r.Top, 0, 0, $bmp.Size)
  $g.Dispose()
  $bmp.Save((Join-Path $shots $name), [System.Drawing.Imaging.ImageFormat]::Png)
  $bmp.Dispose()
}
function LogText([string]$path) {
  if (!(Test-Path $path)) { return '' }
  $fs = [IO.File]::Open($path, [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::ReadWrite)
  try {
    $sr = New-Object IO.StreamReader($fs, [Text.Encoding]::UTF8)
    return $sr.ReadToEnd()
  } finally { $fs.Dispose() }
}

$stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
if ($OutDir -eq '') {
  $OutDir = Join-Path $env:TEMP ("win7h-ui-reg-" + $stamp + "-" + $DPI.ToString('0.00'))
}
$TestHome = Join-Path $OutDir 'home'
$shots = Join-Path $OutDir 'shots'
New-Item -ItemType Directory -Path $TestHome -Force | Out-Null
New-Item -ItemType Directory -Path $shots -Force | Out-Null
$ws = Join-Path $TestHome 'workspace'
New-Item -ItemType Directory -Path $ws -Force | Out-Null
[IO.File]::WriteAllText((Join-Path $ws 'main.go'), "package main`nfunc main() {}`n", $utf8)

# Minimal config: core.Load() merges missing fields with defaults.
$cfg = [ordered]@{
  version = '1.31.1'
  onboarded = $true
  theme = 'light'
  work_dir = $ws
  remote_enabled = $false
  check_update_on_start = $false
}
[IO.File]::WriteAllText((Join-Path $TestHome 'config.json'), ($cfg | ConvertTo-Json -Depth 6), $utf8)
$sess = Join-Path $TestHome 'sessions'
New-Item -ItemType Directory -Path $sess -Force | Out-Null

$now = (Get-Date).ToString('o')
for ($i = 0; $i -lt 60; $i++) {
  $id = '{0:x16}' -f (637100000000000000 + $i)
  $o = @{ v = 1; conv = @{ id = $id; title = ('Conversation ' + $i + ' build log'); at = $now; tok = 10; msgs = @(
    @{ role = 'user'; text = ('Please inspect module ' + $i + ' and fix the build.'); at = $now },
    @{ role = 'assistant'; text = ('Looking at module ' + $i + ' now.'); at = $now }
  ) } }
  [IO.File]::WriteAllText((Join-Path $sess ($id + '.json')), ($o | ConvertTo-Json -Depth 8), $utf8)
}
# Long conversation for virtual scrolling and lazy loading.
$longID = '{0:x16}' -f 637199999999999999
$fence = ([string][char]96) * 3
$msgs = New-Object System.Collections.ArrayList
for ($i = 0; $i -lt 400; $i++) {
  [void]$msgs.Add(@{ role = 'user'; text = ('Check section ' + $i + ' of internal/app/file' + $i + '.go'); at = $now })
  $code = "func step$i(x int) int {`n`treturn x * $i`n}"
  $text = "See code:`n" + $fence + "go`n" + $code + "`n" + $fence + "`n`nConclusion: section $i is fine."
  [void]$msgs.Add(@{ role = 'assistant'; text = $text; at = $now; tools = @(@{ id = ('t' + $i); name = 'read_file'; args = '{"path":"internal/app/file' + $i + '.go"}'; output = 'ok'; done = $true; started = $now }) })
}
$long = @{ v = 1; conv = @{ id = $longID; title = 'Long conversation scroll test'; at = $now; tok = 9999; msgs = $msgs } }
[IO.File]::WriteAllText((Join-Path $sess ($longID + '.json')), ($long | ConvertTo-Json -Depth 12), $utf8)

$env:WIN7HARNESS_HOME = $TestHome
$env:WIN7H_PROFILE = '1'
if ($DPI -ne 1.0) { $env:WIN7H_FORCE_DPI = $DPI.ToString([Globalization.CultureInfo]::InvariantCulture) } else { Remove-Item Env:WIN7H_FORCE_DPI -ErrorAction SilentlyContinue }
$env:WIN7H_NOSNIP = '1'

$p = $null
$hwnd = [IntPtr]::Zero
try {
  $p = Start-Process -FilePath $Exe -PassThru
  for ($i = 0; $i -lt 120; $i++) {
    Start-Sleep -Milliseconds 250
    $proc = Get-Process -Id $p.Id -ErrorAction SilentlyContinue
    if (!$proc) { break }
    if ($proc.MainWindowHandle -ne 0) { $hwnd = [IntPtr]$proc.MainWindowHandle; break }
  }
  Check 'window-visible' ($hwnd -ne [IntPtr]::Zero) ("hwnd=" + $hwnd)
  if ($hwnd -eq [IntPtr]::Zero) { throw 'window not found' }

  # 生命周期消息单独由 scripts/lifecycle_check.ps1 验证：它需要在窗口完成
  # Attach 后同步 SendMessage，混在页面回归里容易受启动时序影响。

  [void][W7]::SetWindowPos($hwnd, [IntPtr](-1), 10, 10, 1280, 720, 0x0040)
  [void][W7]::SetForegroundWindow($hwnd)
  Start-Sleep -Seconds 2
  Shot $hwnd '00-chat.png'
  WheelAt -h $hwnd -x 640 -y 360 -notches 10
  Start-Sleep -Milliseconds 700
  Shot $hwnd '01-chat-scrolled.png'

  $cr = New-Object W7+RECT
  [void][W7]::GetClientRect($hwnd, [ref]$cr)
  $lw = [int][Math]::Round(($cr.Right - $cr.Left) / $Scale)
  $lh = [int][Math]::Round(($cr.Bottom - $cr.Top) / $Scale)

  # 设置入口固定在左下角（v1.32）。启动后 61 个会话还在后台补全，
  # 给 UI 线程留出处理完首批绘制的时间，再点左下角整块设置热区。
  Start-Sleep -Seconds 3
  ClickAt $hwnd 28 ($lh - 100)
  Start-Sleep -Seconds 1
  Shot $hwnd 'page-settings.png'
  $proc = Get-Process -Id $p.Id -ErrorAction SilentlyContinue
  Check 'page-settings' ($proc -and $proc.Responding) ''

  $pages = @(
    @{ name = 'diag'; x = 28; y = 138 },
    @{ name = 'auto'; x = 28; y = 178 },
    @{ name = 'ext'; x = 28; y = 218 },
    @{ name = 'usage'; x = 28; y = 258 },
    @{ name = 'workspace'; x = 28; y = 338 },
    @{ name = 'costs'; x = 28; y = 378 },
    @{ name = 'notices'; x = 28; y = 418 }
  )
  foreach ($pg in $pages) {
    ClickAt $hwnd $pg.x $pg.y
    Start-Sleep -Milliseconds 600
    WheelAt -h $hwnd -x 640 -y 360 -notches 6
    Start-Sleep -Milliseconds 500
    Shot $hwnd ('page-' + $pg.name + '.png')
    $proc = Get-Process -Id $p.Id -ErrorAction SilentlyContinue
    Check ('page-' + $pg.name) ($proc -and $proc.Responding) ''
  }

  # Sessions: list, hover actions, lazy open of an older (unhydrated) session.
  ClickAt $hwnd 28 298
  Start-Sleep -Seconds 2
  MoveAt $hwnd 260 200
  Shot $hwnd 'page-sessions.png'
  WheelAt -h $hwnd -x 640 -y 360 -notches 60
  Start-Sleep -Milliseconds 800
  ClickAt $hwnd 500 330
  Start-Sleep -Seconds 2
  $log = LogText (Join-Path $TestHome 'harness.log')
  if ($log -notmatch 'session-loaded') {
    # DPI 1.25 下虚拟列表的行高/可视行数不同，第一次点到的行可能已在水合缓存里。
    # 再点靠上的未水合行；这不是降低断言，只是让点击目标不依赖固定像素。
    ClickAt $hwnd 500 250
    Start-Sleep -Seconds 2
    $log = LogText (Join-Path $TestHome 'harness.log')
  }
  Shot $hwnd 'lazy-open.png'
  Check 'lazy-session-open' ($log -match 'session-loaded') ''

  # Kit page back button must be handled by classic top bar.
  ClickAt $hwnd 28 298
  Start-Sleep -Milliseconds 800
  ClickAt $hwnd ($lw - 82) 29
  Start-Sleep -Milliseconds 900
  $log = LogText (Join-Path $TestHome 'harness.log')
  Check 'kit-back-button' ($log -match '\[page\] chat') ''

  $log = LogText (Join-Path $TestHome 'harness.log')
  Check 'no-ui-stall' (-not ($log -match 'ui-stall:')) ''
  Check 'no-panic' (-not ($log -match 'PANIC')) ''
  $proc = Get-Process -Id $p.Id -ErrorAction SilentlyContinue
  Check 'process-responsive' ($proc -and $proc.Responding) ''

  [void][W7]::PostMessage($hwnd, 0x0010, [IntPtr]::Zero, [IntPtr]::Zero)
  $exited = $p.WaitForExit(15000)
  Check 'clean-exit' ($exited -and $p.ExitCode -eq 0) ("exit=" + $p.ExitCode)
} catch {
  Check 'exception' $false ($_.Exception.Message + ' @ ' + $_.InvocationInfo.PositionMessage)
} finally {
  if ($p -and !$p.HasExited) { Get-Process -Id $p.Id -ErrorAction SilentlyContinue | Stop-Process -Force }
}

$fail = @($script:checks | Where-Object { !$_.ok }).Count
$summary = [pscustomobject]@{ dpi = $DPI; exe = $Exe; out = $OutDir; pass = ($script:checks.Count - $fail); fail = $fail; checks = $script:checks }
[IO.File]::WriteAllText((Join-Path $OutDir 'report.json'), ($summary | ConvertTo-Json -Depth 6), $utf8)
Write-Host ("RESULT dpi={0} pass={1} fail={2} out={3}" -f $DPI, ($script:checks.Count - $fail), $fail, $OutDir)
if ($fail -gt 0) { exit 1 }
exit 0
