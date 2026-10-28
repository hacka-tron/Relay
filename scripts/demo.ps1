# End to end demo on Windows. Same steps as demo.sh; everything lands in .\demo.
$ErrorActionPreference = "Stop"
Set-Location (Join-Path $PSScriptRoot "..")
$Demo = Join-Path $PWD "demo"
$Port = 8080
$Bin = "app-windows-amd64.exe"
if (Test-Path $Demo) { Remove-Item -Recurse -Force $Demo }
New-Item -ItemType Directory -Path "$Demo\releases", "$Demo\bin" | Out-Null

function Build($v) { go run ./cmd/release build --version $v --key "$Demo\signing.key" --out "$Demo\releases" --targets windows/amd64 | Out-Null }
function Promote($v) { go run ./cmd/release promote --version $v --releases "$Demo\releases" }
function WaitFor($pattern, $file, $seconds) {
  for ($i = 0; $i -lt $seconds; $i++) {
    if ((Test-Path $file) -and (Select-String -Path $file -Pattern $pattern -Quiet)) { return }
    Start-Sleep 1
  }
  Get-Content $file -Tail 20; throw "timed out waiting for: $pattern"
}

Write-Host "`n== keygen"
go run ./cmd/release keygen --out $Demo

Write-Host "`n== build v1.0.0 and v1.1.0"
Build 1.0.0; Build 1.1.0; Promote 1.0.0

Write-Host "`n== start update server on :$Port"
go build -o "$Demo\updateserver.exe" ./cmd/updateserver
$server = Start-Process -FilePath "$Demo\updateserver.exe" -ArgumentList "-addr 127.0.0.1:$Port -releases $Demo\releases -base-url http://127.0.0.1:$Port" -PassThru -WindowStyle Hidden -RedirectStandardOutput "$Demo\server.log"

try {
  Write-Host "`n== install and start app v1.0.0"
  Copy-Item "$Demo\releases\1.0.0\$Bin" "$Demo\bin\app.exe"
  # On Windows the app restarts as a new process, so track it by name rather than PID.
  Start-Process -FilePath "$Demo\bin\app.exe" -ArgumentList "--server http://127.0.0.1:$Port --interval 5s" -WindowStyle Hidden -RedirectStandardOutput "$Demo\app.log" -RedirectStandardError "$Demo\app.err"
  WaitFor "hello from v1.0.0" "$Demo\app.log" 10

  Write-Host "`n== promote v1.1.0; the app should pick it up within a few seconds"
  Promote 1.1.0
  WaitFor "hello from v1.1.0" "$Demo\app.log" 30
  Get-ChildItem "$Demo\bin"

  Write-Host "`n== publish a tampered v1.2.0"
  Build 1.2.0
  $bytes = [IO.File]::ReadAllBytes("$Demo\releases\1.2.0\$Bin"); $bytes[4096] = 88
  [IO.File]::WriteAllBytes("$Demo\releases\1.2.0\$Bin", $bytes)
  Promote 1.2.0
  WaitFor "sha256 does not match" "$Demo\app.err" 30
  Write-Host "`n== done: app is still running v1.1.0 and refused the tampered build"
} finally {
  Get-Process app -ErrorAction SilentlyContinue | Stop-Process
  Stop-Process -Id $server.Id -ErrorAction SilentlyContinue
}
