# nalmeok installer (Windows).
#
# This file must stay UTF-8 *with BOM*. Windows PowerShell 5.1 reads BOM-less
# scripts using the system ANSI code page and would mangle the Korean text.
# Keep installer.cmd pure ASCII and let this script own every Korean message.
$ErrorActionPreference = 'Stop'

$src = $PSScriptRoot
$dest = Join-Path $env:LOCALAPPDATA 'Programs\nalmeok'

function Stop-Install {
    param([string]$Message)
    Write-Host ''
    Write-Host ('오류: ' + $Message)
    Write-Host ''
    exit 1
}

if (-not $env:LOCALAPPDATA) {
    Stop-Install 'LOCALAPPDATA 환경 변수를 찾을 수 없습니다. 사용자 계정으로 로그인한 뒤 다시 실행해 주세요.'
}

$required = @('nalmeok.cmd', 'nalmeok-drop.cmd', 'bootstrap.ps1',
              'bootstrap.env', 'shortcuts.ps1', 'nalmeok\__init__.py')
foreach ($name in $required) {
    if (-not (Test-Path -LiteralPath (Join-Path $src $name))) {
        Stop-Install 'ZIP 압축을 먼저 풀어 주세요. installer.cmd와 app 폴더가 함께 있어야 합니다.'
    }
}

Write-Host ''
Write-Host 'nalmeok 설치를 시작합니다.'
Write-Host "  원본: $src"
Write-Host "  설치: $dest"
Write-Host ''
Write-Host '설치 파일은 약 200KB입니다. 첫 실행 때 Python과 SDK를 자동으로'
Write-Host '준비하고(약 65MB), MP4 변환을 처음 할 때 ffmpeg를 받습니다.'
Write-Host ''

try {
    New-Item -ItemType Directory -Force -Path $dest | Out-Null
    Copy-Item -Path (Join-Path $src '*') -Destination $dest -Recurse -Force
} catch {
    Stop-Install "설치 파일을 복사하지 못했습니다. 설치 폴더가 사용 중인지 확인한 뒤 다시 실행해 주세요. ($dest)"
}

$env:NALMEOK_NAME = 'nalmeok'
$env:NALMEOK_TARGET = Join-Path $dest 'nalmeok.cmd'
$env:NALMEOK_DESC = 'nalmeok - 강의 음성 전사'
$env:NALMEOK_DROP_NAME = 'nalmeok에 끌어놓기'
$env:NALMEOK_DROP_TARGET = Join-Path $dest 'nalmeok-drop.cmd'
$env:NALMEOK_DROP_DESC = 'nalmeok - 폴더/MP4/M4A 드래그앤드롭 전사'
$env:NALMEOK_ICON = Join-Path $dest 'nalmeok.ico'

try {
    & (Join-Path $dest 'shortcuts.ps1')
} catch {
    Write-Host ''
    Write-Host '오류: 바로가기를 만들지 못했습니다.'
    Write-Host ('  ' + $_.Exception.Message)
    Write-Host "설치 파일은 복사되었습니다. $dest\nalmeok.cmd 를 직접 실행해 보세요."
    Write-Host ''
    exit 1
}

Write-Host ''
Write-Host '설치가 끝났습니다.'
Write-Host '바탕화면의 nalmeok 바로가기로 실행하세요.'
Write-Host '첫 실행에서 Python과 SDK를 내려받으므로 인터넷이 필요합니다.'
Write-Host "폴더나 MP4/M4A 파일을 'nalmeok에 끌어놓기' 바로가기 위에 떨어뜨리면"
Write-Host '전사 결과가 문서 폴더의 nalmeok 안에 모입니다.'
Write-Host ''
Write-Host '처음 실행하면 Gemini API 키 등록 화면이 나옵니다.'
Write-Host '제거하려면 아래 폴더와 바로가기를 직접 삭제하세요.'
Write-Host "  $dest"
Write-Host '  바탕화면\nalmeok.lnk'
Write-Host '  바탕화면\nalmeok에 끌어놓기.lnk'
Write-Host '  시작 메뉴\nalmeok.lnk'
Write-Host '  시작 메뉴\nalmeok에 끌어놓기.lnk'
exit 0
