---
name: zentao-cli-go
description: Operate Zentao (禅道) project management via the zentao Go CLI - add and list comments on any object (story, task, bug, epic, testcase...), manage connection profiles for multiple Zentao instances, and verify login. Use when the user mentions zentao, 禅道, comments, 评论, 留言, stories, 需求, bugs, epics, or working with Zentao objects.
license: MIT
compatibility: Requires the `zentao` binary (brew install rioliu/tap/zentao-cli-go) and ZENTAO_URL/ZENTAO_ACCOUNT/ZENTAO_PASSWORD or a saved profile. Tested with Zentao open source 22.4.
metadata:
  author: Rio Liu
  repository: https://github.com/rioliu/zentao-cli-go
  keywords: [zentao, 禅道, comment, 评论, story, bug, epic, profile]
---

# zentao CLI (Go)

Operate Zentao through the `zentao` command line tool. The CLI handles
authentication, session caching with transparent renewal, and multiple
Zentao instances via profiles.

## When to use

- Creating and driving development work: stories (requirements), tasks
  (development work), bugs (issues found in testing)
- Adding comments/评论/备注/留言 to Zentao objects (any type: story, task, bug, epic, ...)
- Listing comments on an object
- Switching between Zentao instances (profiles)
- Checking login/credentials work

## Authentication

Three ways, all handled by the CLI internally:

1. Environment: `ZENTAO_URL`, `ZENTAO_ACCOUNT`, `ZENTAO_PASSWORD`
2. Profiles: `zentao profile add --server URL --account NAME --as alias --save-password`
   then commands run against the current profile (or `--profile <alias>`)
3. Token: `ZENTAO_TOKEN` env (or `zentao login --token TOKEN`) - a REST API
   token instead of a password; `zentao token` prints an authorized one.
   Tokens cannot renew themselves (no password -> expired token is a hard
   error) and cannot reach the web realm (comment commands need a password).

Never collect credentials interactively in the conversation. If the user is not
set up yet, ask them to run `zentao profile add ...` or set the env vars
themselves in their terminal.

## Security rules (strict)

- NEVER read, print, or modify the session cache or profile files
  (`~/.config/zentao-cli-go/sessions.json`, `~/.config/zentao-cli-go/profiles.json`).
  They hold live session credentials and possibly saved passwords.
- Never pass passwords on the command line; use env vars or `--save-password`.
  Same for tokens: prefer `ZENTAO_TOKEN` env over `--token` in shared shells.
- All Zentao data must be obtained through the `zentao` CLI, not by
  manipulating its credential storage.

## Commands

```bash
# Login: authenticates, saves the target as a profile (account@server),
# warms the session cache. Subsequent commands need no credentials.
zentao login -s http://zentao.corp/zentao -u admin --password-stdin   # safe for scripts/agents
zentao login -s http://zentao.corp/zentao -u admin -p PASS            # human use (visible in history)
zentao login -s http://zentao.corp/zentao -u admin --token "$TOKEN"   # token auth, no password
zentao login                          # verify the currently resolved target
zentao token                          # print an authorized REST API token (CI)

# The development loop: story > task > bug
# Product IDs first (story/bug create need --product)
zentao product list                 # one line per product: #id  name  code  status
zentao product get 2                # full product object as JSON
zentao story create --product 1 --title 'Fix login timeout' \
  --spec '<p>description</p>' --verify '<p>acceptance</p>' --reviewer admin
zentao task create --execution 2 --name 'Rework sessions' --story 14 --assigned-to dev1
zentao bug create --product 1 --title 'Session dies on refresh' --steps '<p>1. ...</p>'

# Status flows
zentao story activate 14 --comment '<p>starting</p>'
zentao story close 14 --reason done --comment '<p>shipped</p>'
zentao story update 14 --status draft    # raw status write: draft|reviewing|active|changing|closed
zentao task start 5 --consumed 1 --left 4; zentao task finish 5 --consumed 2; zentao task close 5
zentao bug resolve 12 --resolution fixed --comment '<p>fixed</p>'; zentao bug close 12

# List work in a scope (default: my open items)
zentao bug list                       # my bugs
zentao task list --execution 2        # sprint scope
zentao story list --product 1 --json  # raw JSON

# Read any object as JSON
zentao story get 14; zentao task get 5; zentao bug get 12; zentao product get 2

# Comments (HTML only - Zentao open source renders HTML only)
zentao comment add story 14 --content '<p>Done, MR: !11 merged</p>'
zentao comment add bug 12 --content-file note.html
echo '<p>from stdin</p>' | zentao comment add task 5 --content-file -

# List comments (JSON)
zentao comment list story 14

# Profiles (multiple Zentao instances)
zentao profile                       # list, current marked with *
zentao profile add --server URL --account NAME --as alias --save-password
zentao profile <alias>               # switch
zentao --profile <alias> comment list story 14   # one-off target

# Session management
zentao login                         # verify credentials, warm session cache
zentao token                         # print an authorized REST API token
zentao logout                        # drop cached sessions
```

Modules for comments: story, task, bug, epic, requirement, testcase,
testtask, execution, project, product, productplan, build, release,
feedback, ticket, user, program, doc, file.

## Behavior contract

- Exit codes: 0 success, 1 runtime/auth failure, 2 usage error
- `comment list` prints a JSON array of `{"id": N, "action": "...", "comment": "<html>"}`
  (includes finish/close remarks, not only `action=commented` entries)
- Comment content must be HTML (`<p>...</p>`); markdown is not rendered
- Bug comments work through the same route as everything else - the old
  official-CLI workaround (abusing `bugs/{id}/confirm`) is obsolete and must
  not be used
- For large content use `--content-file` or stdin, never giant argv strings
- Unknown modules are rejected client-side (the server would silently orphan
  the comment)
- `story update --status` is a raw status write validated client-side; for
  active/closed prefer `story activate` / `story close`, which record history
  and handle close reason/stage

## Known caveats (server-side)

- Verified against Zentao open source 22.4; other versions may drift
- Session expiry is invisible to the CLI; it renews automatically and retries,
  so users should never see expiry errors while ZENTAO_PASSWORD is available
