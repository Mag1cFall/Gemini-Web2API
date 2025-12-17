@echo off
echo ==========================================
echo      Gemini-Web2API Update and Run
echo ==========================================
echo.

:: 1. Pull latest code
echo Pulling latest code from remote...
git pull
if %errorlevel% neq 0 (
    echo Git pull failed!
    pause
    exit /b
)
echo Pull successful!
echo.

:: 2. Build the executable
echo Building latest version...
go build -ldflags="-s -w" -o Gemini-Web2API.exe cmd/server/main.go
if %errorlevel% neq 0 (
    echo Build failed! Please check your Go installation.
    pause
    exit /b
)
echo Build successful!
echo.

:: 3. Run the executable
echo Starting server...
echo It will automatically attempt to load cookies from your browser (Firefox/Chrome/Edge).
echo.
Gemini-Web2API.exe

if %errorlevel% neq 0 (
    echo.
    echo Server exited with error.
    pause
)
