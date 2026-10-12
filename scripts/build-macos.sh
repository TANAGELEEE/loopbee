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
# LOOPBEE_VERSION (e.g. 1.2.0 from a v1.2.0 tag) is stamped into the binaries and Info.plist.
version="${LOOPBEE_VERSION:-}"
ldflags=""
[ -n "$version" ] && ldflags="-X loopbee/internal/core.Version=$version"
app="bin/Loopbee.app"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
go build -trimpath -ldflags "$ldflags" -o "$app/Contents/MacOS/loopbee" ./cmd/loopbee
go build -trimpath -ldflags "$ldflags" -o "$app/Contents/MacOS/beectl" ./cmd/beectl
go build -trimpath -ldflags "$ldflags" -o bin/beectl ./cmd/beectl
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
<key>CFBundleVersion</key><string>__VERSION__</string>
<key>CFBundleShortVersionString</key><string>__VERSION__</string>
<key>NSHighResolutionCapable</key><true/>
<key>LSMinimumSystemVersion</key><string>12.0</string>
</dict></plist>
PLIST
plain_version=$(printf '%s' "$version" | sed 's/[^0-9.].*$//')
sed -i '' "s/__VERSION__/${plain_version:-0.1.0}/g" "$app/Contents/Info.plist"
# Portable archive for CI artifacts and releases (ditto keeps the bundle's symlinks and permissions).
zip="bin/Loopbee-macos-$(uname -m).zip"
rm -f "$zip"
ditto -c -k --keepParent "$app" "$zip"
(cd bin && shasum -a 256 "$(basename "$zip")" > "$(basename "$zip").sha256")
echo "Built $app ${version:-dev} (unsigned; not notarized), $zip and bin/beectl. Put beectl on your PATH, e.g. ln -s \"$PWD/bin/beectl\" /usr/local/bin/beectl, then run: beectl skill install"
