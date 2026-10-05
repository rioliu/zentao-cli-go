#!/usr/bin/env bash
# Provision a minimum Zentao test environment (container + install wizard + API smoke).
# Validated against Zentao open source 22.4 (easysoft/zentao image).
#
# Usage:
#   ADMIN_PASSWORD=... bash zentao-testenv-up.sh
#
# Env overrides:
#   IMAGE           container image              (default hub.zentao.net/app/zentao:22.4-20260729)
#   CONTAINER       container name               (default zentao-testenv)
#   VOLUME          data volume name             (default zentao-testenv-data)
#   PORT            host port for web UI/API     (default 8088)
#   ADMIN_ACCOUNT   admin account created        (default admin)
#   ADMIN_PASSWORD  admin password (required)
#   MODE            install mode light|ALM       (default ALM)
#
# Notes:
# - Pulls from the hub.zentao.net mirror (Docker Hub is often unreachable on this network).
# - The install wizard requires a Referer header matching the host and the
#   X-Requested-With: XMLHttpRequest header, otherwise zentao silently drops
#   POST params (framework/base/router.class.php referer check) and the wizard
#   breaks in a redirect loop.

set -euo pipefail

IMAGE="${IMAGE:-hub.zentao.net/app/zentao:22.4-20260729}"
CONTAINER="${CONTAINER:-zentao-testenv}"
VOLUME="${VOLUME:-zentao-testenv-data}"
PORT="${PORT:-8088}"
ADMIN_ACCOUNT="${ADMIN_ACCOUNT:-admin}"
ADMIN_PASSWORD="${ADMIN_PASSWORD:?set ADMIN_PASSWORD}"
MODE="${MODE:-ALM}"
DB_USER="${DB_USER:-root}"
DB_PASSWORD="${DB_PASSWORD:-123456}"

BASE="http://localhost:${PORT}"
API="${BASE}/api.php/v2"
JAR="$(mktemp)"
trap 'rm -f "$JAR"' EXIT

step() { printf '[%s] %s\n' "$(date +%H:%M:%S)" "$*"; }
die()  { printf 'ERROR: %s\n' "$*" >&2; exit 1; }

# Match "key":"value" in JSON regardless of pretty-printing/whitespace.
json_has() { grep -Eq "\"$1\"[[:space:]]*:[[:space:]]*\"?$2\"?"; }

# zentao drops POST bodies without a matching Referer; every call needs these headers.
req() {
    curl -sS -b "$JAR" -c "$JAR" \
        -H "Referer: ${BASE}/" \
        -H 'X-Requested-With: XMLHttpRequest' \
        --max-time "${CURL_TIMEOUT:-120}" \
        "$@"
}

step "[1/7] Resetting container and volume"
podman rm -f "$CONTAINER" >/dev/null 2>&1 || true
podman volume rm "$VOLUME" >/dev/null 2>&1 || true

step "[2/7] Starting $IMAGE on port $PORT"
podman run -d --name "$CONTAINER" \
    -e MYSQL_INTERNAL=true \
    -p "${PORT}:80" \
    -v "${VOLUME}:/data" \
    "$IMAGE" >/dev/null

step "[3/7] Waiting for install wizard"
for i in $(seq 1 60); do
    code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 "${BASE}/install.php?m=install&f=step1" || true)
    [ "$code" = "200" ] && break
    [ "$i" = 60 ] && die "install wizard did not come up"
    sleep 5
done

step "[4/7] Configuring database"
req "${BASE}/install.php?m=install&f=step1" -o /dev/null
out=$(req "${BASE}/install.php?m=install&f=step2" \
    --data-urlencode 'timezone=Asia/Shanghai' \
    --data-urlencode 'defaultLang=zh-cn' \
    --data-urlencode 'dbDriver=mysql' \
    --data-urlencode 'dbHost=127.0.0.1' \
    --data-urlencode 'dbPort=3306' \
    --data-urlencode 'dbName=zentao' \
    --data-urlencode "dbUser=${DB_USER}" \
    --data-urlencode "dbPassword=${DB_PASSWORD}" \
    --data-urlencode 'dbPrefix=zt_' \
    --data-urlencode 'dbEncoding=utf8mb4' \
    --data-urlencode 'clearDB=1')
echo "$out" | json_has result success || die "step2 failed: $out"

step "[5/7] Creating database tables"
req "${BASE}/install.php?m=install&f=showTableProgress" -o /dev/null
for i in $(seq 1 300); do
    out=$(req "${BASE}/install.php?m=install&f=ajaxCreateTable" -d 'x=1')
    echo "$out" | json_has allChangesExecuted true && break
    echo "$out" | json_has result fail && die "table creation failed: $out"
    [ "$i" = 300 ] && die "table creation did not finish"
done

step "[6/7] Finishing install (mode=$MODE, account=$ADMIN_ACCOUNT)"
req "${BASE}/install.php?m=install&f=step3" -o /dev/null
req "${BASE}/install.php?m=install&f=step3" -d 'x=1' | json_has result success || die "step3 failed"
req "${BASE}/install.php?m=install&f=step4" -o /dev/null
req "${BASE}/install.php?m=install&f=step4" --data-urlencode "mode=${MODE}" | json_has result success || die "step4 failed"
req "${BASE}/install.php?m=install&f=step5" -o /dev/null
req "${BASE}/install.php?m=install&f=step5" \
    --data-urlencode 'company=TestEnv' \
    --data-urlencode 'flow=full' \
    --data-urlencode "account=${ADMIN_ACCOUNT}" \
    --data-urlencode "password=${ADMIN_PASSWORD}" | json_has result success || die "step5 failed"
req "${BASE}/install.php?m=install&f=step6" -o /dev/null

step "[7/7] Verifying API"
for i in $(seq 1 30); do
    version=$(curl -s --max-time 5 "${BASE}/?mode=getconfig" | sed -n 's/.*"version":"\([^"]*\)".*/\1/p')
    [ -n "$version" ] && break
    sleep 2
done
[ "$version" = "22.4" ] || die "unexpected version: '${version:-none}'"

token=$(curl -s --max-time 10 -H "Referer: ${BASE}/" -H 'Content-Type: application/json' \
    -d "{\"account\":\"${ADMIN_ACCOUNT}\",\"password\":\"${ADMIN_PASSWORD}\"}" \
    "${API}/users/login" | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')
[ -n "$token" ] || die "API login failed"

curl -s --max-time 10 -H "Token: ${token}" "${API}/products" | json_has status success \
    || die "API read failed"

step "READY: Zentao $version at ${BASE} (API: ${API}, account: ${ADMIN_ACCOUNT})"
