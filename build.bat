@echo off
setlocal
cd /d "%~dp0"

where go >nul 2>nul
if errorlevel 1 (
  echo ERROR: Go is not installed or is not in PATH.
  exit /b 1
)

rem The resource object is generated specifically for Windows amd64.
set "GOOS=windows"
set "GOARCH=amd64"
set "CGO_ENABLED=0"

echo [1/5] Resolving Go dependencies...
go mod tidy
if errorlevel 1 exit /b 1

echo [2/5] Generating AliasPort icon and favicon...
go run ./tools/icon
if errorlevel 1 exit /b 1

echo [3/5] Embedding icon in Windows PE resources...
rem Pin the resource tool version for repeatable builds.
rem Go downloads it automatically the first time; no Visual Studio needed.
if exist "cmd\aliasport\rsrc_windows_amd64.syso" del /q "cmd\aliasport\rsrc_windows_amd64.syso"
go run github.com/akavel/rsrc@v0.10.2 -arch amd64 -ico "assets\aliasport.ico" -o "cmd\aliasport\rsrc_windows_amd64.syso"
if errorlevel 1 exit /b 1
if not exist "cmd\aliasport\rsrc_windows_amd64.syso" (
  echo ERROR: Icon resource was not generated.
  exit /b 1
)

echo [4/5] Running tests...
go test ./...
if errorlevel 1 exit /b 1

echo [5/5] Building Windows GUI executable with embedded icon...
if not exist "dist" mkdir "dist"
go build -trimpath -ldflags="-H=windowsgui -s -w" -o "dist\AliasPort.exe" ./cmd/aliasport
if errorlevel 1 exit /b 1

echo.
echo SUCCESS: dist\AliasPort.exe
echo Windows icon: embedded from assets\aliasport.ico
exit /b 0
