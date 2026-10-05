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

- Adding comments/备注/留言 to Zentao objects (any type: story, task, bug, epic, ...)
- Listing comments on an object
- Switching between Zentao instances (profiles)
- Checking login/credentials work

## Authentication

Two ways, both handled by the CLI internally:

1. Environment: `ZENTAO_URL`, `ZENTAO_ACCOUNT`, `ZENTAO_PASSWORD`
2. Profiles: `zentao profile add --server URL --account NAME --as alias --save-password`
   then commands run against the current profile (or `--profile <alias>`)

Never collect credentials interactively in the conversation. If the user is not
set up yet, ask them to run `zentao profile add ...` or set the env vars
themselves in their terminal.

## Security rules (strict)

- NEVER read, print, or modify the session cache or profile files
  (`~/.config/zentao-cli-go/sessions.json`, `~/.config/zentao-cli-go/profiles.json`).
  They hold live session credentials and possibly saved passwords.
- Never pass passwords on the command line; use env vars or `--save-password`.
- All Zentao data must be obtained through the `zentao` CLI, not by
  manipulating its credential storage.

## Commands

```bash
# Add a comment (content is HTML - Zentao open source renders HTML only)
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
zentao logout                        # drop cached sessions
```

Modules for comments: story, task, bug, epic, requirement, testcase,
testtask, execution, project, product, productplan, build, release,
feedback, ticket, user, program, doc, file.

## Behavior contract

- Exit codes: 0 success, 1 runtime/auth failure, 2 usage error
- `comment list` prints a JSON array of `{"id": N, "comment": "<html>"}`
- Comment content must be HTML (`<p>...</p>`); markdown is not rendered
- For large content use `--content-file` or stdin, never giant argv strings
- Unknown modules are rejected client-side (the server would silently orphan
  the comment)

## Known caveats (server-side)

- Verified against Zentao open source 22.4; other versions may drift
- Session expiry is invisible to the CLI; it renews automatically and retries,
  so users should never see expiry errors while ZENTAO_PASSWORD is available
