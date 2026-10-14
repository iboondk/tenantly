#!/bin/sh
# Runs on first container init. Migration 00001 creates tenantly_app as NOLOGIN,
# so on a fresh database the role does not exist yet and this script does nothing.
# After `make migrate`, re-run it (or set the password manually) to enable LOGIN.
set -eu

if [ -z "${APP_DB_PASSWORD:-}" ]; then
    echo "01-app-role: APP_DB_PASSWORD not set, skipping"
    exit 0
fi

role_exists=$(psql -tAq --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" \
    -c "SELECT 1 FROM pg_roles WHERE rolname = 'tenantly_app'")

if [ "$role_exists" = "1" ]; then
    psql -v ON_ERROR_STOP=1 -v pw="$APP_DB_PASSWORD" --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<'EOSQL'
ALTER ROLE tenantly_app LOGIN PASSWORD :'pw';
EOSQL
    echo "01-app-role: tenantly_app set to LOGIN"
else
    echo "01-app-role: tenantly_app does not exist yet (migrations create it), skipping"
fi
