# AGENTS.md

This file defines working rules for AI/code agents in this repository.

## Project Intent
- Build `VRC LogHook` tooling that monitors local VRChat logs and sends notifications.
- Do not use VRChat official API.
- Do not implement VRChat account login/password handling.
- Keep behavior local-log-driven and transparent.

## Top Priority Policy
Always optimize in this order, with explicit tradeoff notes when needed:
1. Transparency
2. Reliability
3. Security
4. Low runtime cost

## Architecture Guardrails
- Keep `core/` and `gui/` separated.
- Core executable: `vrc-loghook`
- GUI executable: `vrc-loghook-gui`
- GUI must be replaceable without rewriting core.
- Keep GUI-core contract stable and versioned.

## Security/Trust Rules
- Mask secrets in logs and UI.
- Validate config schema strictly.
- Keep dangerous hooks opt-in and clearly warned.
- Require explicit consent before enabling hook execution features.
- Prefer safe defaults; document all externally sent fields.

## Config Rules
- Support JSON/HJSON.
- Auto-generate config with defaults if missing.
- Prefer exposing settings (do not hide without strong reason).
- Mark restart-required settings clearly (e.g. `*` in GUI).
- Use UTF-8 for config and docs.

## Runtime Rules
- Monitor append-only new lines after startup behavior is resolved by saved offsets.
- Follow log rotation automatically.
- Support dry-run mode.
- Persist operational state safely.

## AI Self-Audit (Required)
At these checkpoints, run a short policy audit and record results in Markdown:
- After design decisions
- After each major implementation chunk
- Before commit/PR

Audit must include:
- Evidence for Transparency / Reliability / Security / Low runtime cost
- Remaining risks
- Next corrective action if any policy is not sufficiently met

## Delivery Rules
- Keep README/SECURITY/PRIVACY/LICENSE updated with behavior changes.
- CI/CD must run build/test/lint before release.
- Tag-based release pattern: `v*`.

## Test-First Rules
- Development must follow a strict test-first cycle.
- Start from a small failing test for one behavior.
- Implement the minimum code to pass the test.
- Refactor after tests pass while keeping tests green.
- Repeat in small increments; avoid large untested changes.

## Git Workflow Rules
- Do not work directly on `main` except for emergency hotfix coordination.
- Start each task on a dedicated branch and merge back via PR.
- Branch naming (git-flow style):
- `feature/<short-topic>` for new features
- `fix/<short-topic>` for bug fixes
- `release/<version>` for release stabilization
- `hotfix/<short-topic>` for urgent production fixes
- Keep commits scoped and descriptive per branch purpose.
- Before opening PR: rebase or merge latest `main`, then run required checks.
