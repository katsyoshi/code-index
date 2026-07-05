# YARITAIKOTO

`code-index` を今後どう育てるかのメモ。

## まずやりたい

- lock まわりをもう少し詰める
  - stale lock の扱いを決める。
  - `status` に schema version, indexed_at, branch/head/dirty などを増やすか検討する。

- `update` の運用を詰める
  - rename・ignore 設定変更・schema 変更の扱いを決める。
  - `rebuild` は full rebuild、`update` は incremental refresh という分担を維持する。
  - update/partial build を入れるタイミングで、DB 内の管理テーブルも検討する。
    - `config`: `max_bytes`, `ignore_dirs`, enabled components など。
    - `components`: `files`, `lines`, `symbols`, `metrics`, `fts` の状態。
    - `build_runs`: 直近 operation の履歴や失敗理由。
  - 実行中状態は atomic replace と相性がいい外側の `.lock` file に持たせ、DB 内テーブルは完了後の状態・履歴を持たせる。

- `init` の次の用途を詰める
  - 現在は schema/metadata のみ作成し、既存 DB があれば失敗する。
  - 将来、index だけ作る、metrics だけ作る、などの部分構築とどう接続するか決める。

- README の運用例をもう少し詰める
  - `CODE_INDEX_CACHE_DIR` と `--db` の使い分けを明確にする。
  - `init` / `rebuild` / 将来の `update` の推奨フローを書く。

- index の鮮度を見えるようにする
  - `meta` に `indexed_at`, `branch`, `head`, `dirty` などを持つか検討する。
  - `status` コマンドで現在の worktree と DB のズレを表示する。

## その次にやりたい

- schema まわりを扱いやすくする
  - `schema`: DB schema を簡潔に表示する。
  - schema version を `meta` に入れる。
  - schema migration するのか、古い DB は rebuild を促すのか決める。

- symbol 抽出を強くする
  - regex ベースは維持しつつ、optional で tree-sitter を検討する。
  - 最初に対応するなら Go, Ruby, TypeScript, Python あたり。

- LLM/agent 向けのコマンドを増やす
  - `outline PATH`: 1ファイル内の symbol 一覧を表示する。
  - `refs NAME`: 定義ではなく参照候補を探す。
  - `explain QUERY`: どういう SQL を投げればよいかのヒントを出す。

- 出力形式を増やす
  - 今の tabs に加えて `--format json` を追加する。
  - LLM が parse しやすい安定した JSON schema にする。

## そのうちやりたい

- install しやすくする
  - GitHub Releases で linux/macOS 用 binary を出す。
  - `go install github.com/katsyoshi/code-index@latest` の案内を整える。

- Codex skill を薄くする
  - skill に本体を同梱し続けるのではなく、外部 `code-index` を使う前提に寄せる。
  - 見つからない場合だけ install/build 手順を案内する。

- CI を整える
  - `go vet ./...`
  - README のコマンド例が古くならないようにする。

## やった

- `code-index` に名前を統一した。
- `update` コマンドを追加した。
  - 既存 DB の変更ファイルを差し替える。
  - 削除されたファイル、ignore 対象になったファイルを DB から削除する。
  - `init` 済みの空DBにも投入できる。
- `file_metrics` テーブルと `metrics` コマンドを追加した。
- GitHub Actions CI を追加した。
  - `go test ./...`
  - build 確認
  - `init` / `rebuild` / `stats` / `metrics` の smoke test
- default DB path を XDG cache 対応にした。
  - `CODE_INDEX_CACHE_DIR`
  - `$XDG_CACHE_HOME/code-index`
  - `~/.cache/code-index`
- README に Git hook 例を追加した。
  - `post-checkout`
  - `post-merge`
- `rebuild` を atomic full rebuild にした。
  - 一時 DB に作成して、成功後に rename する。
  - hook が裏で rebuild している間も query は前の DB を読み続けられる。
- `init` / `rebuild` 中の query 体験を改善した。
  - DB と同じ場所に `.lock` file を作る。
  - 既存 DB がある場合、query は前の DB を読み続けて stderr に warning を出す。
  - 既存 DB がない場合、query は init/rebuild 中であることを明示して失敗する。
- lock 中の自動 rebuild 体験を改善した。
  - `rebuild` は既に lock がある場合、何もせず成功終了する。
  - lock 状態を確認する `status` コマンドを追加した。
- `build` コマンドを削除した。
- `init` コマンドを追加した。
  - schema/metadata のみ作成する。
  - 既存 DB がある場合は失敗する。
- README の usage を `init` / `rebuild` に更新した。

## 注意点

- language server や full code intelligence を目指しすぎない。
- grep の完全代替ではなく、LLM が最初に使う軽量な SQL navigation index として保つ。
- 便利さより先に、壊れにくい rebuild と安定した query 結果を優先する。
