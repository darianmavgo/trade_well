---
name: recursive-git-status
description: Show what changed across every git repo under trade_well (including nested repos like data/.git), plus gitignored files and files outside any repo that were modified recently. Use when asked what files changed during a piece of work, for a recursive or whole-workspace git status, or before committing across repos.
---

# Recursive git status

Plain `git status` in `backtestgosqlite` misses most of what a work session touches:

- `data/` is its own git repo, so its changes never show in the parent.
- `trade_well/` itself is a repo that contains the other three.
- Databases, `bin/` and `refdata/*.db` are gitignored, so status hides them.
- Scratch work (the scratchpad directory) and memory files live outside every repo.

`rgs.sh` (next to this file) is read-only. It finds every `.git` under a root and prints, per repo, the branch, ahead/behind, the HEAD commit and every changed or untracked file. Files inside a nested repo are attributed to that repo only.

## Run it

From `backtestgosqlite`:

```bash
.claude/skills/recursive-git-status/rgs.sh                     # status of every repo
.claude/skills/recursive-git-status/rgs.sh -s 3h               # also: ignored files modified in the last 3 hours
.claude/skills/recursive-git-status/rgs.sh -s 2026-10-05 \
    -x <scratchpad dir> -x <memory dir>                        # also: files outside any repo
```

Flags: `-r ROOT` (default: the parent of this repo), `-s SINCE` (an `fd` duration like `30min`, `2h`, `1d`, or a date), `-x DIR` (extra non-repo directory, repeatable, needs `-s`), `-d DEPTH` (how deep to look for `.git`, default 4).

## Reading the result

1. A clean repo with no `-s` does not mean nothing happened. Rerun with `-s` set to when the work began.
2. `-- ignored but modified` lists gitignored files (databases, binaries, `-wal`/`-shm` files). Opening a SQLite database read-only can still touch its `-shm` file, so check `ls -l` mtimes on the `.db` itself before saying a database was changed.
3. Pass the session's scratchpad and the memory directory with `-x`. Analysis done in scratch databases only appears there.
4. Pre-existing changes in `data/` (reports from earlier pipeline runs) are common. Narrow `-s` to the session so they do not drown out the work being asked about.

## Rules

- Never commit, stage, delete or push from this skill. Report only. Use `../git_sync.sh` when the user asks to commit.
- Do not describe a file as changed by Claude unless its mtime falls inside the session.
