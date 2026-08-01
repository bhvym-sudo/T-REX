@echo off
setlocal

cd /d "%~dp0\.."

echo.
echo ========================================
echo T-REX OSINT release build
echo ========================================
echo.

if not exist "bin" mkdir "bin"
if not exist ".cache\go-build" mkdir ".cache\go-build"
set "GOCACHE=%CD%\.cache\go-build"

echo [1/3] Building Go backend...
go build -o "bin\trex-backend.exe" ".\backend\cmd\trex"
if errorlevel 1 (
    echo.
    echo Go backend build failed. Stopping.
    exit /b 1
)

echo.
echo [2/3] Building Python SearchTimeline worker with Nuitka...
python -m nuitka ^
  --standalone ^
  --onefile ^
  --windows-console-mode=disable ^
  --output-dir=python_worker ^
  --output-filename=search.exe ^
  --remove-output ^
  --include-package=playwright ^
  python_worker/search_timeline.py
if errorlevel 1 (
    echo.
    echo Python worker build failed. Stopping.
    exit /b 1
)

echo.
echo [3/3] Building Electron Windows setup...
npm.cmd run dist
if errorlevel 1 (
    echo.
    echo Electron setup build failed. Stopping.
    exit /b 1
)

echo.
echo ========================================
echo Build completed successfully.
echo Setup output is inside the dist folder.
echo ========================================
echo.

endlocal
