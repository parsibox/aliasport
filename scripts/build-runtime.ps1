# Used by build.bat; never kills an unknown process by name or PID.
param(
    [Parameter(Mandatory=$true)]
    [ValidateSet('Stop','Start')]
    [string]$Action
)

$ErrorActionPreference = 'Stop'
$root = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$exe = Join-Path $root 'dist\AliasPort.exe'

function Get-AdminBase {
    $value = $env:ALIASPORT_ADMIN_URL
    if ([string]::IsNullOrWhiteSpace($value)) {
        $value = 'http://127.0.0.1:8787'
    }
    $uri = $null
    if (-not [Uri]::TryCreate($value, [UriKind]::Absolute, [ref]$uri)) {
        throw "Invalid ALIASPORT_ADMIN_URL: $value"
    }
    if ($uri.Scheme -ne 'http' -or
        ($uri.Host -ne '127.0.0.1' -and $uri.Host -ne '::1') -or
        $uri.Port -lt 1 -or $uri.Port -gt 65535 -or
        $uri.AbsolutePath -ne '/' -or
        $uri.Query -ne '' -or $uri.Fragment -ne '' -or
        $uri.UserInfo -ne '') {
        throw 'ALIASPORT_ADMIN_URL must be a loopback origin, e.g. http://127.0.0.1:8787'
    }
    return $uri.GetLeftPart([UriPartial]::Authority)
}

function Test-LocalPort {
    param([string]$Address, [int]$Port)
    $client = New-Object System.Net.Sockets.TcpClient
    $pending = $null
    try {
        $pending = $client.BeginConnect($Address, $Port, $null, $null)
        if (-not $pending.AsyncWaitHandle.WaitOne(400)) { return $false }
        $client.EndConnect($pending)
        return $true
    }
    catch {
        return $false
    }
    finally {
        if ($null -ne $pending) { $pending.AsyncWaitHandle.Close() }
        $client.Dispose()
    }
}

function Request-Local {
    param([string]$Url, [string]$Verb='GET',
          [string]$Origin='', [string]$Payload='')
    $opts = @{
        UseBasicParsing = $true
        MaximumRedirection = 0
        Uri = $Url
        TimeoutSec = 4
        ErrorAction = 'Stop'
    }
    if ($Verb -eq 'POST') {
        $opts['Method'] = 'Post'
        $opts['ContentType'] = 'application/json'
        $opts['Headers'] = @{
            Origin = $Origin
            'X-AliasPort-Action' = 'shutdown'
        }
        $opts['Body'] = $Payload
    }
    else {
        $opts['Method'] = 'Get'
    }
    return Invoke-WebRequest @opts
}

function Test-AliasPortAdmin {
    param([string]$Base)
    $health = Request-Local -Url ($Base + '/health')
    if ($health.StatusCode -ne 200 -or
        [string]$health.Headers['X-AliasPort-Admin'] -ne 'true') {
        throw "Port belongs to an unrecognized service: $Base"
    }
    $body = $health.Content | ConvertFrom-Json
    if ($body.ok -ne $true -or $body.service -ne 'aliasport-admin') {
        throw "Unexpected health response at $Base"
    }
}

function Get-ActiveProxyPort {
    param([string]$Base)
    $response = Request-Local -Url ($Base + '/api/overview')
    if ($response.StatusCode -ne 200) {
        throw "Cannot read current Gateway port from $Base"
    }
    $overview = $response.Content | ConvertFrom-Json
    if ([string]::IsNullOrWhiteSpace([string]$overview.gateway_url)) {
        return 0
    }
    $proxy = $null
    if (-not [Uri]::TryCreate([string]$overview.gateway_url, [UriKind]::Absolute, [ref]$proxy) -or
        $proxy.Scheme -ne 'http' -or
        ($proxy.Host -ne '127.0.0.1' -and $proxy.Host -ne '::1') -or
        $proxy.Port -lt 1 -or $proxy.Port -gt 65535) {
        throw 'Existing AliasPort reports an invalid proxy URL; refusing to build.'
    }
    return $proxy.Port
}

