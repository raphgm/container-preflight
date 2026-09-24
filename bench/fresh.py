#!/usr/bin/env python3
"""Draw a held-out test sample, disjoint from the development sample.

Uses the same categories and exact-text verification as mine.py and
precision.py, but a different search ordering (oldest first), excludes every
issue seen during development, and picks N verified issues per category with
a new seed. Run only after the rules are frozen (git tag eval-frozen).
"""
import glob, json, os, random, subprocess, sys, urllib.parse

sys.path.insert(0, os.path.dirname(__file__))
from mine import CATEGORIES, gh_api          # noqa: E402
from precision import MUST, text_of          # noqa: E402
from sample import excerpt                   # noqa: E402

N, SEED, PAGE = 4, 777013, 50
here = os.path.dirname(__file__)
seen = set()
for p in glob.glob(os.path.join(here, "data", "*.json")):
    seen |= {i["url"] for i in json.load(open(p))["sample"]}

rng = random.Random(SEED)
out = open(os.path.join(here, "labels", "test_sample.jsonl"), "w")
for cat, rule, phrase in CATEGORIES:
    q = f"{phrase} is:issue created:>=2021-01-01"
    res = gh_api("search/issues?per_page=%d&sort=created&order=asc&q=%s" % (PAGE, urllib.parse.quote(q)))
    pool = [i for i in res.get("items", []) if i["html_url"] not in seen]
    rng.shuffle(pool)
    picked = 0
    for it in pool:
        if picked == N:
            break
        body = text_of(it["html_url"])
        if not all((m.search(body) is not None) if hasattr(m, "search") else (m in body) for m in MUST[cat]):
            continue
        out.write(json.dumps({"url": it["html_url"], "category": cat, "rule": rule,
                              "title": it["title"], "excerpt": excerpt(body, MUST[cat][0])}) + "\n")
        picked += 1
    print(f"{picked}  {cat}", file=sys.stderr)
