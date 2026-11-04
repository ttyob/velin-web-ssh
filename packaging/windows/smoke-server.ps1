param(
    [Parameter(Mandatory = $true)]
    [string]$Executable
)

$ErrorActionPreference = "Stop"
$temporary = Join-Path ([IO.Path]::GetTempPath()) ("velin server smoke " + [guid]::NewGuid())
$portable = Join-Path $temporary "portable server"
$launchDirectory = Join-Path $temporary "unrelated working directory"
$server = $null
$keys = @("VELIN_ADDR", "VELIN_DATA_DIR", "VELIN_WEB_DIST", "VELIN_COOKIE_SECURE", "VELIN_ADMIN_USER", "VELIN_ADMIN_PASSWORD")
$previous = @{}
foreach ($key in $keys) {
    $previous[$key] = [Environment]::GetEnvironmentVariable($key, "Process")
    [Environment]::SetEnvironmentVariable($key, $null, "Process")
}

try {
    New-Item -ItemType Directory -Path $portable, $launchDirectory | Out-Null
    $binary = Join-Path $portable "velin-server.exe"
    Copy-Item -LiteralPath $Executable -Destination $binary
    $listener = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, 0)
    $listener.Start()
    $port = $listener.LocalEndpoint.Port
    $listener.Stop()
    $env:VELIN_ADDR = "127.0.0.1:$port"
    $base = "http://127.0.0.1:$port"
    $stdout = Join-Path $temporary "stdout.log"
    $stderr = Join-Path $temporary "stderr.log"
    $server = Start-Process -FilePath $binary -WorkingDirectory $launchDirectory -RedirectStandardOutput $stdout -RedirectStandardError $stderr -PassThru
    $ready = $false
    for ($attempt = 0; $attempt -lt 60; $attempt++) {
        if ($server.HasExited) { throw "Standalone server exited before becoming ready" }
        try {
            $health = Invoke-WebRequest "$base/api/health/live" -TimeoutSec 2
            if ($health.StatusCode -eq 200) { $ready = $true; break }
        } catch {
            Start-Sleep -Milliseconds 500
        }
    }
    if (-not $ready) { throw "Standalone server did not become ready" }

    $page = Invoke-WebRequest "$base/login"
    $script = [regex]::Match($page.Content, 'src="(/assets/[^" ]+\.js)"').Groups[1].Value
    if (-not $script) { throw "Embedded login page is missing its application script" }
    $asset = Invoke-WebRequest "$base$script"
    if ($asset.StatusCode -ne 200 -or $asset.Content.Length -lt 1000 -or $asset.Headers['Content-Type'] -notmatch 'javascript') {
        throw "Embedded application script is unavailable"
    }
    if (Test-Path (Join-Path $portable "web")) { throw "Smoke test must not have external frontend files" }
    $credentialsPath = Join-Path $portable "initial-admin-credentials.txt"
    $credentials = @{}
    foreach ($line in Get-Content -LiteralPath $credentialsPath) {
        $pair = $line -split '=', 2
        $credentials[$pair[0]] = $pair[1]
    }
    if (-not (Test-Path (Join-Path $portable "velin.db")) -or -not (Test-Path (Join-Path $portable "master.key"))) {
        throw "Portable data files were not created beside the EXE"
    }
    if ((Get-ChildItem -LiteralPath $launchDirectory -Force).Count -ne 0) {
        throw "Server incorrectly wrote data to the launch directory"
    }

    $csrf = Invoke-RestMethod "$base/api/csrf" -SessionVariable browser
    $csrfCookies = @($browser.Cookies.GetCookies([uri]$base) | Where-Object { $_.Name -like 'velin_csrf*' })
    if ($csrfCookies.Count -ne 1 -or $csrfCookies[0].Secure) { throw "Default HTTP cookies are unusable" }
    $body = @{ username = $credentials['username']; password = $credentials['password'] } | ConvertTo-Json
    $login = Invoke-RestMethod "$base/api/auth/login" -Method Post -WebSession $browser -ContentType "application/json" -Headers @{ 'X-CSRF-Token' = $csrf.token } -Body $body
    $me = Invoke-RestMethod "$base/api/auth/me" -WebSession $browser
    if ($login.user.username -ne "admin" -or $me.username -ne "admin") { throw "Generated administrator credentials or HTTP session failed" }

    Stop-Process -Id $server.Id -Force
    $null = $server.WaitForExit(10000)
    $server = $null
    Push-Location $launchDirectory
    try {
        $reset = & $binary --reset-admin-password 2>&1
        if ($LASTEXITCODE -ne 0 -or ($reset -join "`n") -notmatch 'username=admin') { throw "Standalone password reset failed" }
    } finally {
        Pop-Location
    }
    Write-Host "Windows standalone server passed: embedded UI, portable data, generated credentials, HTTP login and password reset."
} catch {
    if (Test-Path (Join-Path $temporary "stderr.log")) {
        Get-Content (Join-Path $temporary "stderr.log") | Write-Host
    }
    throw
} finally {
    if ($null -ne $server -and -not $server.HasExited) {
        Stop-Process -Id $server.Id -Force
        $null = $server.WaitForExit(10000)
    }
    foreach ($key in $keys) {
        [Environment]::SetEnvironmentVariable($key, $previous[$key], "Process")
    }
    Remove-Item -LiteralPath $temporary -Recurse -Force -ErrorAction SilentlyContinue
}
