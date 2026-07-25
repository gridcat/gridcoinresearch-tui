#!/usr/bin/env bash
#
# Verifies every release binary fits the self-updater's download cap.
#
# applyUpdate() in selfupdate.go refuses to download an asset larger than
# maxDownloadBytes, so a release that outgrows that cap would install for
# nobody. The release workflow runs this before semantic-release tags anything
# and before GoReleaser builds anything, so an over-cap build aborts the
# release instead of shipping.
#
# The cap is read out of selfupdate.go rather than duplicated here, so raising
# it there is the only edit needed.
#
# Usage: scripts/check-download-cap.sh   (exit 0 = every target fits)
set -euo pipefail

cd "$(dirname "$0")/.."

cap_mib=$(grep -oP 'maxDownloadBytes = \K\d+(?= << 20)' selfupdate.go || true)
if [ -z "$cap_mib" ]; then
  echo "::error::cannot parse maxDownloadBytes from selfupdate.go"
  exit 1
fi
cap=$((cap_mib << 20))
status=0

# Mirrors .goreleaser.yaml: same targets, same CGO/ldflags settings.
for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64; do
  out=$(mktemp)
  CGO_ENABLED=0 GOOS=${target%/*} GOARCH=${target#*/} \
    go build -ldflags="-s -w" -o "$out" .
  size=$(stat -c %s "$out")
  rm -f "$out"
  printf '%-14s %s bytes (%s MiB) of %s MiB cap\n' "$target" "$size" \
    "$(LC_ALL=C awk -v b="$size" 'BEGIN{printf "%.1f", b/1048576}')" "$cap_mib"
  if [ "$size" -gt "$cap" ]; then
    echo "::error::$target binary is $size bytes, over the ${cap_mib} MiB maxDownloadBytes cap in selfupdate.go — raise the cap"
    status=1
  fi
done

exit $status
