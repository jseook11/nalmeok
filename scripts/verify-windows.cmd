@echo off
chcp 65001 >nul
title 날먹 Windows 검증
setlocal
set "HERE=%~dp0"
set "LABEL=%~1"
if "%LABEL%"=="" set "LABEL=conhost"
set "REPORT=%HERE%verify-report-%LABEL%.txt"
(
  echo nalmeok Windows verification
  echo label=%LABEL%
  echo date=%DATE% %TIME%
  echo WT_SESSION=%WT_SESSION%
  echo TERM_PROGRAM=%TERM_PROGRAM%
  echo USERNAME=%USERNAME%
  echo COMPUTERNAME=%COMPUTERNAME%
  ver
  chcp
  "%HERE%runtime\python.exe" "%HERE%nalmeok.py" doctor
) > "%REPORT%" 2>&1
type "%REPORT%"
echo.
echo 리포트: %REPORT%
echo.
echo 이제 TUI를 확인하세요:
echo   "%HERE%nalmeok.cmd"
echo 체크: 키 화면, 위/아래 이동, Enter, Esc, 한글 폴더명, 창 크기 변경, q 종료
echo 스크린샷은 Win+Shift+S 로 저장해 주세요.
pause
