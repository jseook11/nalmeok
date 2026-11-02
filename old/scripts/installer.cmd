@echo off
title nalmeok installer
setlocal EnableExtensions

rem This file must stay pure ASCII. cmd.exe mis-parses UTF-8 batch lines after
rem chcp 65001 and would run fragments of Korean text as commands. All Korean
rem messages live in app\install.ps1 (UTF-8 with BOM).

if not exist "%~dp0app\install.ps1" (
  echo.
  echo nalmeok installer files are missing.
  echo Extract the whole ZIP first, then run installer.cmd again.
  echo.
  pause
  exit /b 1
)

powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0app\install.ps1"
set "CODE=%errorlevel%"
pause
exit /b %CODE%
