#!/usr/bin/env bash
# Installs, upgrades, and removes the Debian packages in a clean container of
# each supported distribution, checking that the packages hold what they claim
# and that per-user driver state survives removal.
#
#   ./packaging/linux/test-packages.sh [image ...]
#
# Without arguments it tests debian:stable and ubuntu:24.04. Needs Docker.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
images=("$@")
if [[ ${#images[@]} -eq 0 ]]; then
  images=(debian:stable ubuntu:24.04)
fi

dist="$(mktemp -d)"
trap 'rm -rf "$dist"' EXIT
"$root/packaging/linux/build-deb.sh" 0.0.1 "$dist/old" >/dev/null
"$root/packaging/linux/build-deb.sh" 0.0.2 "$dist/new" >/dev/null

# The checks run inside the container as root, then as an ordinary user.
read -r -d '' checks <<'SCRIPT' || true
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
fail() { echo "FAIL: $*" >&2; exit 1; }
apt-get update -qq
apt-get install -y -qq /dist/old/*.deb >/dev/null
useradd --create-home tester
as_user() { su tester -c "$*"; }

[[ "$(as_user 'bt --version')" == *v0.0.1* ]] || fail "bt --version after install"
drivers="$(as_user 'bt --json driver list')"
[[ "$drivers" == *official/apple-audio* && "$drivers" == *official/sony-headphones* ]] || fail "bundled drivers not listed: $drivers"
as_user 'bt driver remove official/sony-headphones' 2>/dev/null && fail "a package-managed driver was removable by bt"

# Per-user state for a bundled driver, as Betty writes it.
state=/home/tester/.config/betty/drivers/b2ZmaWNpYWwvYXBwbGUtYXVkaW8/storage.json
as_user "mkdir -p $(dirname "$state") && echo '{\"keys\":\"kept\"}' > $state"

apt-get install -y -qq /dist/new/*.deb >/dev/null
[[ "$(as_user 'bt --version')" == *v0.0.2* ]] || fail "bt --version after upgrade"
[[ "$(cat /usr/lib/betty/drivers/*/manifest.json)" == *v0.0.2* ]] || fail "driver manifests after upgrade"
[[ "$(cat /usr/lib/betty/drivers/*/manifest.json)" != *v0.0.1* ]] || fail "an old driver manifest survived the upgrade"

apt-get remove -y -qq betty-sony >/dev/null
drivers="$(as_user 'bt --json driver list')"
[[ "$drivers" != *official/sony-headphones* ]] || fail "sony driver listed after removal"
[[ "$drivers" == *official/apple-audio* ]] || fail "apple driver missing after removing sony"

apt-get purge -y -qq betty-apple-audio betty >/dev/null
[[ ! -e /usr/bin/bt ]] || fail "/usr/bin/bt left after purge"
[[ ! -e /usr/lib/betty ]] || fail "/usr/lib/betty left after purge"
[[ -f "$state" ]] || fail "per-user driver state was removed with the package"
echo "packages OK on $(. /etc/os-release && echo "$PRETTY_NAME")"
SCRIPT

for image in "${images[@]}"; do
  docker run --rm -v "$dist:/dist:ro" "$image" bash -c "$checks"
done
