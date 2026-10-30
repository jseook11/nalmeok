# Creates the desktop and Start Menu shortcuts used by installer.cmd.
# All Korean names and descriptions arrive through environment variables so this
# file stays pure ASCII and works in Windows PowerShell 5.1 without a BOM.
$ErrorActionPreference = 'Stop'
$ws = New-Object -ComObject WScript.Shell
$desktop = [Environment]::GetFolderPath('Desktop')
if (-not $desktop) { $desktop = Join-Path $env:USERPROFILE 'Desktop' }
$startMenu = [Environment]::GetFolderPath('StartMenu')
if (-not $startMenu) { $startMenu = Join-Path $env:APPDATA 'Microsoft\Windows\Start Menu' }
$startMenu = Join-Path $startMenu 'Programs'
New-Item -ItemType Directory -Force -Path $desktop, $startMenu | Out-Null

$items = @(
    @{ Name = $env:NALMEOK_NAME; Target = $env:NALMEOK_TARGET; Desc = $env:NALMEOK_DESC },
    @{ Name = $env:NALMEOK_DROP_NAME; Target = $env:NALMEOK_DROP_TARGET; Desc = $env:NALMEOK_DROP_DESC }
)

foreach ($item in $items) {
    if (-not $item.Name -or -not $item.Target) { throw 'shortcut names/targets are not set' }
    foreach ($dir in @($desktop, $startMenu)) {
        $path = Join-Path $dir ($item.Name + '.lnk')
        $lnk = $ws.CreateShortcut($path)
        $lnk.TargetPath = $item.Target
        $lnk.WorkingDirectory = $env:USERPROFILE
        $lnk.Description = $item.Desc
        if (Test-Path -LiteralPath $env:NALMEOK_ICON) {
            $lnk.IconLocation = $env:NALMEOK_ICON + ',0'
        } else {
            $lnk.IconLocation = $env:SystemRoot + '\System32\shell32.dll,0'
        }
        $lnk.Save()
    }
}
