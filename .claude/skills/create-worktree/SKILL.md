---
name: create-worktree
description: >
  Create a new git worktree for a feature or fix branch and set up
  the environment to work in it. Use this when the user asks to work
  on a new feature/fix in parallel, wants an isolated workspace,
  or asks to "start working on X in a separate worktree".
---

# Worktree作成手順

1. 現在のリポジトリ名を確認する: `basename $(git rev-parse --show-toplevel)`
2. 適切なブランチ名を決める(例: feat/xxx, fix/xxx)
3. worktreeを作成する:
```bash
   git worktree add ../{repo-name}-{branch-name} -b {branch-name}
```
4. 依存関係のセットアップが必要なら実行する(package.jsonがあれば`npm install`など)
5. 作成したworktreeのパスをユーザーに報告する

## 注意点
- 同じブランチ名で既にworktreeが存在する場合はエラーになるので、事前に `git worktree list` で確認する
- 作業が終わったら `git worktree remove` で片付けることを促す