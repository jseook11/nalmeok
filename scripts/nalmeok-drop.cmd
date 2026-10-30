@echo off
chcp 65001 >nul
title nalmeok drop
setlocal
if "%~1"=="" (
  echo.
  echo 폴더나 MP4/M4A 파일을 이 바로가기 위에 끌어다 놓으세요.
  echo 결과는 문서 폴더의 nalmeok 안에 모입니다.
  echo.
  pause
  exit /b 1
)
call "%~dp0nalmeok.cmd" drop %*
