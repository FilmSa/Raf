param([string]$Python = 'python')
$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
Push-Location $projectRoot
try {
    if (-not (Get-Command go -ErrorAction SilentlyContinue)) { throw 'Install Go 1.24 or newer.' }
    if (-not (Test-Path -LiteralPath '.venv\Scripts\python.exe')) {
        & $Python -m venv .venv
        if ($LASTEXITCODE -ne 0) { throw 'Could not create Python virtual environment.' }
    }
    & '.\.venv\Scripts\python.exe' -m pip install -r face-service/requirements.lock.txt
    if ($LASTEXITCODE -ne 0) { throw 'Could not install Python dependencies.' }
    & '.\.venv\Scripts\python.exe' face-service/download_models.py
    if ($LASTEXITCODE -ne 0) { throw 'Could not download or verify models.' }
    $env:GOCACHE = Join-Path $projectRoot '.gocache'
    go mod download
    if ($LASTEXITCODE -ne 0) { throw 'Could not download Go dependencies.' }
    New-Item -ItemType Directory -Path 'bin' -Force | Out-Null
    go build -trimpath -o bin/server.exe ./cmd/server
    if ($LASTEXITCODE -ne 0) { throw 'Go build failed.' }
    Write-Host 'Ready. Run: .\scripts\start.ps1' -ForegroundColor Green
} finally { Pop-Location }
