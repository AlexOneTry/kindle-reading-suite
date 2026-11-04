@echo off
setlocal
"%SystemRoot%\System32\WindowsPowerShell\v1.0\powershell.exe" -NoLogo -NoProfile -ExecutionPolicy Bypass -File "%~dp0connect-kindle.ps1"
set "RESULT=%ERRORLEVEL%"
echo.
pause
exit /b %RESULT%
