@echo off
title nalmeok drop
setlocal

rem Pure ASCII only. The Korean guidance is printed by messages.ps1
rem (UTF-8 with BOM) because cmd.exe cannot parse UTF-8 batch lines safely.

if not "%~1"=="" goto :run
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0messages.ps1" drop-help
pause
exit /b 1

:run
call "%~dp0nalmeok.cmd" drop %*
