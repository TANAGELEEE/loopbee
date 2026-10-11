$ErrorActionPreference = 'Stop'
Set-Location (Split-Path $PSScriptRoot -Parent)
$loopbeeGo = if (Test-Path '.tools/go/bin/go.exe') { (Resolve-Path '.tools/go/bin/go.exe').Path } else { 'go' }
& $loopbeeGo test ./...
if ($LASTEXITCODE) { throw 'Go tests failed' }
& $loopbeeGo vet ./...
if ($LASTEXITCODE) { throw 'Go vet failed' }
& $loopbeeGo test -tags desktoptest ./cmd/loopbee -run TestDesktopBrowserArgs
if ($LASTEXITCODE) { throw 'Desktop test argument checks failed' }
& ./scripts/build-windows.ps1 -DesktopTest
npm test
if ($LASTEXITCODE) { throw 'Frontend tests failed' }
npm run test:e2e
if ($LASTEXITCODE) { throw 'Browser tests failed' }
$loopbeePreviousDesktopExe = $env:LOOPBEE_DESKTOP_EXE
$loopbeePreviousNativeExe = $env:LOOPBEE_NATIVE_TEST_BINARY
try {
    $env:LOOPBEE_NATIVE_TEST_BINARY = (Resolve-Path 'bin/loopbee.exe').Path
    & $loopbeeGo test ./cmd/loopbee -run TestNativeDesktopLifecycle -v -count=1 -timeout 90s
    if ($LASTEXITCODE) { throw 'Windows native lifecycle tests failed' }
    $env:LOOPBEE_DESKTOP_EXE = (Resolve-Path '.test-data/loopbee-desktop-test.exe').Path
    npm run test:desktop
    if ($LASTEXITCODE) { throw 'Windows desktop tests failed' }
} finally {
    $env:LOOPBEE_DESKTOP_EXE = $loopbeePreviousDesktopExe
    $env:LOOPBEE_NATIVE_TEST_BINARY = $loopbeePreviousNativeExe
}
