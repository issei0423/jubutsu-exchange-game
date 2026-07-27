# CLAUDE.md

## プロジェクト概要

呪物交換対戦ゲーム(仮) — 1対1で呪物を取引し合うボードゲーム風対戦Webゲーム。
仕様・ルールの正は `要件定義書.md` にある。作業前に必ず読むこと。

## 作業ルール(重要)

- **作業(タスク)ごとに必ず新しいブランチを切り、`git worktree` で作業ツリーを分けること。** main ブランチ上で直接作業しない。
- ブランチ名は `feature/<内容>` / `fix/<内容>` / `docs/<内容>` のように内容がわかる名前にする。
- 手順の例:

```bash
# 作業開始: ブランチ + ワークツリーを作る
git worktree add ../jubutsu-feature-board -b feature/board
cd ../jubutsu-feature-board
# ...作業してコミット...

# 作業完了: main にマージしたらワークツリーを片付ける
git worktree remove ../jubutsu-feature-board
```
