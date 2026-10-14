# EthioScript & Voice AI — Docker startup script
# Run this from PowerShell: .\start.ps1
# Or double-click from File Explorer after right-clicking → "Run with PowerShell"

$ErrorActionPreference = "Continue"
$root = $PSScriptRoot
Set-Location $root

Write-Host ""
Write-Host "═══════════════════════════════════════════════════════" -ForegroundColor Cyan
Write-Host "  EthioScript & Voice AI — Starting Docker Stack" -ForegroundColor Cyan
Write-Host "═══════════════════════════════════════════════════════" -ForegroundColor Cyan
Write-Host ""

# Step 1: Validate
Write-Host "[1/5] Validating compose file..." -ForegroundColor Yellow
docker compose config --quiet
if ($LASTEXITCODE -ne 0) {
    Write-Host "ERROR: docker-compose.yml is invalid" -ForegroundColor Red
    exit 1
}
Write-Host "  ✓ compose file valid" -ForegroundColor Green

# Step 2: Pull all infrastructure images (no build)
Write-Host ""
Write-Host "[2/5] Pulling infrastructure images (this may take several minutes)..." -ForegroundColor Yellow

$infraImages = @(
    "postgres:16-alpine",
    "redis:7-alpine",
    "rabbitmq:3.13-management-alpine",
    "minio/minio:latest",
    "bitnami/minio-client:latest",
    "nginx:alpine",
    "prom/prometheus:latest",
    "grafana/grafana:latest",
    "dpage/pgadmin4:latest"
)

foreach ($img in $infraImages) {
    Write-Host "  Pulling $img ..." -NoNewline
    docker pull $img 2>&1 | Out-Null
    if ($LASTEXITCODE -eq 0) {
        Write-Host " ✓" -ForegroundColor Green
    } else {
        Write-Host " ✗ (failed)" -ForegroundColor Red
    }
}

# Step 3: Start infrastructure
Write-Host ""
Write-Host "[3/5] Starting infrastructure services..." -ForegroundColor Yellow
docker compose up -d postgres redis minio rabbitmq 2>&1
Start-Sleep -Seconds 5

# Step 4: Start observability + init jobs
Write-Host ""
Write-Host "[4/5] Starting observability and admin tools..." -ForegroundColor Yellow
docker compose up -d pgadmin prometheus grafana swagger-ui 2>&1
docker compose up -d minio-init 2>&1
Start-Sleep -Seconds 5

# Step 5: Show status
Write-Host ""
Write-Host "[5/5] Container status:" -ForegroundColor Yellow
docker compose ps

Write-Host ""
Write-Host "═══════════════════════════════════════════════════════" -ForegroundColor Cyan
Write-Host "  Infrastructure is running!" -ForegroundColor Green
Write-Host ""
Write-Host "  To build and start microservices, run:" -ForegroundColor White
Write-Host "    docker compose up -d --build account-service document-service job-orchestrator ocr-worker stt-worker" -ForegroundColor White
Write-Host ""
Write-Host "  URLs:" -ForegroundColor White
Write-Host "    Swagger UI  → http://localhost:6005" -ForegroundColor Cyan
Write-Host "    pgAdmin     → http://localhost:6100" -ForegroundColor Cyan
Write-Host "    RabbitMQ    → http://localhost:6305" -ForegroundColor Cyan
Write-Host "    MinIO       → http://localhost:6303" -ForegroundColor Cyan
Write-Host "    Grafana     → http://localhost:6201" -ForegroundColor Cyan
Write-Host "    Prometheus  → http://localhost:6200" -ForegroundColor Cyan
Write-Host "═══════════════════════════════════════════════════════" -ForegroundColor Cyan
Write-Host ""
