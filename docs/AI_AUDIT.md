# AI Self-Audit (2026-04-06)

## Checkpoint: Design
- Transparency: GUI差し替えリスクの対策範囲（検知中心、完全防御は非目標）を文書化する方針を採用。
- Reliability: 自動GUI起動を維持しつつ、識別失敗時は明示エラーで失敗理由を出す設計。
- Security: `--identity` 検証 + ハッシュ不一致警告（デフォルトON）を採用。
- Low runtime cost: 検証はGUI起動時のみ実行し、常駐処理には負荷を増やさない。

## Checkpoint: Implementation
- Transparency: `README.md` / `SECURITY.md` に対策内容と非目標を追記。
- Reliability: 既存自動起動フローを維持し、候補探索 + 識別チェックを追加。
- Security: 
  - `vrc-loghook-gui --identity` 実装
  - CLI起動前に identity 検証
  - リリース/ローカルビルドで GUI を先にビルドし、その SHA256 を core に埋め込み
  - 起動時ハッシュ不一致警告（`--gui-hash-warn=false` で抑止可）
- Low runtime cost: ハッシュ計算はGUI起動時に1回のみ。

## Checkpoint: Pre-PR
- Evidence:
  - テスト: `go test ./...` 成功
  - 生成: `./scripts/build-local-artifacts.sh` 成功
- Remaining risks:
  - 悪性GUIを利用者が明示実行するケースは防げない（同一ユーザー権限問題）。
  - 警告抑止オプション利用時は検知強度が下がる。
- Next corrective actions:
  - 将来: 署名検証/固定公開鍵方式の追加（必要時）。

## Checkpoint: Chaos Engineering (2026-04-06)
- Transparency:
  - `docs/CHAOS_ENGINEERING.md` を追加し、目的・実験範囲・手順・合格条件を公開。
  - 参考元URLを明示し、プロジェクト向けに適用範囲を限定。
- Reliability:
  - `scripts/chaos-local.sh` を追加し、3つの耐障害テストを自動実行可能化。
  - 実行結果: 全フェーズ成功（invalid config recovery / disabled rule isolation / hot-reload pressure）。
- Security:
  - 実験はローカルのみ・一時領域ベースで実施し、外部影響を避ける方針を固定化。
- Low runtime cost:
  - カオス実験は通常運用パスから分離したオンデマンド実行。
  - 常駐処理に追加負荷なし。
- Remaining risks:
  - 実験範囲は主に設定・監視耐性で、通知先外部障害やGUI互換破壊の注入は未網羅。
- Next corrective actions:
  - 通知送信失敗（DNS/TLS/429）を含む追加シナリオを `chaos-local.sh` に段階追加。
