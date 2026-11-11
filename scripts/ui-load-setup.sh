#!/usr/bin/env bash
# Private, disposable copies only. Invoke as root on the test host.
# Usage: ui-load-setup.sh setup|cleanup /var/lib/omb-ui-load-<unique-id>
set -euo pipefail
action=${1:?setup or cleanup}
root=${2:?private fixture directory}
[[ "$root" =~ ^/var/lib/omb-ui-load-[a-zA-Z0-9-]+$ ]] || { echo 'unsafe fixture path' >&2; exit 2; }
[[ $(id -u) == 0 ]] || { echo 'root required' >&2; exit 2; }
suffix=${root##*/}
db_suffix=${suffix//-/_}
control_db=${db_suffix}_control
boot_db=${db_suffix}_boot
pg() { runuser -u postgres -- "$@"; }
if [[ "$action" == cleanup ]]; then
    [[ -f "$root/fixture.marker" && $(cat "$root/fixture.marker") == "$root" ]] || exit 2
    for service in control boot agent; do
        [[ ! -f "$root/created-unit-$service" ]] || systemctl stop "$suffix-$service.service" || true
    done
    [[ ! -f "$root/created-control-db" ]] || pg dropdb --if-exists "$control_db"
    [[ ! -f "$root/created-boot-db" ]] || pg dropdb --if-exists "$boot_db"
    # Keep results and scripts for inspection, remove copied private data/secrets.
    rm -f "$root/cookie" "$root/curl-cookie" "$root/control.sql" "$root/tls.key" "$root/menu.ipxe"
    echo 'fixture processes, databases and secrets removed'
    exit
fi
[[ "$action" == setup && -d "$root" && ! -e "$root/fixture.marker" ]] || exit 2
[[ "$control_db" =~ ^omb_ui_load_[a-zA-Z0-9_]+_control$ && ${#control_db} -lt 63 ]] || exit 2
[[ "$boot_db" =~ ^omb_ui_load_[a-zA-Z0-9_]+_boot$ && ${#boot_db} -lt 63 ]] || exit 2
printf '%s' "$root" > "$root/fixture.marker"
chmod 700 "$root"
pg createdb "$control_db"
touch "$root/created-control-db"
pg createdb "$boot_db"
touch "$root/created-boot-db"
pg pg_dump --no-owner --no-privileges omb_control | pg psql -v ON_ERROR_STOP=1 -q "$control_db"
pg pg_dump --schema-only --no-owner --no-privileges omb_boot | pg psql -v ON_ERROR_STOP=1 -q "$boot_db"
python3 "$root/ui-load-fixture.py" "$root"
pg psql -v ON_ERROR_STOP=1 -q "$control_db" < "$root/control.sql"
pg psql -v ON_ERROR_STOP=1 -q "$boot_db" < "$root/boot.sql"
mkdir "$root/bin" "$root/assets"
cat > "$root/bin/journalctl" <<EOF
#!/bin/sh
exec /usr/bin/python3 '$root/ui-load-fixture.py' journal "\$@"
EOF
chmod 755 "$root/bin/journalctl"
cp /var/lib/omb-boot/menu.ipxe "$root/menu.ipxe"
openssl req -x509 -newkey rsa:2048 -nodes -keyout "$root/tls.key" -out "$root/tls.crt" -days 1 -subj '/CN=OMB load fixture' -addext 'subjectAltName=IP:127.0.0.1' >/dev/null 2>&1
chown -R postgres:postgres "$root"
run() {
    local service=$1
    shift
    systemd-run --quiet --collect --unit="$suffix-$service" --property=User=postgres \
        --property=WorkingDirectory="$root" --property=NoNewPrivileges=yes \
        --property=ProtectSystem=strict --property=ReadWritePaths="$root" \
        --property=ProtectHome=yes --property=MemoryAccounting=yes "$@"
    touch "$root/created-unit-$service"
}
run agent /usr/bin/env "PATH=$root/bin:/usr/bin:/bin" "OMB_LOAD_JOURNAL=$root/journal.jsonl" \
    "$root/ui-load-agent" -socket "$root/agent.sock"
run boot /usr/bin/env "OMB_BOOT_DATABASE_URL=host=/var/run/postgresql user=postgres dbname=$boot_db" \
    "OMB_BOOT_UBUNTU_REGISTRY=$root/registry" "OMB_BOOT_UBUNTU_ISO=$root/absent.iso" \
    /usr/local/bin/omb-boot -listen 127.0.0.1:18080 -control-socket "$root/boot.sock" \
    -assets "$root/assets" -snapshot "$root/menu.ipxe" -base-url http://127.0.0.1:18080
for _ in $(seq 1 30); do
    [[ -S "$root/agent.sock" && -S "$root/boot.sock" ]] && break
    sleep .2
done
run control /usr/bin/env "OMB_CONTROL_DATABASE_URL=host=/var/run/postgresql user=postgres dbname=$control_db" \
    "OMB_CONTROL_BOOT_BASE_URL=http://127.0.0.1:18080" \
    /usr/local/bin/omb-control -listen 127.0.0.1:18443 -tls-cert "$root/tls.crt" -tls-key "$root/tls.key" \
    -boot-socket "$root/boot.sock" -agent-socket "$root/agent.sock" \
    -images-socket "$root/absent-images.sock" -allowed-interfaces fixture
for _ in $(seq 1 60); do
    if curl -ks --max-time 2 https://127.0.0.1:18443/login >/dev/null; then
        echo "fixture ready: $suffix (HTTPS loopback 18443)"
        exit
    fi
    sleep .2
done
echo 'fixture startup failed; use cleanup after inspecting unit logs' >&2
exit 1
