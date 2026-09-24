#!/usr/bin/env python3
"""Draw a stratified, seeded sample of verified issues for labelling.

Takes N verified issues per category and saves a condensed excerpt of each
(title plus the text around the error) to labels/sample.jsonl.
"""
import glob, json, os, random, subprocess, sys

sys.path.insert(0, os.path.dirname(__file__))
from precision import MUST  # noqa: E402

N, SEED, WINDOW = 4, 20260924, 700

def fetch(url):
    path = url.replace("https://github.com/", "repos/")
    out = []
    for p in (path, path + "/comments?per_page=30"):
        r = subprocess.run(["gh", "api", p], capture_output=True, text=True)
        if r.returncode == 0:
            d = json.loads(r.stdout)
            for it in (d if isinstance(d, list) else [d]):
                out.append((it.get("body") or ""))
    return "\n---\n".join(out)

def excerpt(text, needle):
    low = text.lower()
    i = low.find(needle)
    if i < 0:
        return text[:WINDOW * 2]
    return text[max(0, i - WINDOW):i + WINDOW]

def main():
    here = os.path.dirname(__file__)
    rng = random.Random(SEED)
    out = open(os.path.join(here, "labels", "sample.jsonl"), "w")
    for path in sorted(glob.glob(os.path.join(here, "data", "*.json"))):
        d = json.load(open(path))
        pool = [i for i in d["sample"] if i.get("verified")]
        for item in rng.sample(pool, min(N, len(pool))):
            body = fetch(item["url"])
            out.write(json.dumps({
                "url": item["url"], "category": d["category"], "rule": d["rule"],
                "title": item["title"], "excerpt": excerpt(body, MUST[d["category"]][0]),
            }) + "\n")
        print(d["category"], file=sys.stderr)

if __name__ == "__main__":
    main()
