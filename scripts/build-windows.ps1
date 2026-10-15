# -Version 1.2.0 (e.g. from a v1.2.0 tag) is stamped into the binaries and the file version resource.
param([switch]$DesktopTest, [string]$Version = '')

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
# Argument arrays, because Windows PowerShell 5.1 drops empty string arguments.
$loopbeeLdflags = if ($Version) { "-X loopbee/internal/core.Version=$Version" } else { '' }
# LOOPBEE_REPO (owner/name, set by the release workflow) is where the app looks for its updates.
if ($env:LOOPBEE_REPO) { $loopbeeLdflags = ("$loopbeeLdflags -X loopbee/internal/update.Repo=$($env:LOOPBEE_REPO)").Trim() }
$loopbeeLdArgs = if ($loopbeeLdflags) { @('-ldflags', $loopbeeLdflags) } else { @() }
$loopbeeGuiLdflags = ('-H windowsgui ' + $loopbeeLdflags).Trim()
$loopbeeResArgs = @()
if ($Version -match '^(\d+)\.(\d+)\.(\d+)') {
    $fileVersion = "$($Matches[1]).$($Matches[2]).$($Matches[3]).0"
    $loopbeeResArgs = @('--file-version', $fileVersion, '--product-version', $fileVersion)
}

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

& $loopbeeGo build -trimpath -tags nogui @loopbeeLdArgs -o bin/loopbee-nogui.exe ./cmd/loopbee
if ($LASTEXITCODE) { throw 'Windows nogui build failed' }
Assert-Executable 'bin/loopbee-nogui.exe' 3 $false
& $loopbeeGo build -trimpath @loopbeeLdArgs -o bin/beectl.exe ./cmd/beectl
if ($LASTEXITCODE) { throw 'beectl build failed' }

$loopbeeResource = Join-Path (Get-Location) 'cmd/loopbee/loopbee_windows_amd64.syso'
if (Test-Path -LiteralPath $loopbeeResource) { throw "Refusing to overwrite existing build resource: $loopbeeResource" }
try {
    & $loopbeeGo run github.com/tc-hib/go-winres@v0.3.3 make --in scripts/windows/resources.json --arch amd64 --out cmd/loopbee/loopbee @loopbeeResArgs
    if ($LASTEXITCODE) { throw 'Windows resource generation failed' }
    & $loopbeeGo build -trimpath -tags production -ldflags $loopbeeGuiLdflags -o bin/loopbee.exe ./cmd/loopbee
    if ($LASTEXITCODE) { throw 'Windows desktop build failed' }
    if ($DesktopTest) {
        New-Item -ItemType Directory -Path .test-data -Force | Out-Null
        & $loopbeeGo build -trimpath -tags production,desktoptest -ldflags $loopbeeGuiLdflags -o .test-data/loopbee-desktop-test.exe ./cmd/loopbee
        if ($LASTEXITCODE) { throw 'Windows desktop test fixture build failed' }
    }
} finally {
    if (Test-Path -LiteralPath $loopbeeResource) { Remove-Item -LiteralPath $loopbeeResource }
}
Assert-Executable 'bin/loopbee.exe' 2 $true
if ($DesktopTest) { Assert-Executable '.test-data/loopbee-desktop-test.exe' 2 $true }

$loopbeeZip = Join-Path (Get-Location) 'bin/loopbee-windows-amd64.zip'
Compress-Archive -LiteralPath @('bin/loopbee.exe', 'bin/loopbee-nogui.exe', 'bin/beectl.exe', 'scripts/windows/README.txt') -DestinationPath $loopbeeZip -Force
Add-Type -AssemblyName System.IO.Compression.FileSystem
$archive = [IO.Compression.ZipFile]::OpenRead($loopbeeZip)
try {
    $entries = @($archive.Entries | ForEach-Object { $_.FullName })
    if ($entries.Count -ne 4 -or 'loopbee.exe' -notin $entries -or 'loopbee-nogui.exe' -notin $entries -or 'beectl.exe' -notin $entries -or 'README.txt' -notin $entries) {
        throw 'Portable archive contains unexpected files.'
    }
} finally { $archive.Dispose() }
# Release downloads: the portable ZIP (desktop, headless and beectl) and the desktop program itself as a
# directly runnable .exe (not an installer). Each gets a .sha256 file; the release adds a combined SHA256SUMS.
$loopbeeExe = Join-Path (Get-Location) 'bin/loopbee-windows-amd64.exe'
Copy-Item -LiteralPath 'bin/loopbee.exe' -Destination $loopbeeExe -Force
Assert-Executable $loopbeeExe 2 $true
foreach ($file in @($loopbeeZip, $loopbeeExe)) {
    $hash = (Get-FileHash -LiteralPath $file -Algorithm SHA256).Hash.ToLowerInvariant()
    $name = Split-Path $file -Leaf
    [IO.File]::WriteAllText("$file.sha256", "$hash  $name`n", [Text.Encoding]::ASCII)
}
Write-Output 'Built and validated bin/loopbee.exe, bin/loopbee-nogui.exe, bin/beectl.exe, bin/loopbee-windows-amd64.exe and bin/loopbee-windows-amd64.zip (unsigned).'
