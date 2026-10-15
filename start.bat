@echo off
chcp 65001 >nul
setlocal
cd /d "%~dp0"

set "APP=%CD%\gemini-web2api.exe"
if exist "%APP%" goto ready

where go >nul 2>nul
if errorlevel 1 goto missing_tools
where pnpm >nul 2>nul
if errorlevel 1 goto missing_tools

pushd "web"
call pnpm install --frozen-lockfile
if errorlevel 1 goto frontend_failed
call pnpm run build
if errorlevel 1 goto frontend_failed
popd

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

:missing_tools
echo Source build requires Go 1.25.0, Node.js and pnpm, or a published gemini-web2api.exe
pause
exit /b 1

:frontend_failed
popd
pause
exit /b 1
