---
name: backend-runbook-phase
description: >
  Implement the next (or a specified) Phase of
  plan/backend-local-docker-runbook.md in its own isolated worktree and
  PR. Use this when the user says things like "次のphaseやって",
  "Phase N を実装して", "runbookの続きを別PRで進めて", or otherwise
  asks to continue the Backend Local Docker Runbook work.
---

# Backend Local Docker Runbook: Phase 実装

`plan/backend-local-docker-runbook.md` を1 Phaseずつ、独立したworktree +
PRで進めるための手順。Phase 0〜2 で確立した流れをテンプレート化したもの。

## 手順

1. **対象Phaseを決める**
   - `plan/backend-local-docker-runbook.md` を読み、まだ実装されていない
     最初の Phase(またはユーザーが指定したPhase)を対象にする。
   - そのPhaseセクションの「作成するファイル」「完了条件」を正として読む。
     実装の詳細設計は `architecture.md` と
     `plan/backend-implementation-steps.md` を優先する。
   - 1PRにつき1Phase(または1つの独立した修正)に絞る。複数Phaseをまとめない。

2. **最新状態とbase branchを確認する**
   ```bash
   git fetch origin
   gh repo view --json defaultBranchRef -q .defaultBranchRef.name
   gh pr list --state open
   ```
   - base branch名を **`main` 決め打ちにしない**。上記コマンドでリポジトリの
     デフォルトブランチ(現時点では `develop`)を毎回確認し、それを
     起点にする。デフォルトブランチは途中で変わりうる(実際に `main` から
     `develop` に変更された実績がある)ため、ハードコードした値を
     過去のPRやドキュメントから流用しない。
   - 直前のPhaseのPRが**マージ済み**なら `origin/<デフォルトブランチ>` を
     起点にする。
   - **未マージ**で、かつ今回の変更がそのPRの内容に依存する(同じファイルを
     拡張する等)場合は、そのPRのブランチを起点にして積む。PR本文に
     「#N マージ後にbaseを`<デフォルトブランチ>`へ向け直してください」と
     明記する。

3. **worktreeを作成してそこに入る**
   ```bash
   git worktree add ../{repo-name}-{branch-name} -b {branch-name} <base>
   ```
   - ブランチ名は `feat/backend-<phase-slug>`(新機能)、
     `fix/<slug>`(バグ修正)、`chore/<slug>`(ツール整備)など内容に応じて選ぶ。
   - `EnterWorktree` ツール(`path` 指定)でセッションの作業ディレクトリを
     そのworktreeに切り替える。作業が終わったら `keep` で抜ける
     (PRがまだ残るため `remove` しない)。

4. **そのPhaseの範囲だけ実装する**
   - スコープ外の実装(次のPhase分やリファクタ)はしない。
   - 途中で明らかなバグや設計上の懸念に気づいても、今のPhaseと無関係なら
     直さずユーザーに報告するだけに留める(例: Phase 1由来の
     `select {}` デッドロック bug は Phase 2/Dockerfile修正では直さなかった)。

5. **完了条件を実際に実行して検証する**
   - Runbookの各Phaseに書かれているコマンドをそのまま実行する
     (例: `go test ./...`, `make build`, `docker build ...`,
     `docker compose up --build`, `curl http://localhost:PORT/healthz`)。
   - Dockerを使うPhaseはColima経由のDocker daemonが使えるので、実際に
     `docker build` / `docker compose up` まで通してから確認する。
   - 検証用に作った一時ファイル・コンテナは必ず後片付けする:
     ```bash
     docker compose down -v
     rm -rf tmp/ backend/.env.local backend/bin/
     docker image rm <built-images> 2>&1 | tail -10
     ```

6. **gitignoreを適宜更新する**
   - 新しいビルド成果物やローカル専用ファイル(`bin/`, `tmp/`,
     `.env.local` 系)が増えたら `.gitignore` に追加する。
   - `.env.example` 系のテンプレートファイルは既存の
     `!.env.example` ルールで自動的に追跡対象になることを
     `git check-ignore -v <path>` で確認する。

7. **commit / push / PR作成**
   - `git status` で意図した差分だけがstageされているか確認してから
     `git add <files>`(`-A` や `.` は使わない)。
   - commitメッセージは「何のPhase/何の修正か」と「何を実際に検証したか」
     を書く。
   - PR本文のテンプレート:
     ```markdown
     ## Summary
     - どのPhase/セクションを実装したか
     - 主要な設計判断(スコープ外にしたもの含む)

     ## Test plan
     - [x] 実際に実行して確認したことだけをチェック済みにする

     (base branchが未マージPRの場合のみ)
     Base branch: この修正は `<branch>` (#N、未マージ) の上に積んでいます。
     #N マージ後にこのPRのbaseを`<デフォルトブランチ>`に向け直してください。
     ```
   - `gh pr create --base <base-branch-if-stacked> --title ... --body ...`

8. **ユーザーに報告する**
   - worktreeパス、ブランチ名、PR URLを伝える。
   - スコープ外で見つけた問題があれば箇条書きで触れ、対応するか確認する。

## 注意点

- 同じブランチ名で既にworktreeが存在する場合はエラーになるので、事前に
  `git worktree list` で確認する。
- 前のPhaseのPRがマージされたかどうかは毎回 `gh pr view <N> --json
  state,mergedAt` で確認してから次のworktreeを作る(古い情報で
  base branchを決めない)。
- デフォルトブランチ名も同様に毎回 `gh repo view --json defaultBranchRef`
  で確認する。過去の会話やPRで見た `main` / `develop` などの名前を
  そのまま使い回さない。
- コマンド実行の許可を都度求めず進めてよい、と明示された場合以外は
  破壊的操作(`docker system prune`、他worktreeのファイル削除など)は
  避ける。
