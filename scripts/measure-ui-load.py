#!/usr/bin/env python3
"""Read-only UI load probe. Run in the isolated fixture, never seed real databases.

Times use a persistent HTTPS connection per client. header_ms measures complete
response headers (an upper bound on TTFB), total_ms includes consuming the body.
Redirects are deliberately not followed. Only fixture cookies are accepted.
"""
import argparse
import csv
import http.client
import io
import json
import math
import re
import ssl
import statistics
import subprocess
import threading
import time
from collections import defaultdict
from html.parser import HTMLParser
from pathlib import Path
from urllib.parse import parse_qsl, urlencode, urlsplit, urlunsplit


class Page(HTMLParser):
    def __init__(self):
        super().__init__()
        self.rows, self.sentinel, self.source_error = [], None, False
        self.row = self.cell = None

    def handle_starttag(self, tag, attrs):
        attrs = dict(attrs)
        classes = attrs.get("class", "").split()
        if "events-error-block" in classes or ("error" in classes and "hidden" not in attrs) or "data-events-retry" in attrs:
            self.source_error = True
        if "infinite-scroll-sentinel" in attrs.get("class", "").split():
            self.sentinel = attrs
        if tag == "tr":
            self.row = []
        if tag == "td" and self.row is not None:
            self.cell = []

    def handle_data(self, data):
        if self.cell is not None:
            self.cell.append(data)

    def handle_endtag(self, tag):
        if tag == "td" and self.cell is not None:
            self.row.append("".join(self.cell).strip())
            self.cell = None
        if tag == "tr" and self.row is not None:
            if self.row:
                self.rows.append(self.row)
            self.row = None


def parse_page(html):
    p = Page()
    p.feed(html)
    return p


def next_path(s):
    if not s or s.get("data-has-more") != "true":
        return None
    u = urlsplit(s["data-url"])
    q = dict(parse_qsl(u.query))
    q["partial"] = "1"
    for attr, key in [("data-next-offset", "offset"), ("data-before-id", "before_id"),
                      ("data-before-time", "before_time")]:
        if s.get(attr):
            q[key] = s[attr]
    return urlunsplit(("", "", u.path, urlencode(q), ""))


def success(status, body, path="", expected_rows=None):
    if status != 200 or b'<form action="/login"' in body or b'name="password"' in body:
        return False
    u = urlsplit(path)
    page = parse_page(body.decode("utf-8", errors="replace"))
    if page.source_error:
        return False
    if expected_rows is not None and len(page.rows) != expected_rows:
        return False
    if u.path == "/events/content":
        if re.search(rb'class="[^"]*\berror\b', body):
            return False
        if dict(parse_qsl(u.query)).get("partial") == "1":
            return b'infinite-scroll-sentinel' in body
        return b'<table' in body and b'<tbody' in body
    if u.path == "/machines":
        return b'infinite-scroll-sentinel' in body
    return True


def check_ids(actual, expected):
    assert len(actual) == len(set(actual)), "duplicate rows"
    assert set(actual) == expected, f"missing/unexpected rows: {len(actual)} != {len(expected)}"


class Client:
    def __init__(self, base, token):
        u = urlsplit(base)
        if u.scheme != "https" or u.path not in ("", "/"):
            raise ValueError("HTTPS origin required")
        self.host, self.port = u.hostname, u.port or 443
        self.token = token.strip()
        self.context = ssl._create_unverified_context()  # private fixture certificate
        self.conn = None

    def get(self, path, expected_rows=None):
        if not path.startswith("/") or path.startswith("//"):
            raise ValueError("relative path required")
        start = time.perf_counter()
        try:
            if self.conn is None:
                self.conn = http.client.HTTPSConnection(self.host, self.port, timeout=15, context=self.context)
            self.conn.request("GET", path, headers={"Cookie": "omb_session=" + self.token})
            response = self.conn.getresponse()
            headers = time.perf_counter()
            body = response.read()
            end = time.perf_counter()
            return {"path": path, "status": response.status, "header_ms": (headers-start)*1000,
                    "total_ms": (end-start)*1000, "bytes": len(body),
                    "ok": success(response.status, body, path, expected_rows)}, body
        except (OSError, http.client.HTTPException) as exc:
            self.close()
            return {"path": path, "status": 0, "header_ms": (time.perf_counter()-start)*1000,
                    "total_ms": (time.perf_counter()-start)*1000, "bytes": 0, "ok": False,
                    "error": type(exc).__name__}, b""

    def close(self):
        if self.conn:
            self.conn.close()
            self.conn = None


