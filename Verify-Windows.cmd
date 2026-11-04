@echo off
setlocal DisableDelayedExpansion
chcp 65001 >nul
set "PYTHONUTF8=1"
py -3 -c "import sys; sys.exit(sys.version_info < (3, 9))" >nul 2>&1
if not errorlevel 1 goto use_py
python -c "import sys; sys.exit(sys.version_info < (3, 9))" >nul 2>&1
if not errorlevel 1 goto use_python
echo Python 3.9 or newer is required. Download: https://www.python.org/downloads/windows/
if "%~1"=="" pause
exit /b 1
:use_py
py -3 -X utf8 -m unittest discover -s "%~dp0tests" -v > "%~dp0Windows-check.txt" 2>&1
goto finish
:use_python
python -X utf8 -m unittest discover -s "%~dp0tests" -v > "%~dp0Windows-check.txt" 2>&1
:finish
set "RESULT=%ERRORLEVEL%"
type "%~dp0Windows-check.txt"
echo Report saved in Windows-check.txt next to this command.
if "%~1"=="" pause
exit /b %RESULT%
