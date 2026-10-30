@echo off
chcp 65001 >nul
title 날먹 설치
setlocal EnableExtensions

set "SRC=%~dp0"
set "DEST=%LOCALAPPDATA%\Programs\nalmeok"

if not defined LOCALAPPDATA goto :env_error
if not exist "%SRC%nalmeok.cmd" goto :missing
if not exist "%SRC%runtime\python.exe" goto :missing

echo.
echo 날먹을 설치합니다.
echo   원본: "%SRC%"
echo   설치: "%DEST%"
echo.

if not exist "%LOCALAPPDATA%\Programs" mkdir "%LOCALAPPDATA%\Programs" >nul 2>&1
robocopy "%SRC%." "%DEST%" /E /R:2 /W:2 /NFL /NDL /NJH /NJS /NP
if errorlevel 8 goto :copy_error

set "NALMEOK_NAME=날먹"
set "NALMEOK_TARGET=%DEST%\nalmeok.cmd"
set "NALMEOK_ICON=%DEST%\nalmeok.ico"
set "NALMEOK_DESC=날먹 - 강의 음성 전사"

powershell -NoProfile -ExecutionPolicy Bypass -Command "try { $ErrorActionPreference = 'Stop'; $ws = New-Object -ComObject WScript.Shell; $desktop = [Environment]::GetFolderPath('Desktop'); $startMenu = Join-Path ([Environment]::GetFolderPath('StartMenu')) 'Programs'; New-Item -ItemType Directory -Force -Path $startMenu | Out-Null; $name = $env:NALMEOK_NAME + '.lnk'; foreach ($dir in @($desktop, $startMenu)) { $path = Join-Path $dir $name; $lnk = $ws.CreateShortcut($path); $lnk.TargetPath = $env:NALMEOK_TARGET; $lnk.WorkingDirectory = $env:USERPROFILE; $lnk.Description = $env:NALMEOK_DESC; if (Test-Path -LiteralPath $env:NALMEOK_ICON) { $lnk.IconLocation = $env:NALMEOK_ICON + ',0' } else { $lnk.IconLocation = $env:SystemRoot + '\System32\shell32.dll,0' }; $lnk.Save() } } catch { Write-Host ('바로가기 만들기 실패: ' + $_.Exception.Message); exit 1 }"
if errorlevel 1 goto :shortcut_error

echo.
echo 설치가 끝났습니다.
echo 바탕화면의 날먹 바로가기로 실행하세요.
echo.
echo 처음 실행하면 Gemini API 키 등록 화면이 나옵니다.
echo 제거하려면 아래 폴더와 바로가기를 직접 삭제하세요.
echo   %DEST%
echo   바탕화면\날먹.lnk
echo   시작 메뉴\날먹.lnk
echo.
pause
exit /b 0

:missing
echo.
echo 오류: ZIP 압축을 먼저 풀어 주세요.
echo installer.cmd는 nalmeok.cmd와 runtime 폴더가 있는 곳에서 실행해야 합니다.
echo.
pause
exit /b 1

:env_error
echo.
echo 오류: LOCALAPPDATA 환경 변수를 찾을 수 없습니다.
echo 사용자 계정으로 로그인한 뒤 다시 실행해 주세요.
echo.
pause
exit /b 1

:copy_error
echo.
echo 오류: 설치 파일을 복사하지 못했습니다. (robocopy %ERRORLEVEL%)
echo 설치 폴더가 사용 중인지 확인한 뒤 다시 실행해 주세요.
echo   %DEST%
echo.
pause
exit /b 1

:shortcut_error
echo.
echo 오류: 바로가기를 만들지 못했습니다.
echo 설치 파일은 복사되었습니다. "%DEST%\nalmeok.cmd"를 직접 실행해 보세요.
echo.
pause
exit /b 1
