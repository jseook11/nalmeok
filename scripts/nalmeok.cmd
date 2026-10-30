@echo off
title nalmeok
setlocal EnableExtensions
set "HERE=%~dp0"
set "PYTHONUTF8=1"
set "PYTHONIOENCODING=utf-8"

rem Offline extended bundle: use the runtime and ffmpeg shipped next to this file.
if exist "%HERE%runtime\python.exe" (
  set "NALMEOK_HOME=%HERE%"
  set "NALMEOK_PACKAGED=1"
  set "NALMEOK_YES=1"
  set "NALMEOK_FFMPEG=%HERE%"
  if "%~1"=="" cd /d "%USERPROFILE%"
  "%HERE%runtime\python.exe" "%HERE%nalmeok.py" %*
  if errorlevel 1 pause
  exit /b
)

set "DATA=%LOCALAPPDATA%\nalmeok"
set "NALMEOK_HOME=%DATA%"
set "NALMEOK_PACKAGED=1"
set "NALMEOK_YES=1"
set "NALMEOK_FFMPEG=%DATA%\bin"
if not exist "%DATA%\app" mkdir "%DATA%\app" >nul 2>&1
if not exist "%DATA%\app\nalmeok.py" copy /y "%HERE%nalmeok.py" "%DATA%\app\nalmeok.py" >nul 2>&1

call :find_python
if not defined PY (
  powershell -NoProfile -ExecutionPolicy Bypass -File "%HERE%bootstrap.ps1"
  if errorlevel 1 (
    powershell -NoProfile -ExecutionPolicy Bypass -File "%HERE%messages.ps1" install-failed
    pause
    exit /b 1
  )
  call :find_python
)
if not defined PY (
  powershell -NoProfile -ExecutionPolicy Bypass -File "%HERE%messages.ps1" python-missing
  pause
  exit /b 1
)

if "%~1"=="" cd /d "%USERPROFILE%"
"%PY%" "%DATA%\app\nalmeok.py" %*
set "CODE=%errorlevel%"
if not "%CODE%"=="0" pause
exit /b %CODE%

:find_python
set "PY="
if not exist "%DATA%\runtime\current.txt" exit /b 0
set /p PYVER=<"%DATA%\runtime\current.txt"
if not defined PYVER exit /b 0
if exist "%DATA%\runtime\%PYVER%\python.exe" set "PY=%DATA%\runtime\%PYVER%\python.exe"
exit /b 0
