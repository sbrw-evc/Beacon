#!/bin/sh
set -eu

: "${BAO_ADDR:=http://openbao:8200}"
: "${STATE_DIR:=/state}"
: "${UMBRELLA_DIR:=/umbrella}"
: "${POLICY_FILE:=/config/umbrella-policy.hcl}"
: "${READY_FILE:=/tmp/openbao-ready}"
: "${WATCH:=true}"
export BAO_ADDR

log() { echo "openbao-init: $*"; }

state() {
  out=$(bao status -format=json 2>/dev/null) && code=0 || code=$?
  [ "$code" -ne 1 ] || return 1
  case "$out" in *'"initialized": false'*) echo uninitialized ;; *'"sealed": true'*) echo sealed ;; *) echo unsealed ;; esac
}

wait_api() {
  i=0
  until s=$(state); do
    i=$((i + 1))
    [ "$i" -lt 120 ] || { log "OpenBao does not answer at $BAO_ADDR"; exit 1; }
    sleep 2
  done
}

unseal() {
  for key in $(awk '/^Unseal Key/ {print $NF}' "$STATE_DIR/init.txt" | head -3); do
    bao operator unseal "$key" >/dev/null
  done
  log "unsealed"
}

umask 077
mkdir -p "$STATE_DIR" "$UMBRELLA_DIR"
chmod 0700 "$STATE_DIR" 2>/dev/null || true
chmod 0755 "$UMBRELLA_DIR" 2>/dev/null || true
wait_api
if [ "$s" = uninitialized ]; then
  [ ! -s "$STATE_DIR/init.txt" ] || { log "OpenBao is empty but $STATE_DIR/init.txt exists: refusing to overwrite the old keys"; exit 1; }
  bao operator init -key-shares=5 -key-threshold=3 > "$STATE_DIR/init.txt.tmp"
  mv "$STATE_DIR/init.txt.tmp" "$STATE_DIR/init.txt"
  log "initialized: 5 unseal keys (threshold 3) and the root token are in $STATE_DIR/init.txt"
  s=sealed
fi
[ -s "$STATE_DIR/init.txt" ] || { log "no $STATE_DIR/init.txt: unseal OpenBao by hand"; exit 1; }
[ "$s" != sealed ] || unseal

BAO_TOKEN=$(awk '/Initial Root Token/ {print $NF}' "$STATE_DIR/init.txt")
export BAO_TOKEN
i=0
until bao secrets list >/dev/null 2>&1; do
  i=$((i + 1))
  [ "$i" -lt 60 ] || { log "OpenBao is unsealed but not active"; exit 1; }
  sleep 1
done

bao secrets list | grep -q '^umbrella/' || { bao secrets enable -path=umbrella -version=2 kv >/dev/null; log "KV v2 enabled at umbrella/"; }
bao auth list | grep -q '^approle/' || { bao auth enable approle >/dev/null; log "AppRole enabled"; }
bao policy write umbrella "$POLICY_FILE" >/dev/null
bao write auth/approle/role/umbrella token_policies=umbrella token_ttl=1h token_max_ttl=24h \
  secret_id_ttl=0 secret_id_num_uses=0 >/dev/null

umask 022
bao read -field=role_id auth/approle/role/umbrella/role-id > "$UMBRELLA_DIR/role_id.tmp"
mv "$UMBRELLA_DIR/role_id.tmp" "$UMBRELLA_DIR/role_id"
if [ ! -s "$UMBRELLA_DIR/secret_id" ] || ! bao write -f auth/approle/role/umbrella/secret-id/lookup secret_id="$(cat "$UMBRELLA_DIR/secret_id")" >/dev/null 2>&1; then
  bao write -f -field=secret_id auth/approle/role/umbrella/secret-id > "$UMBRELLA_DIR/secret_id.tmp"
  mv "$UMBRELLA_DIR/secret_id.tmp" "$UMBRELLA_DIR/secret_id"
  log "new AppRole secret_id for Umbrella"
fi
chmod 0644 "$UMBRELLA_DIR/role_id" "$UMBRELLA_DIR/secret_id" 2>/dev/null || true

unset BAO_TOKEN

touch "$READY_FILE"
log "ready"
[ "$WATCH" = true ] || exec sleep 2147483647
while sleep 10; do
  if s=$(state) && [ "$s" = sealed ]; then
    log "sealed after a restart, unsealing"
    unseal
  fi
done
