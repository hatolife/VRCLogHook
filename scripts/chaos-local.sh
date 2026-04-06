#!/usr/bin/env bash
set -euo pipefail

export GOCACHE="${GOCACHE:-/tmp/gocache-vrc}"

echo "[chaos] phase 1/3: invalid config auto-recovery"
go test ./core/internal/config -run TestLoadOrCreateRecoversInvalidConfig -count=1

echo "[chaos] phase 2/3: disabled rule isolation"
go test ./core/internal/matcher -run TestCompileSkipsDisabledRules -count=1

echo "[chaos] phase 3/3: hot-reload pressure does not starve polling"
go test ./core/internal/app -run TestServicePollNotStarvedByFrequentReload -count=1

echo "[chaos] all experiments passed"
