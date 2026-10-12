#!/bin/sh
set -eu
CFG=/var/lib/ldap-account-manager/config
cp "$CFG/windows_samba4.sample.conf" "$CFG/lam.conf"
sed -i \
  -e "s|^activeTypes:.*|activeTypes: user,group,host|" \
  -e "s|^types: attr_user:.*|types: attr_user: #sAMAccountName;#displayName;#title;#department;#mail|" \
  -e "s|^types: suffix_host:.*|types: suffix_host: CN=Computers,${LDAP_BASE_DN}|" \
  "$CFG/lam.conf"
chown www-data "$CFG/lam.conf"
sed -i "s|^TLS_CACERT.*|TLS_CACERT /certs/ca.pem|" /etc/ldap/ldap.conf
exec /usr/local/bin/start.sh
