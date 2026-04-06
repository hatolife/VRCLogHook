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
- 各ルールに `message_template` を設定して通知文面を制御可能（`{rule}` `{line}` `{file}` `{at}`）
- 既定ルールは Join/Leave 系通知を重視（`OnPlayerEnteredRoom` / `OnPlayerJoined` / `OnPlayerLeft ...`）
- エラー系（`Exception`）通知も既定ルールに含む
- Discord通知の最小間隔バッチ送信（`notify.discord.min_interval_sec`）に対応
- 通知先:
  - ローカル JSONL イベントログ（常時）
  - Discord Webhook（任意、`curl` コマンド経由）
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
# 引数なし起動時は、core起動と同時にGUI起動も試行します

# 3) 状態確認（IPC経由）
./vrc-loghook --status

# 4) GUIクライアント起動（ローカルWeb UI）
./vrc-loghook-gui

# 5) coreからGUIを起動
./vrc-loghook --open-gui
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
- IPC認証トークンは設定ファイル固定値ではなく、`vrc-loghook` 起動ごとに再生成され
  `<config-dir>/ipc.token` に保存されます（`vrc-loghook-gui` / CLI IPC操作はこれを優先利用）
- `notify.discord.min_interval_sec` を設定すると、短時間の複数イベントを1回のWebhookにまとめて送信できます（`0`で無効）
- `observability.log_level` でログレベルを設定可能（`debug` / `info` / `warn` / `error`）
- `observability.stdout=true` で自己ログを標準出力にもミラー出力可能
- Discord通知には `curl` が必要（未導入時は自己ログにインストール案内を出力）

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
- GUIはローカルWeb UI型のMVP（状態表示 / 設定編集 / 再読込 / 停止）
- 高度なネイティブデスクトップUI（画面遷移、通知履歴表示等）は未実装
- Windows IPC は最小限の Named Pipe request/response 実装。高度機能は今後拡張予定

## GUIオプション
```bash
./vrc-loghook-gui --config <path> --ipc <path> --listen 127.0.0.1:18419 --open-browser=true
```

core側GUI起動オプション:
```bash
./vrc-loghook --open-gui --gui-bin <vrc-loghook-guiのパス>
```

GUI起動セキュリティ（CLI側）:
- GUI起動前に `--identity` を実行し、`vrc-loghook-gui/*` 形式の識別子を確認
- ビルド時に埋め込まれたGUIハッシュと実ファイルが不一致の場合はデフォルトで警告
- ハッシュ不一致警告は `--gui-hash-warn=false` で抑止可能（非推奨）

`curl` がない場合のインストール例:
- Windows: `winget install cURL.cURL`
- macOS: `brew install curl`
- Linux: `apt install curl` / `dnf install curl` / `pacman -S curl`

## 開発
```bash
gofmt -w $(rg --files -g '*.go')
GOCACHE=/tmp/vrc-go-cache go test ./...
```

### ローカルでCI相当の成果物を作成
`release` ワークフローと同名の成果物（linux/darwin/windows amd64）と `SHA256SUMS.txt` を作成します。

```bash
./scripts/build-local-artifacts.sh
# または出力先指定
./scripts/build-local-artifacts.sh dist-local
```

### ローカルWebhook統合テスト（GitHub CI外）
`.local/webhook.env`（`.gitignore` 済み）に `DISCORD_WEBHOOK_URL` を設定して、以下を実行します。

```bash
./scripts/test-webhook-integration.sh
```

このテストは `-tags=integration` でのみ実行され、GitHub Actions では実行しない前提です。

## ライセンス
CC0 (`LICENSE`)
