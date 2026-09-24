#!/usr/bin/env python3
"""Write the paper's category table from data/counts.csv."""
import csv, os
here = os.path.dirname(__file__)
rows = list(csv.DictReader(open(os.path.join(here, "data", "counts.csv"))))
esc = lambda s: s.replace("_", r"\_").replace("&", r"\&").replace("%", r"\%")
out = [r"\begin{table}[t]", r"\centering\scriptsize",
       r"\caption{Failure categories: GitHub issues since 2021 matching each error, precision of the search (share of 30 sampled issues containing the exact error text), and adjusted estimate. Rule ``--'' marks categories \tool{} does not target; $\dagger$ marks a rule added after mining.}",
       r"\label{tab:categories}",
       r"\begin{tabular}{@{}llrrr@{}}", r"\toprule",
       r"Category & Rule & Hits & Precision & Adjusted \\", r"\midrule"]
for r in rows:
    rule = r["preflight_rule"] or "--"
    added = r["category"] == "volume permission denied"  # rule added after mining
    if added:
        rule = "mounts.permissions"
    rule = r"\texttt{" + esc(rule.split(",")[0]) + "}" if rule not in ("--", "(root cause)", "(learned)") else esc(rule)
    if added:
        rule += "$^\\dagger$"
    out.append(f"{esc(r['category'])} & {rule} & {int(r['raw_hits']):,} & {float(r['precision']):.2f} & {int(r['adjusted_estimate']):,} \\\\")
out += [r"\bottomrule", r"\end{tabular}", r"\end{table}"]
open(os.path.join(here, "..", "paper", "categories.tex"), "w").write("\n".join(out) + "\n")
