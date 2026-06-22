# This line automatically asks WSL for its current active IP address
$wslIp = (wsl hostname -I).Split(" ")[0].Trim()

$env:DATABASE_URL="postgres://postgres:root@localhost:5433/trackfund?sslmode=disable"
$env:REDIS_ADDR="$wslIp:6379"
$env:JWT_SECRET="change-me-in-production"
$env:PORT="3000"

$env:SMTP_HOST  = "mail.privateemail.com"
$env:SMTP_PORT  = "465"
$env:SMTP_USER  = "noreply@skills4export.com"
$env:SMTP_PASS  = '!123skills4exporT'
$env:SMTP_FROM  = "noreply@skills4export.com"
# Mono config
$env:MONO_PUBLIC_KEY = "test_pk_t7abdmtip2x92wradvyn"
$env:MONO_SECRET_KEY = "test_sk_x2zlus1mprtutchfo449"
$env:MONO_BASE_URL = "https://api.withmono.com/v2"
$env:MONO_WEBHOOK_SECRET = ""

# Jobs Redis (if different from REDIS_ADDR)
$env:JOBS_REDIS_ADDR = "$wslIp:6379"
$env:JOBS_REDIS_DB = "0"

go run ./cmd/api