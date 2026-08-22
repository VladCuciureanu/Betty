#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 3 ]]; then
  echo "usage: render-formulas.sh <version> <source-sha256> <tap-formula-dir>" >&2
  exit 2
fi

version="${1#v}"
sha256="$2"
destination="$3"
root="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

if [[ ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$ ]]; then
  echo "invalid version: $version" >&2
  exit 2
fi
if [[ ! "$sha256" =~ ^[0-9a-f]{64}$ ]]; then
  echo "source SHA-256 must contain exactly 64 hexadecimal characters" >&2
  exit 2
fi

mkdir -p "$destination"
for formula in betty-bluetooth betty-apple-audio betty-sony; do
  sed -e "s/@@VERSION@@/$version/g" -e "s/@@SHA256@@/$sha256/g" "$root/$formula.rb.tmpl" > "$destination/$formula.rb"
done