def walk(client, path, kind):
    ids, page_sizes, urls = [], [], set()
    while path:
        assert path not in urls, "pagination cycle"
        urls.add(path)
        metric, body = client.get(path)
        assert metric["ok"], metric
        p = parse_page(body.decode())
        page_sizes.append(len(p.rows))
        assert len(p.rows) <= 50, f"unbounded {kind} page"
        for row in p.rows:
            if kind == "machines":
                matches = re.findall(r"02:7e:57:00:[0-9a-f]{2}:[0-9a-f]{2}", row[0])
            elif kind == "requests":
                matches = re.findall(r"/load/\d{6}", " ".join(row))
            else:
                matches = re.findall(r"load-\d{6}", " ".join(row))
            assert len(matches) == 1, (kind, row)
            ids.extend(matches)
        path = next_path(p.sentinel)
    return ids, page_sizes


def validate(client, manifest):
    n, e, d = (manifest[k] for k in ("machines", "boot_events", "journal_lines"))
    expected_macs = {f"02:7e:57:00:{i//256:02x}:{i%256:02x}" for i in range(1, n+1)}
    result = {}
    for kind, path, expected in [
        ("machines", "/machines", expected_macs),
        ("requests", "/events/content?tab=requests", {f"/load/{i:06d}" for i in range(1, e+1)}),
        ("dhcp", "/events/content?tab=dhcp", {f"load-{i:06d}" for i in range(max(1,d-999), d+1)}),
    ]:
        ids, sizes = walk(client, path, kind)
        check_ids(ids, expected)
        result[kind] = {"rows": len(ids), "pages": len(sizes), "first_rows": sizes[0],
                        "duplicates": 0, "missing": 0}
    metric, body = client.get("/machines.csv")
    assert metric["ok"], metric
    rows = list(csv.reader(io.StringIO(body.decode("utf-8-sig")), delimiter=";"))
    assert len(rows) == n+1, f"CSV truncated: {len(rows)-1}/{n}"
    check_ids([r[0] for r in rows[1:]], expected_macs)
    result["csv_rows"] = len(rows)-1
    metric, body = client.get("/machines?q=load-0001")
    assert metric["ok"] and "load-0001" in body.decode(), "search mismatch"
    assert len(parse_page(body.decode()).rows) == 1, "search not unique"
    metric, body = client.get("/events/machines/02:7e:57:00:00:01")
    assert metric["ok"] and b"load-0001" in body, "machine card missing"
    result["search_and_card"] = True
    return result


def summarize(samples):
    groups = defaultdict(list)
    for row in samples:
        groups[row["path"]].append(row)
    result = {}
    for path, rows in groups.items():
        entry = {"requests": len(rows), "errors": sum(not r["ok"] for r in rows),
                 "statuses": sorted(set(r["status"] for r in rows))}
        for field in ("header_ms", "total_ms"):
            times = sorted(r[field] for r in rows)
            entry[field] = {"p50": round(statistics.median(times),3),
                            "p95": round(times[math.ceil(.95*len(times))-1],3),
                            "max": round(max(times),3)}
        result[path] = entry
    return result


def memory(units):
    data = {}
    for unit in units:
        props = dict(line.split("=",1) for line in subprocess.check_output(
            ["systemctl", "show", unit, "-p", "MainPID", "-p", "NRestarts", "-p", "MemoryCurrent", "-p", "ControlGroup"],
            text=True).splitlines() if "=" in line)
        pid = int(props["MainPID"])
        item = {"pid": pid, "restarts": int(props["NRestarts"]), "cgroup_bytes": props["MemoryCurrent"]}
        if pid:
            for line in Path(f"/proc/{pid}/status").read_text().splitlines():
                if line.startswith(("VmRSS:", "VmHWM:", "Threads:")):
                    key, value = line.split(":",1)
                    item[key] = int(value.strip().split()[0])
            for line in Path("/sys/fs/cgroup" + props["ControlGroup"] + "/memory.stat").read_text().splitlines():
                key, value = line.split()
                if key in ("anon", "file", "kernel"):
                    item[key] = int(value)
        data[unit] = item
    return {"unix": time.time(), "units": data}


