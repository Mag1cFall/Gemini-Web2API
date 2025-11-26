@echo off
echo ==========================================
echo      Gemini-Web2API (Go Version)
echo ==========================================
echo.

:: 1. Check if exe exists, if not or if code changed, rebuild
if not exist Gemini-Web2API.exe (
    echo Executable not found. Building...
    go build -ldflags="-s -w" -o Gemini-Web2API.exe cmd/server/main.go
    if %errorlevel% neq 0 (
        echo Build failed! Please check your Go installation.
        pause
        exit /b
    )
    echo Build successful!
)

:: 2. Run the executable
echo Starting server...
echo It will automatically attempt to load cookies from your browser (Firefox/Chrome/Edge).
echo.
Gemini-Web2API.exe

if %errorlevel% neq 0 (
    echo.
    echo Server exited with error.
    pause
)