function Stop-Existing {
    param([string]$Base)
    $adminUri = [Uri]$Base
    if (-not (Test-LocalPort -Address $adminUri.Host -Port $adminUri.Port)) {
        Write-Host '[shutdown] No running AliasPort management listener.'
        return
    }

    # Only stop a verified AliasPort management listener.
    Test-AliasPortAdmin -Base $Base
    $proxyPort = Get-ActiveProxyPort -Base $Base
    Write-Host "[shutdown] Stopping existing AliasPort at $Base ..."
    $response = Request-Local -Url ($Base + '/api/shutdown') -Verb 'POST' -Origin $Base -Payload '{"confirm":"shutdown"}'
    $answer = $response.Content | ConvertFrom-Json
    if ($response.StatusCode -ne 200 -or $answer.accepted -ne $true) {
        throw 'AliasPort did not accept the shutdown command.'
    }

    $deadline = [DateTime]::UtcNow.AddSeconds(20)
    do {
        $adminBusy = Test-LocalPort -Address $adminUri.Host -Port $adminUri.Port
        $proxyBusy = ($proxyPort -ne 0 -and
            (Test-LocalPort -Address '127.0.0.1' -Port $proxyPort))
        if (-not $adminBusy -and -not $proxyBusy) {
            # Give Windows a moment to release the mapped EXE after Go exits.
            Start-Sleep -Milliseconds 300
            Write-Host '[shutdown] Both listeners have stopped.'
            return
        }
        Start-Sleep -Milliseconds 150
    } while ([DateTime]::UtcNow -lt $deadline)
    throw 'AliasPort did not stop within 20 seconds. Not overwriting a running EXE.'
}

function Start-Built {
    param([string]$Base)
    if (-not (Test-Path -LiteralPath $exe -PathType Leaf)) {
        throw "Built executable not found: $exe"
    }
    $adminUri = [Uri]$Base
    if (Test-LocalPort -Address $adminUri.Host -Port $adminUri.Port) {
        throw "Cannot launch rebuilt AliasPort: management port $($adminUri.Port) is occupied."
    }
    $arguments = @()
    if (-not [string]::IsNullOrWhiteSpace($env:ALIASPORT_ADMIN_URL)) {
        $arguments += ('-admin=' + $adminUri.Authority)
    }
    Write-Host "[startup] Launching $exe ..."
    if ($arguments.Count -gt 0) {
        $process = Start-Process -FilePath $exe -ArgumentList $arguments -WorkingDirectory $root -PassThru
    }
    else {
        $process = Start-Process -FilePath $exe -WorkingDirectory $root -PassThru
    }
    $deadline = [DateTime]::UtcNow.AddSeconds(15)
    do {
        Start-Sleep -Milliseconds 200
        $process.Refresh()
        if ($process.HasExited) {
            throw "AliasPort.exe exited with code $($process.ExitCode). Check the startup-error.log in its data folder."
        }
        if (Test-LocalPort -Address $adminUri.Host -Port $adminUri.Port) {
            try {
                Test-AliasPortAdmin -Base $Base
                Write-Host "[startup] AliasPort is running: $Base (PID $($process.Id))"
                return
            }
            catch {
                # It may still be starting. Retry until the deadline.
            }
        }
    } while ([DateTime]::UtcNow -lt $deadline)
    throw "AliasPort.exe was started, but management health was not verified at $Base."
}

try {
    $base = Get-AdminBase
    if ($Action -eq 'Stop') {
        Stop-Existing -Base $base
    }
    else {
        Start-Built -Base $base
    }
    exit 0
}
catch {
    Write-Host ("ERROR: " + $_.Exception.Message) -ForegroundColor Red
    exit 1
}
