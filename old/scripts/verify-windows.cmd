@echo off
chcp 65001 >nul
title nalmeok verify
setlocal
set "HERE=%~dp0"
set "LABEL=%~1"
if "%LABEL%"=="" set "LABEL=conhost"
set "REPORT=%HERE%verify-report-%LABEL%.txt"

echo === console doctor (VT/ANSI is the value to read) ===
set "PYTHONPATH=%HERE%"
"%HERE%runtime\python.exe" -m nalmeok doctor
echo.
if "%WT_SESSION%"=="" (
  echo [info] WT_SESSION is empty: this is not Windows Terminal.
  echo        Open the Terminal app from Start, or run: wt.exe
) else (
  echo [info] Windows Terminal session detected.
)
echo.
(
  echo nalmeok Windows verification
  echo label=%LABEL%
  echo date=%DATE% %TIME%
  echo WT_SESSION=%WT_SESSION%
  echo WT_PROFILE_ID=%WT_PROFILE_ID%
  echo TERM_PROGRAM=%TERM_PROGRAM%
  echo USERNAME=%USERNAME%
  echo COMPUTERNAME=%COMPUTERNAME%
  echo note=report stdout is redirected; use the console doctor output for VT/ANSI
  ver
  chcp
  set "PYTHONPATH=%HERE%"
  "%HERE%runtime\python.exe" -m nalmeok doctor
) > "%REPORT%" 2>&1
type "%REPORT%"
echo.
echo report: %REPORT%
echo.
echo Now check the TUI:
echo   "%HERE%nalmeok.cmd"
echo Check: key screen, up/down, Enter, Esc, Korean path, resize, q to quit
echo Screenshots: Win+Shift+S
pause
