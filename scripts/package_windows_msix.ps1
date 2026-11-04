# Package the Windows executables as an MSIX bundle for the Microsoft Store.
#
# Usage: powershell -File scripts/package_windows_msix.ps1 [-Version x.y.z] [-Register]
#   -Version      three-part version; the package version appends ".0"
#                 (default: the VERSION default in scripts/build_windows_app.sh)
#   -IdentityName package identity name (default: $env:NALMEOK_MSIX_NAME, else Seook.Nalmeok)
#   -Publisher    publisher DN (default: $env:NALMEOK_MSIX_PUBLISHER, else the Store publisher)
#   -ExeDir       folder with Nalmeok.exe and Nalmeok-arm64.exe (default: build/windows)
#   -OutDir       output folder for .msix and .msixbundle (default: dist)
#   -Register     register the current PC's layout for local testing instead of
#                 packing (developer mode, no signing, no SDK, no package output)
param(
    [string]$Version,
    [string]$IdentityName,
    [string]$Publisher,
    [string]$ExeDir = "build/windows",
    [string]$OutDir = "dist",
    [switch]$Register
)

$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $PSScriptRoot
Set-Location $Root

if (-not $Version) {
    $match = Select-String -Path "scripts/build_windows_app.sh" -Pattern 'VERSION="\$\{VERSION:-([0-9]+\.[0-9]+\.[0-9]+)\}"'
    if (-not $match) {
        throw "scripts/build_windows_app.sh에서 기본 버전을 읽지 못했다. -Version으로 지정한다."
    }
    $Version = $match.Matches[0].Groups[1].Value
}
if ($Version -notmatch '^\d+\.\d+\.\d+$') {
    throw "버전은 x.y.z 세 자리여야 한다: $Version"
}
# The fourth part is always 0 for Store submissions.
$PackageVersion = $Version + ".0"

# Partner Center → Product identity (not secret).
if (-not $IdentityName) { $IdentityName = $env:NALMEOK_MSIX_NAME }
if (-not $IdentityName) { $IdentityName = "Seook.Nalmeok" }
if (-not $Publisher) { $Publisher = $env:NALMEOK_MSIX_PUBLISHER }
if (-not $Publisher) { $Publisher = "CN=9C1988C4-DDED-41A0-88F0-2005D33927CF" }

$TemplatePath = Join-Path $Root "packaging/windows/AppxManifest.xml"
$AssetsDir = Join-Path $Root "packaging/windows/Assets"
$ExeDirPath = Join-Path $Root $ExeDir
$OutDirPath = Join-Path $Root $OutDir
$LayoutRoot = Join-Path $Root "build/msix"

$arches = @(
    [pscustomobject]@{ Arch = "x64"; Exe = "Nalmeok.exe" },
    [pscustomobject]@{ Arch = "arm64"; Exe = "Nalmeok-arm64.exe" }
)

function New-Layout {
    param([string]$Arch, [string]$ExeName)

    $exe = Join-Path $ExeDirPath $ExeName
    if (-not (Test-Path $exe)) {
        throw "실행 파일이 없다: $exe (scripts/build_windows_app.sh로 만든다)"
    }

    $layout = Join-Path $LayoutRoot $Arch
    if (Test-Path $layout) {
        Remove-Item $layout -Recurse -Force
    }
    New-Item -ItemType Directory -Path (Join-Path $layout "Assets") -Force | Out-Null
    Copy-Item $exe (Join-Path $layout "Nalmeok.exe")
    Copy-Item (Join-Path $AssetsDir "*.png") (Join-Path $layout "Assets")

    $manifest = Get-Content $TemplatePath -Raw -Encoding UTF8
    $manifest = $manifest.Replace("{{IDENTITY_NAME}}", $IdentityName)
    $manifest = $manifest.Replace("{{PUBLISHER}}", $Publisher)
    $manifest = $manifest.Replace("{{VERSION}}", $PackageVersion)
    $manifest = $manifest.Replace("{{ARCH}}", $Arch)
    # Write without a BOM: makeappx reads the manifest as UTF-8.
    $manifestPath = Join-Path $layout "AppxManifest.xml"
    [System.IO.File]::WriteAllText($manifestPath, $manifest, (New-Object System.Text.UTF8Encoding($false)))

    return $layout
}

if ($Register) {
    $arch = switch ($env:PROCESSOR_ARCHITECTURE) {
        "AMD64" { "x64" }
        "ARM64" { "arm64" }
        default { throw "지원하지 않는 아키텍처다: $env:PROCESSOR_ARCHITECTURE" }
    }
    $entry = $arches | Where-Object { $_.Arch -eq $arch }
    $layout = New-Layout -Arch $entry.Arch -ExeName $entry.Exe
    Add-AppxPackage -Register (Join-Path $layout "AppxManifest.xml")
    Write-Host "등록했다: $layout (개발자 모드 필요)"
    return
}

$KitsBin = "${env:ProgramFiles(x86)}\Windows Kits\10\bin"
if (-not (Test-Path $KitsBin)) {
    throw "Windows SDK를 찾지 못했다: $KitsBin"
}
$sdk = Get-ChildItem $KitsBin -Directory |
    Where-Object { $_.Name -match '^\d+\.\d+\.\d+\.\d+$' } |
    Sort-Object { [version]$_.Name } -Descending |
    Select-Object -First 1
if (-not $sdk) {
    throw "Windows SDK 10을 찾지 못했다: $KitsBin"
}
$MakeAppx = Join-Path $sdk.FullName "x64\makeappx.exe"
if (-not (Test-Path $MakeAppx)) {
    throw "makeappx.exe를 찾지 못했다: $MakeAppx"
}

New-Item -ItemType Directory -Path $OutDirPath -Force | Out-Null

$msixFiles = @()
foreach ($entry in $arches) {
    $layout = New-Layout -Arch $entry.Arch -ExeName $entry.Exe
    $msix = Join-Path $OutDirPath ("Nalmeok-{0}-windows-{1}.msix" -f $Version, $entry.Arch)
    & $MakeAppx pack /o /d $layout /p $msix
    if ($LASTEXITCODE -ne 0) {
        throw "makeappx pack 실패: $($entry.Arch) (종료 코드 $LASTEXITCODE)"
    }
    $msixFiles += $msix
}

$bundleDir = Join-Path $LayoutRoot "bundle"
if (Test-Path $bundleDir) {
    Remove-Item $bundleDir -Recurse -Force
}
New-Item -ItemType Directory -Path $bundleDir -Force | Out-Null
foreach ($msix in $msixFiles) {
    Copy-Item $msix $bundleDir
}

$bundlePath = Join-Path $OutDirPath ("Nalmeok-{0}-windows.msixbundle" -f $Version)
& $MakeAppx bundle /o /bv $PackageVersion /d $bundleDir /p $bundlePath
if ($LASTEXITCODE -ne 0) {
    throw "makeappx bundle 실패 (종료 코드 $LASTEXITCODE)"
}

Write-Host "만들었다:"
$msixFiles | ForEach-Object { Write-Host "  $_" }
Write-Host "  $bundlePath"
Write-Host "서명하지 않았다. Store 제출물은 이 번들이다."
