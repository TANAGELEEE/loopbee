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
# Release downloads, named by CPU architecture (arm64 = Apple Silicon, amd64 = Intel):
#   Loopbee-macos-<arch>.dmg  disk image with Loopbee.app and an Applications shortcut (drag to install)
#   Loopbee-macos-<arch>.zip  the same .app as a plain archive
arch=$(go env GOARCH)
actual=$(lipo -archs "$app/Contents/MacOS/loopbee")
case "$arch:$actual" in
  arm64:arm64|amd64:x86_64) ;;
  *) echo "Loopbee.app was built for $actual, expected $arch" >&2; exit 1 ;;
esac
zip="bin/Loopbee-macos-$arch.zip"
rm -f "$zip"
ditto -c -k --keepParent "$app" "$zip"
dmg="bin/Loopbee-macos-$arch.dmg"
stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT
ditto "$app" "$stage/Loopbee.app"
ln -s /Applications "$stage/Applications"
rm -f "$dmg"
hdiutil create -quiet -volname "Loopbee" -srcfolder "$stage" -fs HFS+ -format UDZO -ov "$dmg"
# Verify the image: checksum, then mount it read-only and check its contents.
hdiutil verify -quiet "$dmg"
mnt="$stage/mnt"
mkdir "$mnt"
hdiutil attach -quiet -nobrowse -readonly -mountpoint "$mnt" "$dmg"
ok=1
[ -x "$mnt/Loopbee.app/Contents/MacOS/loopbee" ] && [ -x "$mnt/Loopbee.app/Contents/MacOS/beectl" ] && [ -L "$mnt/Applications" ] || ok=0
hdiutil detach -quiet "$mnt"
[ "$ok" = 1 ] || { echo "$dmg does not contain Loopbee.app and the Applications shortcut" >&2; exit 1; }
for f in "$zip" "$dmg"; do
  (cd bin && shasum -a 256 "$(basename "$f")" > "$(basename "$f").sha256")
done
echo "Built $app ${version:-dev} for $arch (unsigned; not notarized), $dmg, $zip and bin/beectl. Put beectl on your PATH, e.g. ln -s \"$PWD/bin/beectl\" /usr/local/bin/beectl, then run: beectl skill install"
