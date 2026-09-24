#!/usr/bin/env python3
"""Mine GitHub issues for container deployment failures.

For each failure category, searches GitHub issues for the error message Docker
prints, records how many issues match (a prevalence proxy) and saves a sample
for manual labelling. Categories preflight does not cover are included on
purpose, so coverage is measured against all common failures, not only the
ones the tool was designed for.

Requires the `gh` CLI, logged in. Usage: python3 bench/mine.py [--sample N]
"""
import argparse, csv, json, os, subprocess, sys, time, urllib.parse

# category, preflight rule covering it ("" = not covered), search phrase
CATEGORIES = [
    ("platform: exec format error",        "image.platform",      '"exec format error" docker'),
    ("platform: no matching manifest",     "image.platform",      '"no matching manifest for"'),
    ("platform: requested platform mismatch", "image.platform",   '"does not match the detected host platform"'),
    ("image: tag/repo not found",          "image.exists",        '"manifest unknown" docker'),
    ("image: pull access denied",          "image.exists",        '"pull access denied" "may require"'),
    ("credentials helper missing",         "registry.credentials",'"error getting credentials" "docker-credential"'),
    ("build: COPY source not found",       "build.context",       '"failed to compute cache key" "not found"'),
    ("build: COPY failed legacy",          "build.context",       '"COPY failed: file not found in build context"'),
    ("build: outside build context",       "build.context",       '"forbidden path outside the build context"'),
    ("build: BuildKit required",           "build.buildkit",      '"requires BuildKit"'),
    ("compose: required variable",         "compose.env",         '"required variable" "is missing a value"'),
    ("compose: variable not set",          "compose.env",         '"variable is not set. Defaulting to a blank string"'),
    ("compose: unsupported property",      "compose.version",     '"additional property" "is not allowed" compose'),
    ("port already allocated",             "ports.conflict,ports.host", '"port is already allocated"'),
    ("address already in use",             "ports.host",          '"address already in use" docker'),
    ("rootless privileged port",           "ports.host",          '"cannot expose privileged port"'),
    ("bind: dir onto file",                "mounts.bind",         '"Are you trying to mount a directory onto a file"'),
    ("bind: source does not exist",        "mounts.bind",         '"bind source path does not exist"'),
    ("desktop: mounts denied",             "mounts.sharing",      '"Mounts denied" "is not shared from the host"'),
    ("memory: OOMKilled",                  "resources.memory",    'OOMKilled "exit code 137"'),
    ("memory: mssql minimum",              "resources.memory",    '"requires a machine with at least 2000 megabytes"'),
    ("disk: no space left",                "resources.disk,host.disk", '"no space left on device" docker pull'),
    ("gpu: no device driver",              "resources.gpu",       '"could not select device driver" "capabilities: [[gpu]]"'),
    ("kernel: vm.max_map_count",           "kernel.sysctl",       '"vm.max_map_count" "is too low"'),
    ("daemon: cannot connect",             "(root cause)",        '"Cannot connect to the Docker daemon"'),
    ("emulation: qemu crash",              "image.platform",      '"qemu: uncaught target signal"'),
    # Not covered by preflight: measured to keep coverage claims honest.
    ("volume permission denied",           "",                    'docker volume "permission denied" container'),
    ("DNS resolution in container",        "",                    '"Temporary failure in name resolution" docker'),
    ("container healthcheck unhealthy",    "",                    '"is unhealthy" "dependency failed to start"'),
    ("network not found",                  "",                    'docker "network" "not found" compose'),
    ("OCI runtime exec failed",            "",                    '"OCI runtime exec failed"'),
    ("app config/env missing at runtime",  "(learned)",           '"Database is uninitialized and superuser password is not specified"'),
]

def gh_api(path):
    for attempt in range(5):
        r = subprocess.run(["gh", "api", "-H", "Accept: application/vnd.github+json", path],
                           capture_output=True, text=True)
        if r.returncode == 0:
            return json.loads(r.stdout)
        if "rate limit" in r.stderr.lower() or "403" in r.stderr:
            time.sleep(20 * (attempt + 1))
            continue
        raise RuntimeError(r.stderr.strip())
    raise RuntimeError("rate limited")

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--sample", type=int, default=30)
    ap.add_argument("--since", default="2021-01-01")
    ap.add_argument("--out", default=os.path.join(os.path.dirname(__file__), "data"))
    args = ap.parse_args()
    os.makedirs(args.out, exist_ok=True)

    rows = []
    for cat, rule, phrase in CATEGORIES:
        q = f"{phrase} is:issue created:>={args.since}"
        res = gh_api("search/issues?per_page=%d&q=%s" % (args.sample, urllib.parse.quote(q)))
        items = [{
            "url": i["html_url"], "title": i["title"],
            "repo": i["repository_url"].split("/repos/")[1],
            "created": i["created_at"], "state": i["state"], "comments": i["comments"],
        } for i in res.get("items", [])]
        slug = "".join(c if c.isalnum() else "-" for c in cat.lower()).strip("-")
        with open(os.path.join(args.out, f"{slug}.json"), "w") as f:
            json.dump({"category": cat, "rule": rule, "query": q,
                       "total": res["total_count"], "sample": items}, f, indent=1)
        rows.append((cat, rule, res["total_count"]))
        print(f"{res['total_count']:>7}  {cat}", file=sys.stderr)
        time.sleep(2.5)  # search API: 30 requests/minute

    with open(os.path.join(args.out, "counts.csv"), "w", newline="") as f:
        w = csv.writer(f)
        w.writerow(["category", "preflight_rule", "github_issues_since_" + args.since])
        w.writerows(sorted(rows, key=lambda r: -r[2]))

if __name__ == "__main__":
    main()
