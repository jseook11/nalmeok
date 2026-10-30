# Korean guidance for the ASCII-only .cmd launchers.
#
# This file must stay UTF-8 *with BOM*. Windows PowerShell 5.1 reads BOM-less
# scripts using the system ANSI code page and would mangle the Korean text.
# Keep every .cmd file pure ASCII and route user-facing Korean here.
param(
    [ValidateSet('drop-help', 'install-failed', 'python-missing')]
    [string]$Id = 'drop-help'
)

switch ($Id) {
    'drop-help' {
        Write-Host ''
        Write-Host '폴더나 MP4/M4A 파일을 이 바로가기 위에 끌어다 놓으세요.'
        Write-Host '결과는 문서 폴더의 nalmeok 안에 모입니다.'
        Write-Host ''
    }
    'install-failed' {
        Write-Host ''
        Write-Host 'Python 설치에 실패했습니다. 네트워크를 확인하고 다시 실행해 주세요.'
        Write-Host ''
    }
    'python-missing' {
        Write-Host ''
        Write-Host 'Python 실행 파일을 찾지 못했습니다. 설치 폴더를 지우고 다시 설치해 주세요.'
        Write-Host ''
    }
}
