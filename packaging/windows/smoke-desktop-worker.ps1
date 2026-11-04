param([Parameter(Mandatory = $true)][string]$Executable)

$ErrorActionPreference = "Stop"
$temporary = Join-Path ([IO.Path]::GetTempPath()) ("velin desktop smoke " + [guid]::NewGuid())
$portable = Join-Path $temporary "portable application"
$launchDirectory = Join-Path $temporary "unrelated working directory"
$worker = $null
$occupied = $null
$keys = @("VELIN_ADDR", "VELIN_DATA_DIR", "VELIN_WEB_DIST", "VELIN_COOKIE_SECURE", "VELIN_ADMIN_USER", "VELIN_ADMIN_PASSWORD")
$previous = @{}
foreach ($key in $keys) {
    $previous[$key] = [Environment]::GetEnvironmentVariable($key, "Process")
    [Environment]::SetEnvironmentVariable($key, $null, "Process")
}

function Start-Worker([string]$Extra = "") {
    $info = [Diagnostics.ProcessStartInfo]::new()
    $info.FileName = $binary
    $info.Arguments = "--service-worker $Extra"
    $info.WorkingDirectory = $launchDirectory
    $info.UseShellExecute = $false
    $info.CreateNoWindow = $true
    $info.RedirectStandardInput = $true
    $info.RedirectStandardOutput = $true
    $info.RedirectStandardError = $true
    $process = [Diagnostics.Process]::new()
    $process.StartInfo = $info
    $null = $process.Start()
    try {
        $line = $process.StandardOutput.ReadLineAsync()
        if (-not $line.Wait(45000)) { throw "Service worker startup timed out" }
        if (-not $line.Result) { throw "Worker returned no startup information" }
        return @{ Process = $process; Ready = ($line.Result | ConvertFrom-Json) }
    } catch {
        if (-not $process.HasExited) { $process.Kill() }
        throw
    }
}

function Stop-Worker($Value) {
    $Value.Process.StandardInput.WriteLine("stop")
    $Value.Process.StandardInput.Close()
    if (-not $Value.Process.WaitForExit(20000)) { $Value.Process.Kill(); throw "Worker did not stop gracefully" }
    if ($Value.Process.ExitCode -ne 0) { throw "Worker exited with an error" }
}

try {
    New-Item -ItemType Directory -Path $portable, $launchDirectory | Out-Null
    $binary = Join-Path $portable "velin-web-ssh.exe"
    Copy-Item -LiteralPath $Executable -Destination $binary
    $listener = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, 0)
    $listener.Start()
    $port = $listener.LocalEndpoint.Port
    $listener.Stop()
    $env:VELIN_ADDR = "127.0.0.1:$port"
    $base = "http://127.0.0.1:$port"
    $worker = Start-Worker
    if ($worker.Ready.error -or $worker.Ready.url -ne "$base/") { throw "Worker failed to bind the configured port" }
    $page = Invoke-WebRequest "$base/login"
    $script = [regex]::Match($page.Content, 'src="(/assets/[^" ]+\.js)"').Groups[1].Value
    if (-not $script) { throw "Embedded login page is missing its application script" }
    $asset = Invoke-WebRequest "$base$script"
    if ($asset.StatusCode -ne 200 -or $asset.Content.Length -lt 1000 -or $asset.Headers['Content-Type'] -notmatch 'javascript') { throw "Embedded frontend is unavailable" }
    foreach ($name in @("velin.db", "master.key", "initial-admin-credentials.txt")) {
        if (-not (Test-Path (Join-Path $portable $name))) { throw "Missing portable file: $name" }
    }
    if (Test-Path (Join-Path $portable "web")) { throw "Test must not have external frontend files" }
    if ((Get-ChildItem -LiteralPath $launchDirectory -Force).Count -ne 0) { throw "Worker wrote data to its launch directory" }

    $csrf = Invoke-RestMethod "$base/api/csrf" -SessionVariable browser
    $credentials = $worker.Ready.credentials
    $body = @{ username = $credentials.username; password = $credentials.password } | ConvertTo-Json
    $login = Invoke-RestMethod "$base/api/auth/login" -Method Post -WebSession $browser -ContentType "application/json" -Headers @{ 'X-CSRF-Token' = $csrf.token } -Body $body
    $me = Invoke-RestMethod "$base/api/auth/me" -WebSession $browser
    if ($login.user.username -ne "admin" -or $me.username -ne "admin") { throw "Generated credentials or HTTP session failed" }
    Stop-Worker $worker
    $worker = $null
    $stillRunning = $false
    try { $null = Invoke-WebRequest "$base/api/health/live" -TimeoutSec 2; $stillRunning = $true } catch {}
    if ($stillRunning) { throw "Stopped worker still accepts connections" }

    $worker = Start-Worker "--reset-admin-password"
    if (-not $worker.Ready.credentials.reset -or $worker.Ready.credentials.password -eq $credentials.password) { throw "Reset credentials were not returned to the launcher" }
    Stop-Worker $worker
    $worker = $null
    $worker = Start-Worker
    if ($worker.Ready.error -or $worker.Ready.credentials) { throw "Service restart failed or recreated the administrator" }
    # The parent disappearing closes stdin; the worker must exit with it.
    $worker.Process.StandardInput.Close()
    if (-not $worker.Process.WaitForExit(20000) -or $worker.Process.ExitCode -ne 0) { throw "Worker survived its owner closing the control pipe" }
    $worker = $null

    $occupied = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, $port)
    $occupied.Start()
    $worker = Start-Worker
    if (-not $worker.Ready.error) { throw "Service mode silently changed an occupied port" }
    if (-not $worker.Process.WaitForExit(10000) -or $worker.Process.ExitCode -eq 0) { throw "Failed service startup did not exit" }
    $worker = $null
    Write-Host "Managed desktop service passed: embedded UI, portable data, login, stop, restart, password reset, owner exit and occupied port."
} catch {
    $logPath = Join-Path $portable "velin-web-ssh.log"
    if (Test-Path $logPath) { Get-Content $logPath -Tail 30 | Write-Host }
    throw
} finally {
    if ($null -ne $worker -and -not $worker.Process.HasExited) { $worker.Process.Kill() }
    if ($null -ne $occupied) { $occupied.Stop() }
    foreach ($key in $keys) { [Environment]::SetEnvironmentVariable($key, $previous[$key], "Process") }
    Remove-Item -LiteralPath $temporary -Recurse -Force -ErrorAction SilentlyContinue
}
