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

# Prefer to use `air` or `CompileDaemon` for live reload; fall back to `go run`
# Check in PATH first, then in GOPATH\bin and USERPROFILE\go\bin so no shell restart is required.
$watcher = $null
$watcherArgs = $null

# 1) Check PATH for known watchers
$cmd = Get-Command air -ErrorAction SilentlyContinue
if ($cmd) { $watcher = $cmd.Source; $watcherArgs = @() }

if (-not $watcher) {
	$cmd = Get-Command CompileDaemon -ErrorAction SilentlyContinue
	if ($cmd) { $watcher = $cmd.Source; $watcherArgs = @('-command','"go run ./cmd/api"') }
}

# 2) Check GOPATH\bin and USERPROFILE\go\bin directly
if (-not $watcher) {
	$gobin = "$(go env GOPATH)\bin"
	$usergobin = Join-Path $env:USERPROFILE "go\bin"
	$candidates = @(
		Join-Path $gobin 'air.exe', Join-Path $usergobin 'air.exe',
		Join-Path $gobin 'CompileDaemon.exe', Join-Path $usergobin 'CompileDaemon.exe'
	)
	foreach ($c in $candidates) {
		if (Test-Path $c) {
			if ($c -match 'air.exe') { $watcher = $c; $watcherArgs = @() ; break }
			if ($c -match 'CompileDaemon.exe') { $watcher = $c; $watcherArgs = @('-command','"go run ./cmd/api"'); break }
		}
	}
}

if ($watcher) {
	Write-Host "Starting watcher: $watcher"
	& $watcher @watcherArgs
} else {
	Write-Host "No watcher found; running with 'go run'"
	go run ./cmd/api
}