def check_processes(samples):
    first = samples[0]["units"]
    for sample in samples:
        for unit, expected in first.items():
            current = sample["units"][unit]
            assert current["pid"] > 0 and current["pid"] == expected["pid"], f"process changed: {unit}"
            assert current["restarts"] == expected["restarts"], f"process restarted: {unit}"


PATHS = ["/events", "/machines", "/machines?partial=1&offset=50", "/machines?q=load-0001",
         "/events/content?tab=requests", "/events/content?tab=requests&partial=1&before_id=101",
         "/events/content?tab=dhcp", "/events/content?tab=dhcp&partial=1&offset=50",
         "/events/content?tab=machines", "/events/machines/02:7e:57:00:00:01", "/menu", "/menu/versions"]


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--base-url", required=True)
    p.add_argument("--cookie-file", type=Path, required=True)
    p.add_argument("--manifest", type=Path, required=True)
    p.add_argument("--output", type=Path, required=True)
    p.add_argument("--unit", action="append", default=[])
    p.add_argument("--phases", default="1:60,5:120,10:420")
    p.add_argument("--pause", type=float, default=.25)
    p.add_argument("--cooldown", type=int, default=60)
    p.add_argument("--validate-only", action="store_true")
    args = p.parse_args()
    token = args.cookie_file.read_text().strip()
    manifest = json.loads(args.manifest.read_text())
    client = Client(args.base_url, token)
    result = {"manifest": manifest, "started_unix": time.time(), "metrics": "header_ms = complete headers; upper bound on TTFB",
              "pause_seconds": args.pause, "memory": [memory(args.unit)], "phases": []}
    result["validation"] = validate(client, manifest)
    # Exercise both cursor fields in the sustained workload as in the full walk.
    _, first_requests = client.get("/events/content?tab=requests")
    PATHS[5] = next_path(parse_page(first_requests.decode()).sentinel)
    assert PATHS[5], "fixture must have at least two HTTP pages"
    expected = {PATHS[1]: min(50, manifest["machines"]),
                PATHS[2]: min(50, max(0, manifest["machines"]-50)), PATHS[3]: 1,
                PATHS[4]: 50, PATHS[5]: min(50, manifest["boot_events"]-50),
                PATHS[6]: min(50, manifest["journal_lines"]),
                PATHS[7]: min(50, max(0, min(1000, manifest["journal_lines"])-50)),
                PATHS[8]: manifest["machines"]}
    result["expected_rows"] = expected
    result["initial"] = [client.get(path, expected.get(path))[0] for path in PATHS]
    assert all(r["ok"] for r in result["initial"]), "initial requests failed"
    client.close()
    print("validation", json.dumps(result["validation"]), flush=True)
    def save():
        args.output.write_text(json.dumps(result, indent=2) + "\n")
    save()
    if args.validate_only:
        return
    sample_file = args.output.with_suffix(".requests.jsonl")
    with sample_file.open("w") as raw:
        for phase in args.phases.split(","):
            count, duration = map(int, phase.split(":"))
            assert count > 0 and duration > 0
            start = time.monotonic()
            deadline = start + duration
            samples, lock = [], threading.Lock()
            def worker(index):
                c = Client(args.base_url, token)
                step = index
                while time.monotonic() < deadline:
                    path = PATHS[step % len(PATHS)]
                    row, _ = c.get(path, expected.get(path))
                    row.update(unix=time.time(), clients=count)
                    with lock:
                        samples.append(row)
                        raw.write(json.dumps(row) + "\n")
                    step += 1
                    time.sleep(args.pause)
                c.close()
            threads = [threading.Thread(target=worker, args=(i,)) for i in range(count)]
            for thread in threads:
                thread.start()
            while any(t.is_alive() for t in threads):
                result["memory"].append(memory(args.unit))
                for thread in threads:
                    thread.join(timeout=5/count)
            result["phases"].append({"clients": count, "seconds": time.monotonic()-start,
                "requests": len(samples), "errors": sum(not r["ok"] for r in samples), "routes": summarize(samples)})
            raw.flush()
            save()
            print(f"phase {count}: {len(samples)} requests, {sum(not r['ok'] for r in samples)} errors", flush=True)
    for _ in range(args.cooldown//5):
        time.sleep(5)
        result["memory"].append(memory(args.unit))
    result["finished_unix"] = time.time()
    check_processes(result["memory"])
    result["processes_stable"] = True
    save()
    if any(phase["errors"] for phase in result["phases"]):
        raise SystemExit(1)


if __name__ == "__main__":
    main()
