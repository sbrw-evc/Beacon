#!/bin/bash
set -euo pipefail

: "${REALM:=CORP.UMBRELLA.LAB}"
: "${DOMAIN:=CORP}"
: "${HOST_NAME:=dc1}"
: "${DNS_FORWARDER:=1.1.1.1}"
: "${ADMIN_PASSWORD:?set ADMIN_PASSWORD in .env}"
: "${SERVICE_PASSWORD:?set SERVICE_PASSWORD in .env}"
: "${USER_PASSWORD:?set USER_PASSWORD in .env}"

REALM=${REALM^^}
FQDN="$HOST_NAME.${REALM,,}"
PRIVATE=/var/lib/samba/private
TLS="$PRIVATE/umbrella-tls"

log() { echo "samba-dc: $*"; }

if [ ! -f "$PRIVATE/.umbrella-provisioned" ]; then
  find /var/lib/samba -mindepth 1 -delete
  rm -f /etc/samba/smb.conf
  samba-tool domain provision --server-role=dc --use-rfc2307 --dns-backend=SAMBA_INTERNAL \
    --realm="$REALM" --domain="$DOMAIN" --host-name="$HOST_NAME" --adminpass="$ADMIN_PASSWORD" \
    --option="dns forwarder = $DNS_FORWARDER" \
    --option="tls enabled = yes" \
    --option="tls keyfile = $TLS/server.key" \
    --option="tls certfile = $TLS/server.crt" \
    --option="tls cafile = $TLS/ca.crt" >/dev/null
  touch "$PRIVATE/.umbrella-provisioned"
  log "domain $REALM provisioned, DC $FQDN"
fi
cp "$PRIVATE/krb5.conf" /etc/krb5.conf

if [ ! -s "$TLS/server.crt" ]; then
  mkdir -p "$TLS"
  chmod 0700 "$TLS"
  openssl req -x509 -newkey rsa:3072 -nodes -days 3650 -subj "/CN=Umbrella Lab AD CA" \
    -keyout "$TLS/ca.key" -out "$TLS/ca.crt" -addext "basicConstraints=critical,CA:TRUE" \
    -addext "keyUsage=critical,keyCertSign,cRLSign" 2>/dev/null
  openssl req -newkey rsa:3072 -nodes -subj "/CN=$FQDN" -keyout "$TLS/server.key" -out "$TLS/server.csr" 2>/dev/null
  printf 'subjectAltName=DNS:%s,DNS:%s,DNS:localhost,IP:127.0.0.1\nextendedKeyUsage=serverAuth\nkeyUsage=critical,digitalSignature,keyEncipherment\n' \
    "$FQDN" "$HOST_NAME" > "$TLS/server.ext"
  openssl x509 -req -in "$TLS/server.csr" -CA "$TLS/ca.crt" -CAkey "$TLS/ca.key" -CAcreateserial -days 3650 \
    -extfile "$TLS/server.ext" -out "$TLS/server.crt" 2>/dev/null
  rm -f "$TLS/server.csr" "$TLS/server.ext"
  chmod 0600 "$TLS"/*.key
  log "TLS certificate issued for $FQDN"
fi
if [ -d /export ]; then
  cp "$TLS/ca.crt" /export/ca.pem
  chmod 0644 /export/ca.pem
fi

if [ ! -f "$PRIVATE/.umbrella-seeded" ]; then
  python3 /seed/seed.py
  touch "$PRIVATE/.umbrella-seeded"
fi

log "starting samba"
exec samba --foreground --no-process-group --debug-stdout
