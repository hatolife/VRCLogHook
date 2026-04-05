#!/usr/bin/env bash
set -euo pipefail

if [[ "${GITHUB_ACTIONS:-}" == "true" ]]; then
  echo "Do not run this integration test on GitHub Actions."
  exit 1
fi

if [[ -f ./.local/webhook.env ]]; then
  # shellcheck disable=SC1091
  source ./.local/webhook.env
fi

if [[ -z "${DISCORD_WEBHOOK_URL:-}" ]]; then
  echo "DISCORD_WEBHOOK_URL is not set."
  echo "Set it in ./.local/webhook.env or export it in your shell."
  exit 1
fi

GOCACHE=${GOCACHE:-/tmp/vrc-go-cache} go test -tags=integration ./core/internal/notify -run TestDiscordWebhookIntegration -v
