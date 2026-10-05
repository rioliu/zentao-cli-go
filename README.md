# zentao-cli-go

A Go implementation of a Zentao CLI with first-class comment support and a
contract test suite that detects server API drift across Zentao versions.

## Why this exists

The official `zentao-cli` (Node) generates its module schemas from an upstream
OpenAPI spec (`easysoft/zentao-api` -> `data/zentao-openapi.json`) that does
**not** match real server behavior. The resulting schema bugs (fields silently
dropped, `productID` rejected) had to be patched into a 1MB minified bundle
after every CLI update. Community fixes have been open and unreviewed for
months (easysoft/zentao-api#3, #6), so this project owns its own schema layer.

## Compatibility

Developed and regression-tested against **Zentao open source 22.4**
(`easysoft/zentao:22.4-20260729`). Behavior against other versions or
editions (biz / max / ipd) is not guaranteed - the contract suite exists
precisely because server behavior drifts between versions. Before pointing
this CLI at another Zentao version, run `make regression` against that
version's image (`make testenv-up IMAGE=...`) and expect the contract ring to
report exactly what changed.

## Core principle

**The OpenAPI spec is a hypothesis; the contract tests are the truth.**

```
specs/upstream.json   frozen upstream OpenAPI spec (hypothesis)
specs/overrides.yaml  verified per-version server deviations (client rules)
        |
        v  (generator, planned)
internal/gen          generated client + module registry
        |
        v  verified by
contract/             live contract tests against pinned server images
```

When a server upgrade changes behavior, `make test-contract` goes red and the
failing test names the exact drift. Update `specs/overrides.yaml`, regenerate,
ship.

## Layout

```
cmd/zentao/           entrypoint
internal/cmd/         subcommands (comment add/list, profile, login/logout, version)
internal/zclient/     REST v2 client + classic web routes (comments), session cache
internal/profile/     connection profiles (account@server keys, aliases)
contract/             contract tests (drift detectors)
contract/testenv/     disposable Zentao 22.4 test environment
specs/                upstream spec + verified overrides
```

## Step-by-step usage

### 1. Install

```bash
brew install rioliu/tap/zentao-cli-go   # macOS/Linux, prebuilt binary
zentao version                          # -> 0.1.4
```

Or from source: `make build` gives you `bin/zentao`.

### 2. Login

One command authenticates, saves the target as a profile (`account@server`)
and warms the session cache - afterwards, commands need no credentials:

```bash
# password via stdin (recommended for scripts and agents)
zentao login -s http://zentao.corp/zentao -u admin --password-stdin

# or typed on the command line (visible in shell history)
zentao login -s http://zentao.corp/zentao -u admin -p 'password'
```

`login` always verifies credentials **fresh** - a cached session never masks a
wrong password. Useful options: `--as prod` saves a short alias,
`--save-password` stores the password (0600) so expired sessions can renew
without prompting.

### 3. Create: story > task > bug

The development loop in Zentao terms: a **story** captures the requirement,
**tasks** are the development work under it, **bugs** are what testing finds.

```bash
# Story: the requirement
zentao story create --product 1 --title 'Fix login timeout' \
  --spec '<p>Users are logged out after 5 minutes...</p>' \
  --verify '<p>Session survives 30 minutes of activity</p>' --reviewer admin

# Task: the work, linked to the story (tasks live under an execution/sprint)
zentao task create --execution 2 --name 'Rework session handling' \
  --story 14 --assigned-to dev1 --estimate 8

# Bug: what testing found
zentao bug create --product 1 --title 'Session dies on page refresh' \
  --severity 3 --type codeerror --steps '<p>1. login 2. refresh 3. logged out</p>'
```

Content is **HTML** (Zentao open source renders HTML only); use the
`--spec-file`/`--steps-file`/`--content-file` forms for large payloads (`-`
reads stdin). `story get` / `task get` / `bug get` print the full object JSON.

### 4. Comment as you work

```bash
zentao comment add story 14 --content '<p>MR: !11 merged</p>'
zentao comment add task 5 --content '<p>blocked on API keys</p>'
zentao comment add bug 12 --content-file note.html
zentao comment list story 14     # JSON: [{"id": N, "comment": "<html>"}]
```

Works for any object type: `story`, `task`, `bug`, `epic`, `requirement`,
`testcase`, `execution`, `project`, ...

### 5. Update status

```bash
# Story: reviewing -> active -> closed
zentao story activate 14 --comment '<p>starting work</p>'
zentao story close 14 --reason done --comment '<p>shipped</p>'

# Task: wait -> doing -> done -> closed
zentao task start 5 --consumed 1 --left 4
zentao task finish 5 --consumed 2
zentao task close 5 --comment '<p>merged</p>'

# Bug: active -> resolved -> closed (confirm/activate reopen)
zentao bug resolve 12 --resolution fixed --comment '<p>fixed in !12</p>'
zentao bug close 12 --comment '<p>verified</p>'
```

Fields can be updated at any point (`zentao story update 14 --spec '...'`,
`zentao task update 5 --assigned-to dev2`, `zentao bug update 12 --severity 4`).
Notes passed with `--comment` land in the object's action stream alongside the
status change.

### 6. Work with multiple Zentao instances

Profiles mirror the official zentao-cli semantics: the canonical key is
**`account@server`**, the *current* profile is the one switched to most
recently, and an optional short alias works anywhere a key is expected.

```bash
zentao profile add --server http://zentao.corp --account admin --as prod --save-password
zentao profile add --server http://localhost:8088 --account admin --as lab --save-password

zentao profile                 # list, current marked with *
zentao profile lab             # switch (or: zentao profile use lab)

zentao comment add story 14 --content '<p>hi</p>'     # runs against lab
zentao --profile prod comment list story 14           # one-off against prod
zentao profile remove lab                             # delete a profile
```

Target selection (highest wins): `--profile` flag > `ZENTAO_PROFILE` env >
`ZENTAO_URL`/`ZENTAO_ACCOUNT` env > current profile. Passwords:
`ZENTAO_PASSWORD` env wins over a profile's saved password. Profiles share
the session cache: entries are keyed `server|account`, so two profiles on the
same account reuse the same sessions, and adding the same `account@server`
twice updates one entry (alias and saved password are preserved).

### 7. Install the skill for a coding agent (optional)

```bash
zentao add-skill pi        # Pi         (~/.pi/agent/skills)
zentao add-skill claude    # Claude Code (~/.claude/skills)
zentao add-skill agents    # portable    (~/.agents/skills)
zentao add-skill --dir X   # any custom skills directory
```

The skill is a portable `SKILL.md` (agentskills.io spec). Restart the agent
after installing.

### 8. Manage sessions

```bash
zentao login               # verify the current target, re-auth on demand
zentao logout              # drop cached sessions (the server session then
                           # expires on its own - it is not revocable via API)
```

## Command reference

| Command | Purpose |
|---|---|
| `zentao login [-s URL -u ACCOUNT [-p PASS \| --password-stdin]] [--as A] [--save-password]` | authenticate, save profile, warm sessions |
| `zentao logout` | drop cached sessions |
| `zentao comment add <module> <id> --content HTML \| --content-file F` | add a comment (F = `-` reads stdin) |
| `zentao comment list <module> <id>` | list comments as JSON |
| `zentao story create \| update \| get \| activate \| change \| close` | story lifecycle |
| `zentao task create \| update \| get \| start \| finish \| close \| activate` | task lifecycle |
| `zentao bug create \| update \| get \| resolve \| confirm \| close \| activate` | bug lifecycle |
| `zentao profile [list \| add \| use \| remove]` | manage/switch connection profiles |
| `zentao --profile <key\|alias> <command>` | run one command against a specific profile |
| `zentao add-skill [pi \| claude \| agents \| --dir X]` | install the bundled agent skill |
| `zentao version` | print version |

Exit codes: **0** success, **1** runtime/auth failure, **2** usage error.

## Session lifecycle

Both credentials (REST `Token`, web `zentaosid`) are server-side PHP sessions.
Their expiry is unknowable to the client, so the CLI follows **reuse until
proven dead, renew lazily**:

1. Sessions are cached in `~/.config/zentao-cli-go/sessions.json` (0600) and
   reused across invocations - a warm start performs zero logins
2. A call that fails with the expiry signature (REST: `302` empty body; web:
   login-timeout response) triggers a **transparent re-login and one retry**
   with the fresh session, which is then cached
3. Renewal needs a password source (`ZENTAO_PASSWORD`); without one the error
   says so explicitly instead of failing mysteriously

Controls: `ZENTAO_NO_CACHE=1` disables caching; `ZENTAO_SESSION_CACHE=<path>`
overrides the file location. v0 currently authenticates from
`ZENTAO_URL`/`ZENTAO_ACCOUNT`/`ZENTAO_PASSWORD` env on every run when no cache
is warm - the cache simply removes those logins.

## Test environment

```bash
ADMIN_PASSWORD=... make testenv-up    # Zentao 22.4 in podman, ~20s, wizard automated
export ZENTAO_TEST_URL=http://localhost:8088 ZENTAO_TEST_ACCOUNT=admin ZENTAO_TEST_PASSWORD=...
make regression                       # the full gate
make testenv-down
```

Image pin: `hub.zentao.net/app/zentao:22.4-20260729` (official mirror of
`easysoft/zentao`; Docker Hub is unreachable from some networks). For the
version matrix, run `make testenv-up` with `IMAGE=...` for each pinned
version and compare failures.

## Regression suite

`make regression` is the gate for every change. Three rings, each catching a
different class of breakage:

| Ring | Command | Scope | Catches |
|------|---------|-------|---------|
| 1. Unit | `make test-unit` | `internal/...`, no server, <1s | protocol helpers (param order, URL building, response classification, credential redaction, realm separation) |
| 2. Contract | `make test-contract` | against testenv | server behavior drift: what the spec claims vs what the server does, known bugs/quirks, error-path behavior |
| 3. E2E | `make test-e2e` | compiled binary as subprocess | user-visible contract: command syntax, output formats, exit codes (0 ok / 1 runtime / 2 usage) |

Rules of the suite:

- Contract tests assert **observed** server behavior, including bugs. When a
  server upgrade flips one, the failing test name *is* the drift report.
- `TestSpecClaim_*` pins what the upstream spec says,
  `TestServerReality_*` pins what the server does - never let the two
  silently converge without updating `specs/overrides.yaml`.
- Error paths are pinned too (bad credentials, missing objects, PHP fatals,
  silent server quirks) - silent success is the failure mode this project
  exists to eliminate.

## Verified server quirks (22.4)

See `specs/overrides.yaml` for the full list. Highlights:

- `productID` placement on create differs **per module** (stories/bugs need
  it in the query string, epics/requirements/productplans accept the body),
  contradicting the upstream spec in both directions
- `productID` in both places: the body value silently shadows the query value
- `GET /epics?productID=N` returns an empty body (server bug)
- epic create returns no `id`; story create does
- comments have no REST endpoint at all - they live in the classic action
  module behind web-session auth and a mandatory `Referer` header

## Roadmap

- [x] v0: `comment` subcommand + contract suite + testenv automation
- [ ] M1: generator from spec + overrides -> typed client (238 operations)
- [ ] M2: `ls/get/create/update/delete/do` parity with official CLI syntax
- [ ] M3: `--spec-file`/stdin everywhere (no shell-escaping limits)
- [ ] M4: MCP server (modelcontextprotocol/go-sdk), drop-in for the AI skill
- [ ] M5: version matrix in CI (22.4 / 22.5 / 22.6, then biz/max/ipd)

## License

MIT - see [LICENSE](LICENSE).
