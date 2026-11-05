#!/bin/sh
set -eu
export DEMO_READ_TOKEN="$(cat /run/secrets/observability_token)"
export GF_SECURITY_ADMIN_PASSWORD="$(cat /run/secrets/grafana_admin_password)"
unset GF_SECURITY_ADMIN_PASSWORD__FILE
chown -R 472:0 /var/lib/grafana
exec su -p -s /bin/sh grafana -c 'exec /run.sh'
