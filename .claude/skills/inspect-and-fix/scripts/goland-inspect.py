#!/usr/bin/env python3
"""Run GoLand's full inspection engine headlessly and print a triaged report — the
same engine behind the IDE's editor squiggles, which an agent can't otherwise see.
The inspect-and-fix skill's runner.

    python3 .../goland-inspect.py            # run the inspection, print the report
    python3 .../goland-inspect.py RESULTSDIR # just re-print a report from a results dir

Output is ephemeral: the run works in a throwaway temp dir removed on exit (Ctrl-C and
SIGTERM included), so every run is a brand-new index and nothing is persisted.

Overrides: GOLAND_HOME (path to GoLand.app if auto-detect fails), INSPECT_PROFILE
(inspection profile XML; default: the project's .idea profile).
"""
import sys
import os
import glob
import re
import signal
import subprocess
import tempfile
from collections import Counter
import xml.etree.ElementTree as ET

PROG = "goland-inspect"

# --- report parsing (works on any GoLand headless-inspection results dir) -------

# Per-inspection hints: ids whose fix OFTEN needs a user CALL (docs, markdown fences,
# networked CI-YAML schemas) rather than a clean edit. A HINT, not a skip — verify each.
NOISE_INSPECTIONS = {
    "Annotator": "GoLand analyzing a Markdown/code fence as live code",
    "HtmlUnknownTag": "HTML in docs / IDE log self-inspection",
    "HtmlUnknownAttribute": "HTML in docs / IDE log self-inspection",
    "LossyEncoding": "non-ASCII in log/index files (e.g. the scan reading its own output)",
    "JsonStandardCompliance": "a generated/log JSON file",
    "UndefinedAction": "GoLand's networked GitHub-Actions resolver (often a rate-limited 404), not a real undefined action",
    "UndefinedParamsPresent": "GoLand's stale GitHub-Actions input schema",
    "MarkdownIncorrectTableFormatting": "cosmetic; renders fine on GitHub",
    "MarkdownUnresolvedFileReference": "directory link GoLand can't resolve",
    "ShellCheck": "often the idiomatic `A && B || die` guard (die exits)",
    "DuplicatedCode": "dedup is a design call; usually parallel test tables / config builders",
}
# Path patterns where a finding is usually inherent (not editable Go you'd fix).
NOISE_PATHS = (re.compile(r"_test\.go$"), re.compile(r"\.md$"),
               re.compile(r"^\.github/"), re.compile(r"^docs/"))

# Severity order GoLand emits, worst first; unknowns sort last.
SEV_RANK = {"ERROR": 0, "WARNING": 1, "SERVER PROBLEM": 1, "WEAK WARNING": 2,
            "INFORMATION": 3, "INFO": 3, "TYPO": 4, "GRAMMAR_ERROR": 4}

# Redact bare IPv4: GoLand bakes the caller's IP into some messages ("rate limit
# exceeded for 1.2.3.4") and this report can be pasted elsewhere.
IP_RE = re.compile(r"\b\d{1,3}(?:\.\d{1,3}){3}\b")


def sev_rank(s):
    return SEV_RANK.get(s.upper(), 5)


def oneline(s):
    """One trimmed line: drop GoLand's `#loc` marker, redact bare IPs, collapse space."""
    return " ".join(IP_RE.sub("<ip>", s.replace("#loc", " ")).split())


def parse_file(path):
    """A list of problem dicts, or None if the XML won't parse (a killed/partial run —
    surfaced as a skip rather than silently mis-read). ElementTree unescapes entities."""
    try:
        root = ET.parse(path).getroot()
    except ET.ParseError:
        return None
    if root.tag != "problems":
        return []          # e.g. a *_aggregate <root> file — no <problem>s to read
    rows = []
    for p in root.iter("problem"):
        pc = p.find("problem_class")
        rows.append({
            "file": (p.findtext("file") or "?").replace("file://$PROJECT_DIR$/", ""),
            "line": p.findtext("line") or "?",
            "sev": (pc.get("severity") if pc is not None else None) or "?",
            "desc": oneline(p.findtext("description") or ""),
            "token": oneline(p.findtext("highlighted_element") or ""),
        })
    return rows


def report(results):
    """Parse every per-inspection XML in `results` and print the triaged report."""
    groups = []      # (name, rows, best_sev_rank, noise_hint)
    malformed = []
    for x in sorted(glob.glob(os.path.join(results, "*.xml"))):
        name = os.path.basename(x)[:-4]
        if name == ".descriptions" or name.endswith("_aggregate"):
            continue
        rows = parse_file(x)
        if rows is None:
            malformed.append(name)
        elif rows:
            best = min(sev_rank(r["sev"]) for r in rows)
            groups.append((name, rows, best, NOISE_INSPECTIONS.get(name)))

    # Worst-severity inspections first, then alphabetical — so ERRORs lead the report.
    groups.sort(key=lambda g: (g[2], g[0]))

    total = sig = 0
    sigs = []
    for name, rows, _, noise_hint in groups:
        hint = f"  [likely noise: {noise_hint}]" if noise_hint else ""
        print(f"\n### {name} ({len(rows)}){hint}")
        for r in sorted(rows, key=lambda r: sev_rank(r["sev"])):
            total += 1
            inherent = bool(noise_hint) or any(p.search(r["file"]) for p in NOISE_PATHS)
            if not inherent:
                sig += 1
                sigs.append(f'{name}: {r["file"]}:{r["line"]}  {r["desc"][:80]}')
            tok = f'  «{r["token"][:40]}»' if r["token"] else ""
            tag = "decide?" if inherent else "fix?"
            print(f'  [{tag}] {r["sev"]:<13} {r["file"]}:{r["line"]}{tok}  {r["desc"][:90]}')

    print(f"\n=== {total} problems; {sig} look like clean direct fixes; the rest need a")
    print("    per-finding CALL (surface the tradeoff to the user) — NOT auto-noise.")
    print("    Verify every one firsthand; the hints are focus, not verdicts. ===")
    sev_counts = Counter(r["sev"] for _, rows, _, _ in groups for r in rows)
    if sev_counts:
        print("by severity: " + ", ".join(
            f"{k}={v}" for k, v in sorted(sev_counts.items(), key=lambda kv: sev_rank(kv[0]))))
    if malformed:
        print("UNPARSEABLE XML (skipped — investigate, do not assume clean): " + ", ".join(malformed))
    if sigs:
        print("\nLikely clean-fix candidates (read each before editing):")
        for s in sigs:
            print("  - " + s)


