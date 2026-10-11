param([switch]$DesktopTest)

$ErrorActionPreference = 'Stop'
Set-Location (Split-Path $PSScriptRoot -Parent)
if ([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT) {
    throw 'Build the Windows desktop package on Windows.'
}
$loopbeeGo = if (Test-Path '.tools/go/bin/go.exe') { (Resolve-Path '.tools/go/bin/go.exe').Path } else { 'go' }
$loopbeeArch = & $loopbeeGo env GOARCH
if ($LASTEXITCODE -or $loopbeeArch -ne 'amd64') { throw 'This package targets Windows x64 (GOARCH=amd64).' }
$loopbeeOS = & $loopbeeGo env GOOS
if ($LASTEXITCODE -or $loopbeeOS -ne 'windows') { throw 'GOOS must be windows.' }
New-Item -ItemType Directory -Path bin -Force | Out-Null

function Assert-Executable([string]$Path, [int]$Subsystem, [bool]$RequireResources) {
    $bytes = [IO.File]::ReadAllBytes((Resolve-Path $Path))
    $pe = [BitConverter]::ToInt32($bytes, 0x3c)
    if ([BitConverter]::ToUInt32($bytes, $pe) -ne 0x4550 -or
        [BitConverter]::ToUInt16($bytes, $pe + 4) -ne 0x8664) { throw "Invalid x64 PE: $Path" }
    $optional = $pe + 24
    if ([BitConverter]::ToUInt16($bytes, $optional + 68) -ne $Subsystem) { throw "Incorrect PE subsystem: $Path" }
    if (-not $RequireResources) { return }
    $resourceRVA = [BitConverter]::ToUInt32($bytes, $optional + 128)
    $sectionCount = [BitConverter]::ToUInt16($bytes, $pe + 6)
    $sections = $optional + [BitConverter]::ToUInt16($bytes, $pe + 20)
    $resourceOffset = 0
    for ($i = 0; $i -lt $sectionCount; $i++) {
        $section = $sections + $i * 40
        $rva = [BitConverter]::ToUInt32($bytes, $section + 12)
        $size = [BitConverter]::ToUInt32($bytes, $section + 16)
        if ($resourceRVA -ge $rva -and $resourceRVA -lt $rva + $size) {
            $resourceOffset = [BitConverter]::ToUInt32($bytes, $section + 20) + $resourceRVA - $rva
            break
        }
    }
    if (-not $resourceOffset) { throw "Missing PE resources: $Path" }
    $count = [BitConverter]::ToUInt16($bytes, $resourceOffset + 12) + [BitConverter]::ToUInt16($bytes, $resourceOffset + 14)
    $types = for ($i = 0; $i -lt $count; $i++) { [BitConverter]::ToUInt32($bytes, $resourceOffset + 16 + $i * 8) }
    foreach ($type in @(3, 14, 16, 24)) {
        if ($type -notin $types) { throw "Missing icon, version or manifest resource ($type): $Path" }
    }
    if ([Text.Encoding]::UTF8.GetString($bytes) -notmatch 'PerMonitorV2') { throw "Missing per-monitor DPI manifest: $Path" }
}

& $loopbeeGo build -trimpath -tags nogui -o bin/loopbee-nogui.exe ./cmd/loopbee
if ($LASTEXITCODE) { throw 'Windows nogui build failed' }
Assert-Executable 'bin/loopbee-nogui.exe' 3 $false
& $loopbeeGo build -trimpath -o bin/beectl.exe ./cmd/beectl
if ($LASTEXITCODE) { throw 'beectl build failed' }

$loopbeeResource = Join-Path (Get-Location) 'cmd/loopbee/loopbee_windows_amd64.syso'
if (Test-Path -LiteralPath $loopbeeResource) { throw "Refusing to overwrite existing build resource: $loopbeeResource" }
try {
    & $loopbeeGo run github.com/tc-hib/go-winres@v0.3.3 make --in scripts/windows/resources.json --arch amd64 --out cmd/loopbee/loopbee
    if ($LASTEXITCODE) { throw 'Windows resource generation failed' }
    & $loopbeeGo build -trimpath -tags production -ldflags '-H windowsgui' -o bin/loopbee.exe ./cmd/loopbee
    if ($LASTEXITCODE) { throw 'Windows desktop build failed' }
    if ($DesktopTest) {
        New-Item -ItemType Directory -Path .test-data -Force | Out-Null
        & $loopbeeGo build -trimpath -tags production,desktoptest -ldflags '-H windowsgui' -o .test-data/loopbee-desktop-test.exe ./cmd/loopbee
        if ($LASTEXITCODE) { throw 'Windows desktop test fixture build failed' }
    }
} finally {
    if (Test-Path -LiteralPath $loopbeeResource) { Remove-Item -LiteralPath $loopbeeResource }
}
Assert-Executable 'bin/loopbee.exe' 2 $true
if ($DesktopTest) { Assert-Executable '.test-data/loopbee-desktop-test.exe' 2 $true }

$loopbeeZip = Join-Path (Get-Location) 'bin/Loopbee-windows-amd64.zip'
Compress-Archive -LiteralPath @('bin/loopbee.exe', 'bin/loopbee-nogui.exe', 'bin/beectl.exe', 'scripts/windows/README.txt') -DestinationPath $loopbeeZip -Force
Add-Type -AssemblyName System.IO.Compression.FileSystem
$archive = [IO.Compression.ZipFile]::OpenRead($loopbeeZip)
try {
    $entries = @($archive.Entries | ForEach-Object { $_.FullName })
    if ($entries.Count -ne 4 -or 'loopbee.exe' -notin $entries -or 'loopbee-nogui.exe' -notin $entries -or 'beectl.exe' -notin $entries -or 'README.txt' -notin $entries) {
        throw 'Portable archive contains unexpected files.'
    }
} finally { $archive.Dispose() }
$hash = (Get-FileHash -LiteralPath $loopbeeZip -Algorithm SHA256).Hash.ToLowerInvariant()
Set-Content -LiteralPath "$loopbeeZip.sha256" -Value "$hash  Loopbee-windows-amd64.zip" -Encoding ASCII
Write-Output 'Built and validated bin/loopbee.exe, bin/loopbee-nogui.exe, bin/beectl.exe and bin/Loopbee-windows-amd64.zip (unsigned).'
