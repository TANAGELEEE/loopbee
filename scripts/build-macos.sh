#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
if [ "$(uname -s)" != Darwin ]; then
  echo 'The Wails desktop application must be built on macOS with Xcode Command Line Tools.' >&2
  exit 1
fi
# Match cgo objects to the Go linker's deployment target (avoids "built for newer macOS" linker warnings).
export MACOSX_DEPLOYMENT_TARGET="${MACOSX_DEPLOYMENT_TARGET:-11.0}"
export CGO_CFLAGS="${CGO_CFLAGS:--O2 -g} -mmacosx-version-min=$MACOSX_DEPLOYMENT_TARGET"
export CGO_LDFLAGS="${CGO_LDFLAGS:--O2 -g} -mmacosx-version-min=$MACOSX_DEPLOYMENT_TARGET"
app="bin/Loopbee.app"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
go build -trimpath -o "$app/Contents/MacOS/loopbee" ./cmd/loopbee
go build -trimpath -o "$app/Contents/MacOS/beectl" ./cmd/beectl
go build -trimpath -o bin/beectl ./cmd/beectl
cp internal/ui/web/bee.svg "$app/Contents/Resources/bee.svg"
iconset="bin/loopbee.iconset"
mkdir -p "$iconset"
for size in 16 32 128 256 512; do
  sips -z "$size" "$size" internal/ui/web/bee.png --out "$iconset/icon_${size}x${size}.png" >/dev/null
  double=$((size * 2))
  sips -z "$double" "$double" internal/ui/web/bee.png --out "$iconset/icon_${size}x${size}@2x.png" >/dev/null
done
iconutil -c icns "$iconset" -o "$app/Contents/Resources/loopbee.icns"
cat > "$app/Contents/Info.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleName</key><string>Loopbee</string>
<key>CFBundleDisplayName</key><string>Loopbee</string>
<key>CFBundleIdentifier</key><string>app.loopbee.desktop</string>
<key>CFBundleExecutable</key><string>loopbee</string>
<key>CFBundleIconFile</key><string>loopbee.icns</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>CFBundleVersion</key><string>0.1.0</string>
<key>CFBundleShortVersionString</key><string>0.1.0</string>
<key>NSHighResolutionCapable</key><true/>
<key>LSMinimumSystemVersion</key><string>12.0</string>
</dict></plist>
PLIST
echo "Built $app (unsigned; not notarized) and bin/beectl. Put beectl on your PATH, e.g. ln -s \"$PWD/bin/beectl\" /usr/local/bin/beectl, then run: beectl skill install"
