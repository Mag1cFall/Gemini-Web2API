@echo off
chcp 65001 >nul
setlocal
cd /d "%~dp0"

set "APP=%CD%\gemini-web2api.exe"
if exist "%APP%" goto ready

where go >nul 2>nul
if errorlevel 1 (
  echo Go 1.25.0 or a published gemini-web2api.exe is required
  pause
  exit /b 1
)
go build -o "%APP%" ./cmd/gemini-web2api
if errorlevel 1 (
  pause
  exit /b 1
)

:ready
if not "%~1"=="" goto run

set "AUTH_READY="
if exist "auth" for /r "auth" %%F in (storage-state.json) do if exist "%%~fF" set "AUTH_READY=1"
if not defined AUTH_READY (
  "%APP%" setup
  if errorlevel 1 (
    pause
    exit /b 1
  )
)

:run
"%APP%" %*
set "EXIT_CODE=%ERRORLEVEL%"
if not "%EXIT_CODE%"=="0" pause
exit /b %EXIT_CODE%
