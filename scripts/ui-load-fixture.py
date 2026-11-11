#!/usr/bin/env python3
"""Generate deterministic load fixtures; journal subcommand streams cursor reads.

Used as the private Agent's journalctl executable, never installed system-wide.
"""
import argparse
import hashlib
import json
import os
import secrets
import sys
import time
from pathlib import Path


def mac(i):
    return f"02:7e:57:00:{i // 256:02x}:{i % 256:02x}"


def generate(root, machines, events, dhcp):
    root.mkdir(parents=True, exist_ok=True)
    token = secrets.token_hex(32)
    cookie = root / "cookie"
    cookie.touch(mode=0o600)
    cookie.write_text(token)
    sql = ["TRUNCATE machines; DELETE FROM sessions; DELETE FROM users WHERE username='ui-load-tester';",
           "INSERT INTO users(username,password_hash,role) VALUES('ui-load-tester','disabled-login','admin');",
           "INSERT INTO sessions(token_hash,user_id,expires_at) SELECT '" + hashlib.sha256(token.encode()).hexdigest() +
           "',id,now()+interval '4 hours' FROM users WHERE username='ui-load-tester';"]
    for i in range(1, machines + 1):
        sql.append("INSERT INTO machines(mac,first_seen,last_seen,last_ip,firmware,manufacturer,product,serial,name,note,manual) VALUES"
                   f"('{mac(i)}',now()-interval '1 day',now()-interval '{i} seconds','198.18.{i // 250}.{i % 250 + 1}',"
                   f"'{('BIOS' if i % 3 == 0 else 'UEFI')}','Fixture','Load Model {i % 12}','LOAD-{i:04d}','load-{i:04d}','Синтетическая машина',true);")
    (root / "control.sql").write_text("\n".join(sql) + "\n")
    # Both fields of the cursor are needed: 100 consecutive events share a time.
    boot = ["TRUNCATE boot_events RESTART IDENTITY;",
            "INSERT INTO boot_events(occurred_at,kind,path,remote_addr,mac,status,bytes,size) SELECT "
            "date_trunc('second',now())-interval '1 hour'+(i/100)*interval '1 second','menu',"
            "'/load/'||lpad(i::text,6,'0'),'198.18.0.1:12345',"
            "'02:7e:57:00:'||lpad(to_hex(((i-1)%" + str(machines) + "+1)/256),2,'0')||':'||"
            "lpad(to_hex(((i-1)%" + str(machines) + "+1)%256),2,'0'),200,1024,1024 "
            f"FROM generate_series(1,{events}) i; ANALYZE boot_events;"]
    (root / "boot.sql").write_text("\n".join(boot) + "\n")
    base = int(time.time() * 1_000_000) - dhcp * 1_000_000
    with (root / "journal.jsonl").open("w") as f:
        for i in range(1, dhcp + 1):
            client = (i - 1) % machines + 1
            tftp = i % 4 == 0
            entry = {"__CURSOR": f"fixture-{i}", "__REALTIME_TIMESTAMP": str(base + i * 1_000_000),
                     "SYSLOG_IDENTIFIER": "dnsmasq-tftp" if tftp else "dnsmasq-dhcp",
                     "MESSAGE": f"sent /srv/omb/tftp/load-{i:06d} to 198.18.{client // 250}.{client % 250 + 1}" if tftp else
                     f"DHCPACK(fixture) 198.18.{client // 250}.{client % 250 + 1} {mac(client)} load-{i:06d}"}
            f.write(json.dumps(entry) + "\n")
    (root / "manifest.json").write_text(json.dumps({"machines": machines, "boot_events": events,
        "journal_lines": dhcp, "dhcp_snapshot_limit": 1000, "generated_unix": time.time()}, indent=2) + "\n")


def journal(args):
    # NewDHCPLog verifies the cursor line, so include it on resumed reads.
    start = args[args.index("--cursor") + 1] if "--cursor" in args else None
    found = start is None
    with Path(os.environ["OMB_LOAD_JOURNAL"]).open() as f:
        for line in f:
            if not found and json.loads(line)["__CURSOR"] == start:
                found = True
            if found:
                sys.stdout.write(line)
    if not found:
        sys.exit(1)


if __name__ == "__main__":
    if len(sys.argv) > 1 and sys.argv[1] == "journal":
        journal(sys.argv[2:])
    else:
        p = argparse.ArgumentParser(description=__doc__)
        p.add_argument("root", type=Path)
        p.add_argument("--machines", type=int, default=500)
        p.add_argument("--events", type=int, default=20000)
        p.add_argument("--dhcp", type=int, default=10000)
        a = p.parse_args()
        if not (1 <= a.machines <= 65535 and a.events > 0 and a.dhcp > 0):
            p.error("invalid fixture size")
        generate(a.root, a.machines, a.events, a.dhcp)
