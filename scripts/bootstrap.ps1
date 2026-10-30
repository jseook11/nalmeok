param(
    [switch]$Ffmpeg
)

$ErrorActionPreference = 'Stop'
$Here = $PSScriptRoot
$Data = Join-Path $env:LOCALAPPDATA 'nalmeok'
$RuntimeDir = Join-Path $Data 'runtime'
$AppDir = Join-Path $Data 'app'
$BinDir = Join-Path $Data 'bin'
New-Item -ItemType Directory -Force -Path $RuntimeDir, $AppDir, $BinDir | Out-Null

function Read-EnvFile([string]$Path) {
    $values = @{}
    foreach ($line in Get-Content -LiteralPath $Path) {
        $line = $line.Trim()
        if (-not $line -or $line.StartsWith('#')) { continue }
        $index = $line.IndexOf('=')
        if ($index -lt 1) { continue }
        $values[$line.Substring(0, $index)] = $line.Substring($index + 1)
    }
    return $values
}

function Assert-Hash([string]$Path, [string]$Expected) {
    $sha = [System.Security.Cryptography.SHA256]::Create()
    try {
        $stream = [System.IO.File]::OpenRead($Path)
        try {
            $bytes = $sha.ComputeHash($stream)
        } finally {
            $stream.Dispose()
        }
    } finally {
        $sha.Dispose()
    }
    $actual = ([System.BitConverter]::ToString($bytes) -replace '-', '').ToLowerInvariant()
    if ($actual -ne $Expected.ToLowerInvariant()) {
        throw "checksum mismatch: $actual"
    }
}

function Invoke-Download([string]$Url, [string]$Destination) {
    if (Get-Command curl.exe -ErrorAction SilentlyContinue) {
        & curl.exe -fL --retry 3 --progress-bar -o $Destination $Url
        if ($LASTEXITCODE -ne 0) { throw "download failed (curl $LASTEXITCODE)" }
    } else {
        $client = New-Object System.Net.WebClient
        try {
            $client.DownloadFile($Url, $Destination)
        } finally {
            $client.Dispose()
        }
    }
}

$envFile = Read-EnvFile (Join-Path $Here 'bootstrap.env')

if ($Ffmpeg) {
    $ffmpeg = Join-Path $BinDir 'ffmpeg.exe'
    $ffprobe = Join-Path $BinDir 'ffprobe.exe'
    if ((Test-Path -LiteralPath $ffmpeg) -and (Test-Path -LiteralPath $ffprobe)) {
        exit 0
    }
    Write-Host ""
    Write-Host "Downloading FFmpeg (about 160 MB)..."
    $zip = Join-Path $env:TEMP ("nalmeok-ffmpeg-" + [guid]::NewGuid().ToString('N') + ".zip")
    $extract = Join-Path $env:TEMP ("nalmeok-ffmpeg-" + [guid]::NewGuid().ToString('N'))
    try {
        Invoke-Download $envFile['FFMPEG_URL'] $zip
        Write-Host "Verifying..."
        Assert-Hash $zip $envFile['FFMPEG_SHA256']
        Add-Type -AssemblyName System.IO.Compression.FileSystem
        New-Item -ItemType Directory -Force -Path $extract | Out-Null
        [System.IO.Compression.ZipFile]::ExtractToDirectory($zip, $extract)
        $ffmpegSource = Get-ChildItem -LiteralPath $extract -Recurse -Filter ffmpeg.exe | Select-Object -First 1
        $ffprobeSource = Get-ChildItem -LiteralPath $extract -Recurse -Filter ffprobe.exe | Select-Object -First 1
        if (-not $ffmpegSource -or -not $ffprobeSource) { throw "ffmpeg.exe/ffprobe.exe not found" }
        Copy-Item -LiteralPath $ffmpegSource.FullName -Destination $ffmpeg -Force
        Copy-Item -LiteralPath $ffprobeSource.FullName -Destination $ffprobe -Force
        Write-Host "FFmpeg is ready."
        exit 0
    } catch {
        Write-Host ("Error: " + $_.Exception.Message) -ForegroundColor Red
        exit 1
    } finally {
        Remove-Item -LiteralPath $zip -Force -ErrorAction SilentlyContinue
        Remove-Item -LiteralPath $extract -Recurse -Force -ErrorAction SilentlyContinue
    }
}

$current = Join-Path $RuntimeDir 'current.txt'
if (Test-Path -LiteralPath $current) {
    $version = (Get-Content -LiteralPath $current -TotalCount 1).Trim()
    $python = Join-Path $RuntimeDir (Join-Path $version 'python.exe')
    if ($version -and (Test-Path -LiteralPath $python)) {
        & $python -c "import sys" 2>$null
        if ($LASTEXITCODE -eq 0) { exit 0 }
        Remove-Item -LiteralPath $current -Force -ErrorAction SilentlyContinue
    }
}

$seed = Join-Path $Here 'nalmeok.py'
$app = Join-Path $AppDir 'nalmeok.py'
if ((Test-Path -LiteralPath $seed) -and -not (Test-Path -LiteralPath $app)) {
    Copy-Item -LiteralPath $seed -Destination $app
}

$pbsVersion = $envFile['PBS_VERSION']
Write-Host ""
Write-Host "First run: downloading Python $pbsVersion (about 25 MB)..."
$archive = Join-Path $env:TEMP ("nalmeok-python-" + [guid]::NewGuid().ToString('N') + ".tar.gz")
$extract = Join-Path $RuntimeDir (".extract-" + [guid]::NewGuid().ToString('N'))
try {
    Invoke-Download $envFile['PBS_URL'] $archive
    Write-Host "Verifying..."
    Assert-Hash $archive $envFile['PBS_SHA256']
    Write-Host "Installing..."
    New-Item -ItemType Directory -Force -Path $extract | Out-Null
    & tar.exe -xzf $archive -C $extract
    if ($LASTEXITCODE -ne 0) { throw "extract failed (tar $LASTEXITCODE)" }
    $source = Join-Path $extract 'python'
    if (-not (Test-Path -LiteralPath (Join-Path $source 'python.exe'))) {
        throw "python.exe not found in archive"
    }
    $destination = Join-Path $RuntimeDir $pbsVersion
    if (Test-Path -LiteralPath $destination) {
        Remove-Item -LiteralPath $destination -Recurse -Force
    }
    Move-Item -LiteralPath $source -Destination $destination
    Set-Content -LiteralPath $current -Value $pbsVersion -Encoding ASCII
    Write-Host "Python $pbsVersion is ready."
} catch {
    Write-Host ("Error: " + $_.Exception.Message) -ForegroundColor Red
    exit 1
} finally {
    Remove-Item -LiteralPath $archive -Force -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $extract -Recurse -Force -ErrorAction SilentlyContinue
}
