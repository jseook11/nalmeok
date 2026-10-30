@echo off
title 날먹
setlocal
set "HERE=%~dp0"
set "PYTHONUTF8=1"
set "PYTHONIOENCODING=utf-8"
if "%~1"=="" cd /d "%USERPROFILE%"
"%HERE%runtime\python.exe" "%HERE%nalmeok.py" %*
if errorlevel 1 pause
