@echo off
setlocal
cd /d "%~dp0"

where go >nul 2>nul
if errorlevel 1 (
  echo ERROR: Go is not installed or is not in PATH.
  exit /b 1
)
where powershell.exe >nul 2>nul
if errorlevel 1 (
  echo ERROR: Windows PowerShell is required to stop and restart AliasPort safely.
  exit /b 1
)

rem Windows 10/11 64-bit GUI EXE; no console on execution.
set "GOOS=windows"
set "GOARCH=amd64"
set "CGO_ENABLED=0"

echo [1/7] Stopping an existing AliasPort through its local shutdown API...
powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "scripts\build-runtime.ps1" -Action Stop
if errorlevel 1 (
  echo ERROR: AliasPort shutdown could not be verified. Build canceled.
  exit /b 1
)

echo [2/7] Resolving Go dependencies...
go mod tidy
if errorlevel 1 exit /b 1

echo [3/7] Generating AliasPort icon and favicon...
go run ./tools/icon
if errorlevel 1 exit /b 1

echo [4/7] Embedding icon in Windows PE resources...
rem Pin the resource tool version; Go downloads it on first use.
if exist "cmd\aliasport\rsrc_windows_amd64.syso" del /q "cmd\aliasport\rsrc_windows_amd64.syso"
go run github.com/akavel/rsrc@v0.10.2 -arch amd64 -ico "assets\aliasport.ico" -o "cmd\aliasport\rsrc_windows_amd64.syso"
if errorlevel 1 exit /b 1
if not exist "cmd\aliasport\rsrc_windows_amd64.syso" (
  echo ERROR: Icon resource was not generated.
  exit /b 1
)

echo [5/7] Running tests...
go test ./...
if errorlevel 1 (
  echo ERROR: Tests failed. Existing AliasPort remains stopped.
  exit /b 1
)

echo [6/7] Building Windows GUI executable with embedded icon...
if not exist "dist" mkdir "dist"
go build -trimpath -ldflags="-H=windowsgui -s -w" -o "dist\AliasPort.exe" ./cmd/aliasport
if errorlevel 1 (
  echo ERROR: Build failed. Existing AliasPort remains stopped.
  exit /b 1
)
if not exist "dist\AliasPort.exe" (
  echo ERROR: dist\AliasPort.exe was not created.
  exit /b 1
)

echo.
echo SUCCESS: dist\AliasPort.exe
if /I "%CI%"=="true" goto skipLaunch
if /I "%CI%"=="1" goto skipLaunch
if "%ALIASPORT_NO_START%"=="1" goto skipLaunch

echo [7/7] Starting the newly built AliasPort.exe...
powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "scripts\build-runtime.ps1" -Action Start
if errorlevel 1 (
  echo ERROR: Built EXE exists, but automatic startup was not verified.
  exit /b 1
)
echo AliasPort was rebuilt and restarted successfully.
exit /b 0

:skipLaunch
echo [7/7] Automatic launch skipped: CI or ALIASPORT_NO_START=1.
exit /b 0
