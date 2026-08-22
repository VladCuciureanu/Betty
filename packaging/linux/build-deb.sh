#!/usr/bin/env bash
set -euo pipefail

if [[ "$(uname -s)" != "Linux" ]]; then
  echo "build-deb.sh must run on Linux" >&2
  exit 1
fi
for tool in go dpkg-deb; do
  command -v "$tool" >/dev/null || { echo "$tool is required" >&2; exit 1; }
done

version="${1#v}"
output="${2:-$PWD/dist}"
architecture="${BETTY_DEB_ARCH:-amd64}"
case "$architecture" in
  amd64) goarch=amd64 ;;
  arm64) goarch=arm64 ;;
  *) echo "unsupported Debian architecture: $architecture" >&2; exit 1 ;;
esac

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
base="$work/betty"
advanced="$work/betty-apple-audio"
sony="$work/betty-sony"
driver_dir="$advanced/usr/lib/betty/drivers/b2ZmaWNpYWwvYXBwbGUtYXVkaW8"
sony_driver_dir="$sony/usr/lib/betty/drivers/b2ZmaWNpYWwvc29ueS1oZWFkcGhvbmVz"
mkdir -p "$output" "$base/DEBIAN" "$base/usr/bin" "$advanced/DEBIAN" "$driver_dir" "$sony/DEBIAN" "$sony_driver_dir"
mkdir -p "$base/usr/share/doc/betty" "$advanced/usr/share/doc/betty-apple-audio" "$sony/usr/share/doc/betty-sony"

for package_doc in "$base/usr/share/doc/betty" "$advanced/usr/share/doc/betty-apple-audio" "$sony/usr/share/doc/betty-sony"; do
  install -m 0644 "$root/LICENSE" "$package_doc/copyright"
done

(
  cd "$root"
  GOOS=linux GOARCH="$goarch" CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=v$version" -o "$base/usr/bin/bt" ./cmd/bt
  GOOS=linux GOARCH="$goarch" CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=v$version" -o "$driver_dir/driver" ./cmd/bt-driver-apple-audio
  GOOS=linux GOARCH="$goarch" CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=v$version" -o "$sony_driver_dir/driver" ./cmd/bt-driver-sony
)

install -m 0644 "$root/packaging/linux/apple-audio-manifest.json" "$driver_dir/manifest.json"
sed -i "s/\"version\": \"bundled\"/\"version\": \"v$version\"/" "$driver_dir/manifest.json"
install -m 0644 "$root/packaging/linux/sony-manifest.json" "$sony_driver_dir/manifest.json"
sed -i "s/\"version\": \"bundled\"/\"version\": \"v$version\"/" "$sony_driver_dir/manifest.json"

cat >"$base/DEBIAN/control" <<EOF
Package: betty
Version: $version
Section: utils
Priority: optional
Architecture: $architecture
Depends: bluez, dbus
Maintainer: Betty contributors
Description: Small cross-platform Bluetooth command-line interface
 Betty provides scanning, pairing, connections, and generic BLE/GATT access.
EOF

cat >"$advanced/DEBIAN/control" <<EOF
Package: betty-apple-audio
Version: $version
Section: utils
Priority: optional
Architecture: $architecture
Depends: betty (= $version)
Maintainer: Betty contributors
Replaces: betty-airpods
Conflicts: betty-airpods
Provides: betty-airpods
Description: Optional Apple and Beats audio support for Betty
 Adds passive battery decoding and Classic L2CAP transport for exact
 component battery readings. The base Betty package contains no Apple audio code.
EOF
cat >"$sony/DEBIAN/control" <<EOF
Package: betty-sony
Version: $version
Section: utils
Priority: optional
Architecture: $architecture
Depends: betty (= $version)
Maintainer: Betty contributors
Description: Optional Sony headphones support for Betty
 Adds Sony v1/v2 identification, RFCOMM protocol support, and exact v2 battery
 readings. The base Betty package contains no Sony protocol implementation.
EOF

dpkg-deb --root-owner-group --build "$base" "$output/betty_${version}_${architecture}.deb"
dpkg-deb --root-owner-group --build "$advanced" "$output/betty-apple-audio_${version}_${architecture}.deb"
dpkg-deb --root-owner-group --build "$sony" "$output/betty-sony_${version}_${architecture}.deb"
printf '%s\n' "$output/betty_${version}_${architecture}.deb" "$output/betty-apple-audio_${version}_${architecture}.deb" "$output/betty-sony_${version}_${architecture}.deb"
