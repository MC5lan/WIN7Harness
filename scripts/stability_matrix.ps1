param(
  [string]$Exe = (Join-Path $PSScriptRoot '..\dist\WIN7Harness-amd64.exe'),
  [int]$Rounds = 3,
  [string]$DPIList = '1.0,1.25,1.5',
  [string]$OutRoot = ''
)

# v1.40 稳定性矩阵：把 Terminal-Bench 的 avg@N 方法搬到 GUI 回归。
# 每一轮都跑完整的 scripts/ui_regression.ps1（页面导航、长会话、懒加载、
# 返回按钮、卡顿/崩溃检查），同一 DPI 跑 N 次，任何一次 fail 都计入失败率。
# 目的不是“测能不能跑一次”，而是抓偶发 flaky、状态残留和 DPI 相关回归。

$ErrorActionPreference = 'Stop'
if ($OutRoot -eq '') {
  $OutRoot = Join-Path $env:TEMP ('win7h-stability-' + (Get-Date -Format 'yyyyMMdd-HHmmss'))
}
New-Item -ItemType Directory -Path $OutRoot -Force | Out-Null
$results = New-Object System.Collections.ArrayList
$ui = Join-Path $PSScriptRoot 'ui_regression.ps1'
# -File 调用时数组参数会被外层 shell 拆坏，统一用逗号字符串再解析。
$DPIs = @($DPIList.Split(',') | ForEach-Object { [double]::Parse($_.Trim(), [Globalization.CultureInfo]::InvariantCulture) })

foreach ($dpi in $DPIs) {
  for ($r = 1; $r -le $Rounds; $r++) {
    $out = Join-Path $OutRoot (('dpi{0:0.00}-run{1:D2}' -f $dpi, $r))
    New-Item -ItemType Directory -Path $out -Force | Out-Null
    $log = Join-Path $out 'run.log'
    & $ui -Exe $Exe -OutDir $out -DPI $dpi *> $log
    $code = $LASTEXITCODE
    $line = (Get-Content $log | Where-Object { $_ -like 'RESULT *' } | Select-Object -Last 1)
    [void]$results.Add([pscustomobject]@{
      dpi = $dpi; round = $r; exit = $code; result = $line; log = $log
    })
    Write-Host ("[stability] dpi={0} round={1}/{2} exit={3} {4}" -f $dpi, $r, $Rounds, $code, $line)
  }
}

$pass = @($results | Where-Object { $_.exit -eq 0 }).Count
$fail = $results.Count - $pass
$summary = [pscustomobject]@{
  exe = $Exe; rounds = $Rounds; dpis = $DPIs; total = $results.Count
  pass = $pass; fail = $fail; passRate = [Math]::Round(100.0 * $pass / $results.Count, 1)
  out = $OutRoot; runs = $results
}
$utf8 = New-Object System.Text.UTF8Encoding($false)
[IO.File]::WriteAllText((Join-Path $OutRoot 'summary.json'), ($summary | ConvertTo-Json -Depth 8), $utf8)
Write-Host ("SUMMARY total={0} pass={1} fail={2} passRate={3}% out={4}" -f $results.Count, $pass, $fail, $summary.passRate, $OutRoot)
if ($fail -gt 0) { exit 1 }
exit 0
