# Build, shutdown, and restart workflow

On Windows, `build.bat` executes the following steps:

1. Check the configured local management port (default: `127.0.0.1:8787`).
2. If something is listening, verify it is AliasPort via `GET /health` and `X-AliasPort-Admin: true`.
3. Read the currently active Gateway port from `GET /api/overview`.
4. Request `POST /api/shutdown` and wait for both listeners to close (up to 20 seconds).
5. Resolve Go dependencies, regenerate the ICO, run `go test ./...`, then build `dist/AliasPort.exe`.
6. Only if compilation succeeds, launch the new EXE and verify the management health check.

## Normal build

```powershell
git pull origin main
.\build.bat
```

No manual process termination is needed, and the program starts automatically after a successful build. SQLite configuration and all saved API keys are preserved.

## Manual shutdown route

The program already exposes `POST /api/shutdown` on its management listener. It requires the local management Origin, the action header and an explicit JSON confirmation:

```powershell
$base = "http://127.0.0.1:8787"
Invoke-RestMethod -Uri "$base/api/shutdown" -Method Post `
  -Headers @{
    Origin = $base
    "X-AliasPort-Action" = "shutdown"
  } `
  -ContentType "application/json" `
  -Body '{"confirm":"shutdown"}'
```

Successful acceptance: `{"accepted":true,"message":"AliasPort will stop immediately"}`. The build helper also waits for the actual listener ports to become unavailable.

## Custom management port

If the previous instance was started on a nondefault management port, point both the shutdown probe and the next EXE at the same local address:

```powershell
$env:ALIASPORT_ADMIN_URL = "http://127.0.0.1:8899"
.\build.bat
```

The Gateway proxy port is still managed exclusively within each Gateway profile.

## Build without automatic launch

```powershell
$env:ALIASPORT_NO_START = "1"
.\build.bat
Remove-Item Env:ALIASPORT_NO_START
```

On CI, automatic launch is skipped as well. These options do not skip the initial shutdown check.

## Failure handling

- If an unrelated service occupies the management port, the build **aborts**, never kills that service.
- If AliasPort does not acknowledge shutdown or its management/Gateway port remains open after 20 seconds, the build aborts.
- The helper never uses `taskkill`, forceful PID termination, or process-name matching.
- Failed tests or compilation prevent automatic startup; the previous instance remains stopped.
- If the EXE launches but the management health check fails, startup is reported as unverified.
- Startup errors can be found at `%LOCALAPPDATA%\AliasPort\startup-error.log`.

Implementation: `scripts/build-runtime.ps1` with `-Action Stop` and `-Action Start`, invoked automatically by `build.bat`.
