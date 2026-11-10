#!/usr/bin/env python3
"""Measure authenticated UI GETs with curl; credentials stay in a cookie file."""
import argparse
import json
import math
import statistics
import subprocess
import time
from pathlib import Path

PAGES = ["/", "/events", "/events?tab=requests", "/events?tab=dhcp", "/machines",
         "/images", "/answers", "/storages", "/system", "/usb", "/installations",
         "/installations/new", "/menu", "/drivers", "/network", "/users", "/audit",
         "/account", "/answers/editor", "/ubuntu-autoinstall/new", "/login",
         "/search?q=windows&format=json", "/api/system/metrics"]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base-url", required=True)
    parser.add_argument("--cookie-file", type=Path, required=True)
    parser.add_argument("--samples", type=int, default=10)
    parser.add_argument("--insecure", action="store_true")
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--path", action="append", dest="paths")
    args = parser.parse_args()
    if args.samples < 1:
        parser.error("--samples must be positive")
    rows = []
    for path in args.paths or PAGES:
        samples = []
        for _ in range(args.samples):
            cmd = ["curl", "--silent", "--show-error", "--max-time", "45",
                   "--cookie", str(args.cookie_file), "--output", "/dev/null",
                   "--write-out", "%{json}", args.base_url.rstrip("/") + path]
            if args.insecure:
                cmd.insert(1, "--insecure")
            result = subprocess.run(cmd, capture_output=True, text=True)
            try:
                data = json.loads(result.stdout)
            except json.JSONDecodeError:
                data = {}
            samples.append({"status": data.get("http_code", 0), "exit": result.returncode,
                            "ttfb_ms": round(data.get("time_starttransfer", 0) * 1000, 3),
                            "total_ms": round(data.get("time_total", 0) * 1000, 3),
                            "bytes": data.get("size_download", 0)})
        times = sorted(v["ttfb_ms"] for v in samples)
        row = {"path": path, "first_ms": samples[0]["ttfb_ms"],
               "median_ms": statistics.median(times),
               "p95_ms": times[max(0, math.ceil(len(times) * .95) - 1)],
               "samples": samples}
        rows.append(row)
        print(f"{path}: first={row['first_ms']:.1f} median={row['median_ms']:.1f} "
              f"p95={row['p95_ms']:.1f} ms statuses={sorted(set(s['status'] for s in samples))}", flush=True)
        args.output.write_text(json.dumps({"measured_unix": time.time(), "base_url": args.base_url,
                                          "rows": rows}, indent=2) + "\n")


if __name__ == "__main__":
    main()
