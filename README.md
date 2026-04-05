# VRC LogHook

`VRC LogHook` は VRChat 向けのローカルログ監視ツールです。  
VRChat のログファイルへの新規追記を監視し、VRChat API やアカウントログインを使わずに通知します。

## ポリシー
優先順位:
1. Transparency
2. Reliability
3. Security
4. Low runtime cost

## 実行ファイル
- `vrc-loghook`（コアデーモン / CLI）
- `vrc-loghook-gui`（置換可能クライアント）

## できること
- 設定可能なディレクトリ内の VRChat ログ（`output_log_*.txt`）を監視
- 起動時条件の処理後は追加行のみを追跡
- ログローテーションを自動追従
- `contains` / `regex` ルールでマッチ判定
- 通知先:
  - ローカル JSONL イベントログ（常時）
  - Discord Webhook（任意）
- 明示同意が必要なコマンド Hook（任意）
- 設定のホットリロード
- オフセット永続化による再起動時の信頼性確保

## やらないこと
- VRChat 公式 API は使用しない
- VRChat アカウントのログイン/パスワード処理は実装しない

## クイックスタート
```bash
# 1) ビルド
GOFLAGS= go build -o vrc-loghook ./core/cmd/vrc-loghook
GOFLAGS= go build -o vrc-loghook-gui ./gui/cmd/vrc-loghook-gui

# 2) 初回起動（設定ファイルがなければ自動生成）
./vrc-loghook

# 3) 状態確認（IPC経由）
./vrc-loghook --status

# 4) GUIクライアント起動（状態監視）
./vrc-loghook-gui
```

## 設定ファイル
デフォルト設定パス:
- Windows: `%LOCALAPPDATA%/VRCLogHook/config.hjson`
- macOS: `~/Library/Application Support/VRCLogHook/config.hjson`
- Linux: `~/.config/vrc-loghook/config.hjson`

補足:
- JSON / HJSON風コメント付き形式に対応
- 設定ファイルが存在しない場合は自動生成
- 機密値（Webhook URL など）は状態/設定出力でマスク
- `observability.log_level` でログレベルを設定可能（`debug` / `info` / `warn` / `error`）
- `observability.stdout=true` で自己ログを標準出力にもミラー出力可能

ログレベルの目安:
- `debug`: 監視ポーリング詳細、重複抑制、状態保存などの詳細ログ
- `info`: 起動/停止、設定再読込、マッチ検出、定期ステータス
- `warn`: Hookスキップ、再起動が必要な設定変更など
- `error`: 通知失敗、設定読込失敗、監視処理エラー

## セーフティ注意
- Hook 実行はデフォルト無効
- Hook を有効にするには `hooks.enabled=true` と `hooks.unsafe_consent=true` の両方が必要
- Hook は任意コマンドを実行できるため、危険性を理解している上級者向け機能

## 既知の制限（現MVP）
- GUI は現状、軽量な CLI 風ステータスクライアント（置換可能アーキテクチャ）
- Windows IPC は最小限の Named Pipe request/response 実装。高度機能は今後拡張予定

## 開発
```bash
gofmt -w $(rg --files -g '*.go')
GOCACHE=/tmp/vrc-go-cache go test ./...
```

### ローカルWebhook統合テスト（GitHub CI外）
`.local/webhook.env`（`.gitignore` 済み）に `DISCORD_WEBHOOK_URL` を設定して、以下を実行します。

```bash
./scripts/test-webhook-integration.sh
```

このテストは `-tags=integration` でのみ実行され、GitHub Actions では実行しない前提です。

## ライセンス
CC0 (`LICENSE`)
