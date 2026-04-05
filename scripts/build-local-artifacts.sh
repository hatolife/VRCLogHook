#!/usr/bin/env bash
set -euo pipefail

OUT_DIR="${1:-dist}"
mkdir -p "$OUT_DIR"

build_one() {
  local goos="$1"
  local goarch="$2"
  local ext="$3"
  echo "[build] $goos/$goarch"
  GOOS="$goos" GOARCH="$goarch" go build -trimpath -ldflags="-s -w" \
    -o "$OUT_DIR/vrc-loghook-${goos}-${goarch}${ext}" ./core/cmd/vrc-loghook
  GOOS="$goos" GOARCH="$goarch" go build -trimpath -ldflags="-s -w" \
    -o "$OUT_DIR/vrc-loghook-gui-${goos}-${goarch}${ext}" ./gui/cmd/vrc-loghook-gui
}

build_one linux amd64 ""
build_one darwin amd64 ""
build_one windows amd64 ".exe"

(
  cd "$OUT_DIR"
  sha256sum * > SHA256SUMS.txt
)

echo "Artifacts created in: $OUT_DIR"
