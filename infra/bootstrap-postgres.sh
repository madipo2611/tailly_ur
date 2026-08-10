#!/usr/bin/env bash
set -Eeuo pipefail

# Run as root on the PostgreSQL host: ./bootstrap-postgres.sh <cluster-egress-ip>
cluster_ip="${1:?cluster egress IP is required}"
app_dir=/opt/digital-notary
password_file="$app_dir/postgres-password"
cert_file=/etc/postgresql/18/main/digital-notary.crt
key_file=/etc/postgresql/18/main/digital-notary.key
hba_file=/etc/postgresql/18/main/pg_hba.conf

install -d -m 0700 "$app_dir"
if [[ ! -s "$password_file" ]]; then
  umask 077
  openssl rand -hex 32 >"$password_file"
fi
password=$(<"$password_file")

if ! sudo -u postgres psql -Atqc "SELECT 1 FROM pg_roles WHERE rolname = 'digital_notary'" | grep -qx 1; then
  sudo -u postgres psql -c "CREATE ROLE digital_notary LOGIN PASSWORD '$password'"
else
  sudo -u postgres psql -c "ALTER ROLE digital_notary PASSWORD '$password'"
fi
if ! sudo -u postgres psql -Atqc "SELECT 1 FROM pg_database WHERE datname = 'digital_notary'" | grep -qx 1; then
  sudo -u postgres createdb -O digital_notary digital_notary
fi

if [[ ! -s "$cert_file" || ! -s "$key_file" ]]; then
  openssl req -x509 -newkey rsa:4096 -sha256 -nodes -days 825 \
    -subj '/CN=80.78.241.215' \
    -addext 'subjectAltName=IP:80.78.241.215' \
    -keyout "$key_file" -out "$cert_file"
  chown postgres:postgres "$cert_file" "$key_file"
  chmod 0600 "$key_file"
  chmod 0644 "$cert_file"
fi

sudo -u postgres psql -c "ALTER SYSTEM SET listen_addresses = '*'"
sudo -u postgres psql -c "ALTER SYSTEM SET ssl_cert_file = '$cert_file'"
sudo -u postgres psql -c "ALTER SYSTEM SET ssl_key_file = '$key_file'"
hba_rule="hostssl digital_notary digital_notary ${cluster_ip}/32 scram-sha-256"
grep -Fqx "$hba_rule" "$hba_file" || printf '\n%s\n' "$hba_rule" >>"$hba_file"
systemctl restart postgresql

if command -v ufw >/dev/null && ufw status | grep -qx 'Status: active'; then
  ufw allow from "$cluster_ip" to any port 5432 proto tcp
fi

sudo -u postgres psql -d digital_notary -Atqc 'SELECT current_database(), current_user'
echo "PostgreSQL ready; client CA: $cert_file"
