FROM debian:trixie-slim
RUN apt-get update \
 && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
      samba samba-ad-dc samba-ad-provision samba-dsdb-modules samba-vfs-modules python3-samba python3-setproctitle winbind krb5-user ldb-tools openssl ca-certificates \
 && rm -rf /var/lib/apt/lists/* \
 && rm -f /etc/samba/smb.conf
COPY entrypoint.sh /entrypoint.sh
COPY seed /seed
RUN sed -i 's/\r$//' /entrypoint.sh /seed/*.py \
 && chmod 0755 /entrypoint.sh
EXPOSE 389 636
VOLUME ["/var/lib/samba", "/etc/samba"]
ENTRYPOINT ["/entrypoint.sh"]
