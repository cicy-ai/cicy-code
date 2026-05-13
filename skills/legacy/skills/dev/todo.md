# todo

Manage todos via GitHub Issues + GitHub Project Board.

## Setup

- Repo: `cicy-dev/Private` (private)
- Project: **Todo Board** #4 → https://github.com/users/cicy-dev/projects/4
- Labels: `feat`, `fix`, `todo`

## Commands

```bash
todo                        # list all open todos
todo feat "make a js demo"  # add feature todo
todo fix "fix the js error" # add fix todo
todo add "review code"      # add general todo
todo done <number>          # close/complete a todo
todo url                    # show project board URL
todo -h                     # show help
```

## How it works

- `todo feat/fix/add` creates a GitHub Issue and auto-adds it to the Project Board
- `todo done` closes the issue
- All todos visible at: https://github.com/users/cicy-dev/projects/4

## Script

Located at: `~/skills/todo` → `/usr/local/bin/todo`

## Create Labels (first time setup)

```bash
gh label create feat --repo cicy-dev/Private --color 0075ca --description "New feature"
gh label create fix  --repo cicy-dev/Private --color d73a4a --description "Bug fix"
gh label create todo --repo cicy-dev/Private --color e4e669 --description "General task"
```
