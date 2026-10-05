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

## Usage

```bash
make build

export ZENTAO_URL=http://zentao.example.com/zentao
export ZENTAO_ACCOUNT=admin
export ZENTAO_PASSWORD=...

bin/zentao comment add story 14 --content '<p>MR: !11 merged</p>'
echo '<p>from stdin</p>' | bin/zentao comment add bug 12 --content-file -
bin/zentao comment list story 14
```

Comment content is HTML (Zentao open source renders HTML only). Any object
type works (`story`, `task`, `bug`, `epic`, `testcase`, ...).

## Profiles (multiple Zentao instances)

Profiles mirror the official zentao-cli semantics: the canonical key is
**`account@server`**, the *current* profile is the one switched to most
recently, and an optional short alias works anywhere a key is expected.

```bash
zentao profile add --server http://zentao.corp --account admin --as prod --save-password
zentao profile add --server http://localhost:8088 --account admin --as lab --save-password

zentao profile                 # list, current marked with *
zentao profile lab             # switch (or: zentao profile use lab)
zentao comment add story 14 --content '<p>hi</p>'            # against lab
zentao --profile prod comment list story 14                  # one-off against prod
zentao profile remove lab2
zentao login / zentao logout   # verify creds / drop cached sessions
```

Target selection (highest wins): `--profile` flag > `ZENTAO_PROFILE` env >
`ZENTAO_URL`/`ZENTAO_ACCOUNT` env > current profile. Passwords: `ZENTAO_PASSWORD`
env wins; `--save-password` stores it in the profile file (0600, opt-in) for
password-less session renewal.

Profiles share the session cache with everyone else: entries are keyed
`server|account`, so two profiles pointing at the same account reuse the same
sessions, and adding the same `account@server` twice updates one entry.

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