# --- GoLand discovery + orchestration -------------------------------------------

def find_inspect():
    """Locate GoLand's headless inspect.sh (GOLAND_HOME overrides auto-detect)."""
    cands = [os.environ["GOLAND_HOME"]] if os.environ.get("GOLAND_HOME") else []
    cands += glob.glob("/Applications/GoLand*.app")
    cands += glob.glob(os.path.expanduser("~/Applications/GoLand*.app"))
    tb = os.path.expanduser("~/Library/Application Support/JetBrains/Toolbox/apps")
    cands += glob.glob(tb + "/[gG]oland*/*/*GoLand*.app")  # Toolbox channel dirs vary
    for c in cands:
        for p in ("Contents/bin/inspect.sh", "bin/inspect.sh", "inspect.sh"):
            if os.path.isfile(os.path.join(c, p)):
                return os.path.join(c, p)
    return None


def git_root(start):
    try:
        out = subprocess.run(["git", "-C", start, "rev-parse", "--show-toplevel"],
                             capture_output=True, text=True, check=True)
        return out.stdout.strip()
    except (subprocess.CalledProcessError, FileNotFoundError):
        return None


def run_inspection():
    repo = git_root(os.path.dirname(os.path.abspath(__file__)))
    if not repo:
        sys.exit(f"{PROG}: not inside a git repository (could not resolve repo root)")
    profile = os.environ.get("INSPECT_PROFILE") or os.path.join(
        repo, ".idea/inspectionProfiles/Project_Default.xml")

    # Preflight everything cheap before the multi-minute index, never after.
    inspect_sh = find_inspect()
    if not inspect_sh:
        sys.exit(f"{PROG}: GoLand not found. Install GoLand, or set GOLAND_HOME=/path/to/GoLand.app")
    if not os.path.isfile(profile):
        sys.exit(f"{PROG}: inspection profile not found: {profile}\n"
                 "  Open the project in GoLand once (creates .idea), or set INSPECT_PROFILE=<profile.xml>.")

    # TemporaryDirectory cleans up on exit/exceptions/Ctrl-C; route SIGTERM through it too.
    signal.signal(signal.SIGTERM, lambda *_: sys.exit(143))

    with tempfile.TemporaryDirectory(prefix="goland-inspect.") as work:
        results = os.path.join(work, "results")
        for sub in ("system", "log", "plugins", "results"):
            os.makedirs(os.path.join(work, sub))
        props = os.path.join(work, "idea.properties")
        with open(props, "w", encoding="utf-8") as f:
            f.write(f"idea.system.path={work}/system\nidea.log.path={work}/log\n"
                    f"idea.plugins.path={work}/plugins\n")
        # Isolate from any open GoLand GUI (the single-instance lock) via the private
        # system/log/plugins; the config dir stays default so the IDE license applies.
        env = dict(os.environ, GOLAND_PROPERTIES=props)

        print(f"{PROG}\n  goland : {inspect_sh}\n  project: {repo}\n  profile: {profile}\n"
              "  indexing and running the full profile (minutes; GoLand's startup chatter\n"
              "  is captured to a log, so only the report prints below)…")

        # Capture GoLand's startup spew (e.g. a locked state DB when the GUI is open) to a
        # log so the console shows just the report. subprocess gives a real timeout.
        logpath = os.path.join(work, "inspect.log")
        with open(logpath, "wb") as log:
            try:
                subprocess.run([inspect_sh, repo, profile, results, "-v2"],
                               env=env, stdout=log, stderr=subprocess.STDOUT,
                               timeout=3600, check=False)
            except subprocess.TimeoutExpired:
                sys.exit(f"{PROG}: inspection timed out after 3600s.")

        # A completed inspection always writes .descriptions.xml; its absence means the run
        # died, so bail loudly rather than let report() print a false "0 problems".
        if not os.path.isfile(os.path.join(results, ".descriptions.xml")):
            with open(logpath, encoding="utf-8", errors="replace") as lf:
                tail = "".join(lf.readlines()[-40:])
            sys.exit(f"{PROG}: inspection did not complete — no results written.\n"
                     "  tail of GoLand's log:\n" + tail)

        print()
        report(results)


def main():
    try:
        if len(sys.argv) > 1:
            if not os.path.isdir(sys.argv[1]):
                sys.exit(f"{PROG}: not a directory: {sys.argv[1]}")
            report(sys.argv[1])
        else:
            run_inspection()
    except KeyboardInterrupt:
        sys.exit(130)


if __name__ == "__main__":
    main()
