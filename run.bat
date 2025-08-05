@echo off
:: Section to request Administrator privileges
>nul 2>&1 "%SYSTEMROOT%\system32\cacls.exe" "%SYSTEMROOT%\system32\config\system"
if '%errorlevel%' NEQ '0' (
    echo Requesting administrative privileges...
    goto UACPrompt
) else ( goto gotAdmin )

:UACPrompt
    echo Set UAC = CreateObject^("Shell.Application"^) > "%temp%\getadmin.vbs"
    echo UAC.ShellExecute "%~s0", "", "", "runas", 1 >> "%temp%\getadmin.vbs"
    "%temp%\getadmin.vbs"
    exit /B

:gotAdmin
    if exist "%temp%\getadmin.vbs" ( del "%temp%\getadmin.vbs" )
    pushd "%CD%"
    CD /D "%~dp0"
:: End of Administrator request section

:: Change directory to the script's location
cd /d "%~dp0"

echo Creating virtual environment...
python -m venv .venv

echo Activating virtual environment...
call .venv\Scripts\activate

echo Installing the gemini-webapi package in editable mode first...
pip install -e .

echo Installing dependencies from requirements.txt...
pip install -r requirements.txt

echo.
echo Running cookie retrieval script...
python get_cookies.py
echo.

echo ==================================================
echo Setup complete.
echo.
echo Your API keys should be in a file named .env
echo To run the server in the future, just run this script again.
echo ==================================================
echo.
echo Starting server now...
python openai_adapter.py