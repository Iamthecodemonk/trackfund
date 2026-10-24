# This line automatically asks WSL for its current active IP address
$wslIp = (wsl hostname -I).Split(" ")[0].Trim()

$env:DATABASE_URL="postgres://postgres:root@localhost:5433/trackfund?sslmode=disable"
$env:REDIS_ADDR="$wslIp:6379"
if (-not $env:JWT_SECRET -or $env:JWT_SECRET.Trim().Length -lt 32) {
 throw "Set JWT_SECRET to a random secret of at least 32 bytes before running this script."
}
$env:PORT="3000"

# SMTP settings must be supplied through the process environment.
# With no SMTP settings, development uses the existing OTP log sender.
# Mono config
if (-not $env:MONO_PUBLIC_KEY) { Write-Warning "MONO_PUBLIC_KEY is not set." }
if (-not $env:MONO_SECRET_KEY) { Write-Warning "MONO_SECRET_KEY is not set." }
if (-not $env:MONO_BASE_URL) { $env:MONO_BASE_URL = "https://api.withmono.com/v2" }
if (-not $env:MONO_CALLBACK_URL) { Write-Warning "MONO_CALLBACK_URL is not set. Flutter must send redirect_url/callback_url when creating a Mono link session." }
if (-not $env:MONO_WEBHOOK_SECRET) { $env:MONO_WEBHOOK_SECRET = "" }
if (-not $env:MONO_TIMEOUT_SECONDS) { $env:MONO_TIMEOUT_SECONDS = "60" }
if (-not $env:MONO_DEBUG_LOGGING) { $env:MONO_DEBUG_LOGGING = "false" }

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
