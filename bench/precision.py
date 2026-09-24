#!/usr/bin/env python3
"""Estimate how many search hits really contain each category's error.

GitHub issue search matches words loosely, so raw counts overstate
prevalence. For every sampled issue, this fetches the body and comments and
checks for the category's exact error text. precision = verified / sampled,
and adjusted = total × precision.
"""
import csv, glob, json, os, re, subprocess, sys

# Exact text (lowercase) that must appear for an issue to count.
MUST = {
    "platform: exec format error": ["exec format error"],
    "platform: no matching manifest": ["no matching manifest for"],
    "platform: requested platform mismatch": ["does not match the detected host platform"],
    "image: tag/repo not found": ["manifest unknown"],
    "image: pull access denied": ["pull access denied"],
    "credentials helper missing": ["error getting credentials"],
    "build: COPY source not found": ["failed to compute cache key"],
    "build: COPY failed legacy": ["copy failed: file not found in build context"],
    "build: outside build context": ["outside the build context"],
    "build: BuildKit required": ["requires buildkit"],
    "compose: required variable": ["is missing a value"],
    "compose: variable not set": ["variable is not set. defaulting to a blank string"],
    "compose: unsupported property": ["additional property", "not allowed"],
    "port already allocated": ["port is already allocated"],
    "address already in use": ["address already in use"],
    "rootless privileged port": ["cannot expose privileged port"],
    "bind: dir onto file": ["mount a directory onto a file"],
    "bind: source does not exist": ["bind source path does not exist"],
    "desktop: mounts denied": ["mounts denied"],
    "memory: OOMKilled": ["oomkilled"],
    "memory: mssql minimum": ["at least 2000 megabytes"],
    "disk: no space left": ["no space left on device"],
    "gpu: no device driver": ["could not select device driver"],
    "kernel: vm.max_map_count": ["vm.max_map_count", "too low"],
    "daemon: cannot connect": ["cannot connect to the docker daemon"],
    "emulation: qemu crash": ["qemu: uncaught target signal"],
    "volume permission denied": ["permission denied"],
    "DNS resolution in container": ["temporary failure in name resolution"],
    "container healthcheck unhealthy": ["is unhealthy"],
    "network not found": [re.compile(r"network \S+ (not found|declared as external, but could not be found)")],
    "OCI runtime exec failed": ["oci runtime exec failed"],
    "app config/env missing at runtime": ["superuser password is not specified"],
}

def text_of(url):
    path = url.replace("https://github.com/", "repos/").replace("/issues/", "/issues/", 1)
    parts = []
    for p in (path, path + "/comments?per_page=50"):
        r = subprocess.run(["gh", "api", p], capture_output=True, text=True)
        if r.returncode != 0:
            continue
        data = json.loads(r.stdout)
        items = data if isinstance(data, list) else [data]
        for it in items:
            parts.append((it.get("title") or "") + "\n" + (it.get("body") or ""))
    return "\n".join(parts).lower()

def main():
    here = os.path.join(os.path.dirname(__file__), "data")
    rows = []
    for path in sorted(glob.glob(os.path.join(here, "*.json"))):
        d = json.load(open(path))
        must = MUST[d["category"]]
        verified = 0
        for item in d["sample"]:
            if "verified" not in item:
                t = text_of(item["url"])
                item["verified"] = all((m.search(t) is not None) if hasattr(m, "search") else (m in t) for m in must)
            verified += item["verified"]
        n = len(d["sample"])
        d["precision"] = verified / n if n else 0
        d["adjusted"] = round(d["total"] * d["precision"])
        json.dump(d, open(path, "w"), indent=1)
        rows.append((d["category"], d["rule"], d["total"], n, verified, round(d["precision"], 2), d["adjusted"]))
        print(f"{d['adjusted']:>7} adj  {verified:>2}/{n:<2} {d['category']}", file=sys.stderr)
    with open(os.path.join(here, "counts.csv"), "w", newline="") as f:
        w = csv.writer(f)
        w.writerow(["category", "preflight_rule", "raw_hits", "sampled", "verified", "precision", "adjusted_estimate"])
        w.writerows(sorted(rows, key=lambda r: -r[6]))

if __name__ == "__main__":
    main()
