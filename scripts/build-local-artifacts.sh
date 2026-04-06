#!/usr/bin/env bash
set -euo pipefail

OUT_DIR="dist"
TARGETS="all"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --out-dir)
      OUT_DIR="${2:-}"
      shift 2
      ;;
    --target)
      TARGETS="${2:-}"
      shift 2
      ;;
    -*)
      echo "unknown option: $1" >&2
      echo "usage: $0 [--target all|windows|linux|darwin] [--out-dir DIR]" >&2
      exit 2
      ;;
    *)
      # Backward compatibility: positional out dir.
      OUT_DIR="$1"
      shift
      ;;
  esac
done

if [[ -z "$OUT_DIR" ]]; then
  echo "--out-dir requires a value" >&2
  exit 2
fi

case "$TARGETS" in
  all|windows|linux|darwin) ;;
  *)
    echo "--target must be one of: all, windows, linux, darwin" >&2
    exit 2
    ;;
esac

mkdir -p "$OUT_DIR"
REVISION="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
VERSION="$(git describe --tags --abbrev=0 2>/dev/null || echo dev)"
BUILD_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

build_one() {
  local goos="$1"
  local goarch="$2"
  local ext="$3"
  echo "[build] $goos/$goarch"
  local gui_out="$OUT_DIR/vrc-loghook-gui-${goos}-${goarch}${ext}"
  local core_out="$OUT_DIR/vrc-loghook-${goos}-${goarch}${ext}"
  local ldflags_common="-s -w -buildid= -X main.version=${VERSION} -X main.revision=${REVISION} -X main.buildTime=${BUILD_TIME}"
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build -trimpath -buildvcs=false -tags netgo -ldflags="${ldflags_common}" \
    -o "$gui_out" ./gui/cmd/vrc-loghook-gui
  local gui_hash
  gui_hash=$(sha256sum "$gui_out" | awk '{print $1}')
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build -trimpath -buildvcs=false -tags netgo -ldflags="${ldflags_common} -X main.expectedGUIHash=${gui_hash}" \
    -o "$core_out" ./core/cmd/vrc-loghook
}

if [[ "$TARGETS" == "all" || "$TARGETS" == "windows" ]]; then
  build_one windows amd64 ".exe"
fi
if [[ "$TARGETS" == "all" || "$TARGETS" == "linux" ]]; then
  build_one linux amd64 ""
fi
if [[ "$TARGETS" == "all" || "$TARGETS" == "darwin" ]]; then
  build_one darwin amd64 ""
fi

(
  cd "$OUT_DIR"
  sha256sum * > SHA256SUMS.txt
)

echo "Artifacts created in: $OUT_DIR"
