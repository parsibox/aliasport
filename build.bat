@echo off
setlocal
cd /d "%~dp0"
where go >nul 2>nul
if errorlevel 1 (
  echo Go is not installed or not in PATH.
  exit /b 1
)
echo [1/3] Downloading dependencies...
go mod download
if errorlevel 1 exit /b 1
echo [2/3] Running tests...
go test ./...
if errorlevel 1 exit /b 1
if not exist dist mkdir dist
echo [3/3] Building Windows GUI executable...
go build -trimpath -ldflags="-H=windowsgui -s -w" -o dist\AliasPort.exe ./cmd/aliasport
if errorlevel 1 exit /b 1
echo.
echo Built dist\AliasPort.exe successfully.
exit /b 0
