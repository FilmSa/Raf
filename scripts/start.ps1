param([switch]$Lan)
$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
$pythonExe = Join-Path $projectRoot '.venv\Scripts\python.exe'
$serverExe = Join-Path $projectRoot 'bin\server.exe'
if (-not (Test-Path -LiteralPath $pythonExe) -or -not (Test-Path -LiteralPath $serverExe)) { throw 'Run scripts/setup.ps1 first.' }
Push-Location $projectRoot
$faceProcess = $null
$originalAddr = $env:APP_ADDR
try {
    if ($Lan) { $env:APP_ADDR = '0.0.0.0:8080' }
    New-Item -ItemType Directory -Path '.runtime' -Force | Out-Null
    if (-not $env:FACE_SERVICE_URL -or $env:FACE_SERVICE_URL.TrimEnd('/') -eq 'http://127.0.0.1:8001') {
        $portProbe = New-Object System.Net.Sockets.TcpClient
        try {
            $portProbe.Connect('127.0.0.1',8001)
            throw 'Port 8001 is already in use. Stop the other service or set FACE_SERVICE_URL.'
        } catch [System.Net.Sockets.SocketException] {
            # No listener: this script can own the private encoder.
        } finally { $portProbe.Dispose() }
        $faceProcess = Start-Process -FilePath $pythonExe -ArgumentList @('-m','uvicorn','app:app','--app-dir','"face-service"','--host','127.0.0.1','--port','8001','--no-access-log') -WorkingDirectory $projectRoot -WindowStyle Hidden -PassThru -RedirectStandardOutput '.runtime/face.out.log' -RedirectStandardError '.runtime/face.err.log'
        $ready = $false
        for ($attempt = 0; $attempt -lt 40; $attempt++) {
            if ($faceProcess.HasExited) { throw 'Face service stopped. See .runtime/face.err.log.' }
            try { Invoke-RestMethod 'http://127.0.0.1:8001/health' -TimeoutSec 1 | Out-Null; $ready = $true; break } catch { Start-Sleep -Milliseconds 500 }
        }
        if (-not $ready) { throw 'Face service did not become ready. See .runtime/face.err.log.' }
    }
    Write-Host 'Open http://localhost:8080. Press Ctrl+C to stop.' -ForegroundColor Green
    & $serverExe
    if ($LASTEXITCODE -ne 0) { throw 'Server stopped with an error.' }
} finally {
    if ($faceProcess -and -not $faceProcess.HasExited) { & taskkill.exe /PID $faceProcess.Id /T /F | Out-Null }
    $env:APP_ADDR = $originalAddr
    Pop-Location
}
