#!/usr/bin/env python3
"""Checks every number in a published document against a committed run.

Run it through scripts/claims-check.sh, which is what `make verify-phase-4`
calls. This file holds the logic because the work is CSV, markdown, and JSONL
parsing, and awk would be the wrong tool three times over. Standard library
only, like everything under harness/, so a clone can run it (ADR-0007).

Three checks, in order of how much they prove.

1. TABLE CELLS. A markdown table whose header carries an `arm` column and at
   least one metric column is a results table. It must also carry a `layer`
   column, which is ADR-0004 enforced rather than remembered, and every metric
   cell in it is looked up in the CSV row for that layer, arm, and scope and
   compared as a number. This is exact and positional: a cell that drifted
   from the run behind it fails here with the value it should have had.

2. PROSE NUMBERS. Every number outside a fenced block, outside backticks, and
   outside a markdown link target has to appear in the fact set or in
   scripts/claims-allow.txt with a reason on its line. The fact set is built
   from the committed artifacts and nothing else, so this catches an invented
   number. It is deliberately weaker than check 1: a number that exists
   somewhere in a run passes here even if the sentence around it is wrong.
   Check 1 is what covers the tables, and a reader is what covers the rest.

3. LABELS. RESULTS.md and README.md each have to carry a results table with a
   `live` row, under a heading that names Razorpay test mode, so nobody can
   quote a test-mode recovery rate off this repository without the label
   attached to it.

The fact set is built only from files git tracks. An untracked local run
cannot launder a number into it.
"""

import json
import os
import re
import subprocess
import sys

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

# The documents this gate is responsible for.
DEFAULT_FILES = [
    "README.md",
    "RESULTS.md",
    "ARCHITECTURE.md",
    "HONEST-LIMITATIONS.md",
    "docs/DEMO-SCRIPT.md",
]

# The two runs the published tables come from. A table's `layer` cell picks
# the CSV. Publishing a table from a different run means changing this map,
# which is the point: the mapping is a claim about provenance and it is here
# rather than implied by whichever file was newest.
PUBLISHED_CSV = {
    "fake": "results/tables/phase-3-fake.csv",
    "live": "results/tables/phase-3-live.csv",
}

# Markdown header text to CSV column. A results table header that is not in
# here fails rather than being skipped, so a new column cannot arrive
# unchecked.
COLUMNS = {
    "orders": "n_orders",
    "scorable": "n_scorable",
    "unscorable": "n_unscorable",
    "recoverable": "ground_truth_recoverable",
    "recovered": "recovered_orders",
    "recovered amount": "recovered_amount_paise",
    "rate": "recovery_rate",
    "actions": "actions_taken",
    "false actions": "false_action_count",
    "fa-1": "fa1_forbidden",
    "fa-2": "fa2_over_attempt",
    "modelled cost": "modeled_false_action_cost_paise",
    "notifications": "notifications_sent",
    "notify cost": "modeled_notification_cost_paise",
    "escalations": "escalations",
    "precision": "escalation_precision",
    "recall": "escalation_recall",
    "class acc": "classification_accuracy",
    "evaluations": "policy_evaluations",
    "refusals": "policy_refusals",
    "violations attempted": "policy_violations_attempted",
    "violations succeeded": "policy_violations_succeeded",
    "gateway calls": "api_calls",
    "claim disagreements": "claim_disagreements",
}

# Header cells that identify a row rather than measure it.
KEY_COLUMNS = {"layer", "arm", "scope"}

failures = []


def fail(where, message):
    failures.append("%s: %s" % (where, message))


def tracked(prefix):
    out = subprocess.run(
        ["git", "-C", REPO, "ls-files", prefix],
        capture_output=True,
        text=True,
        check=True,
    ).stdout
    return [line for line in out.splitlines() if line.strip()]


def norm(value):
    """One string per numeric value, so 1.0, 1, and 1.000 compare equal."""
    if abs(value - round(value)) < 1e-12:
        return str(int(round(value)))
    return ("%.10f" % value).rstrip("0").rstrip(".")


def as_number(text):
    try:
        return float(str(text).strip())
    except (TypeError, ValueError):
        return None


# A raw JSON leaf under this magnitude does not enter the fact set. A ledger
# carries sequence numbers, attempt counts, and status codes, so admitting
# every small integer it holds would make check 2 pass on almost any two-digit
# number somebody typed. Small numbers have to come from a table cell or from
# a count this file computes on purpose. Amounts in paise and timestamps are
# above the line and stay in, which is what lets a sentence cite the amount on
# a span.
SMALL = 1000


class Facts:
    """Every number that appears in a committed artifact, plus the rounded
    forms a sentence uses: a usd figure to two decimals, a millisecond wall
    clock as seconds and as minutes."""

    def __init__(self):
        self.values = set()

    def add(self, text, key=None, small_ok=True, scale_ms=False):
        value = as_number(text)
        if value is None:
            return
        if not small_ok and abs(value) < SMALL:
            return
        forms = {value, round(value), round(value, 1), round(value, 2), round(value, 3)}
        # Seconds and minutes off a millisecond column, because that is how a
        # sentence says it. Only from the CSV: a per-invocation duration_ms in
        # a ledger would put a small second count into the fact set for every
        # invocation, which is the flood SMALL exists to stop.
        if scale_ms and key and str(key).endswith("_ms"):
            for scale in (1000.0, 60000.0):
                forms |= {round(value / scale), round(value / scale, 1)}
        for form in forms:
            self.values.add(norm(form))

    def __contains__(self, value):
        return norm(value) in self.values

    def load(self):
        for path in tracked("results/"):
            full = os.path.join(REPO, path)
            if path.endswith(".csv"):
                self._csv(full)
            elif path.endswith(".jsonl"):
                self._jsonl(full)
            elif path.endswith(".json"):
                self._json(full)
        self._ledger_counts()
        self._batch_counts()

    def _csv(self, path):
        with open(path, encoding="utf-8") as handle:
            lines = [line.rstrip("\n") for line in handle if line.strip()]
        if not lines:
            return
        header = lines[0].split(",")
        for line in lines[1:]:
            for column, cell in zip(header, line.split(",")):
                self.add(cell, column, scale_ms=True)

    def _json(self, path):
        with open(path, encoding="utf-8") as handle:
            self._walk(json.load(handle))

    def _jsonl(self, path):
        with open(path, encoding="utf-8") as handle:
            for line in handle:
                if line.strip():
                    self._walk(json.loads(line))

    def _walk(self, node, key=None):
        if isinstance(node, dict):
            for name, value in node.items():
                self._walk(value, name)
        elif isinstance(node, list):
            for value in node:
                self._walk(value, key)
        elif isinstance(node, bool):
            return
        else:
            self.add(node, key, small_ok=False)

    def _ledger_counts(self):
        """Counts a sentence can cite: rows per kind, and policy evaluations
        per rule. HONEST-LIMITATIONS quotes both."""
        for path in tracked("results/"):
            if not path.endswith("ledger.jsonl"):
                continue
            kinds = {}
            rules = {}
            with open(os.path.join(REPO, path), encoding="utf-8") as handle:
                for line in handle:
                    if not line.strip():
                        continue
                    row = json.loads(line)
                    kind = row.get("kind", "")
                    kinds[kind] = kinds.get(kind, 0) + 1
                    if kind == "policy_evaluated":
                        rule = row.get("policy_rule", "")
                        rules[rule] = rules.get(rule, 0) + 1
            for count in list(kinds.values()) + list(rules.values()):
                self.add(count)

    def _batch_counts(self):
        """The shape of a seeded batch: its size, its bait count, and how many
        orders it holds per seeded class. A sentence describing a batch cites
        these and no CSV column carries all of them."""
        for path in tracked("results/batches/"):
            with open(os.path.join(REPO, path), encoding="utf-8") as handle:
                manifest = json.load(handle)
            orders = manifest.get("orders") or []
            self.add(len(orders))
            classes = {}
            bait = 0
            for order in orders:
                name = order.get("seeded_failure_class", "")
                classes[name] = classes.get(name, 0) + 1
                if order.get("bait_kind"):
                    bait += 1
            self.add(bait)
            for count in classes.values():
                self.add(count)


def load_allowlist():
    """Numbers that are settings, protocol constants, or documented history
    rather than run output. Every line carries its reason."""
    path = os.path.join(REPO, "scripts", "claims-allow.txt")
    allowed = {}
    if not os.path.exists(path):
        return allowed
    with open(path, encoding="utf-8") as handle:
        for number, line in enumerate(handle, start=1):
            text = line.strip()
            if not text or text.startswith("#"):
                continue
            parts = text.split(None, 1)
            if len(parts) < 2:
                fail("scripts/claims-allow.txt:%d" % number, "no reason on the line")
                continue
            value = as_number(parts[0])
            if value is None:
                fail("scripts/claims-allow.txt:%d" % number, "not a number: %s" % parts[0])
                continue
            allowed[norm(value)] = parts[1]
    return allowed


def load_csv_rows(path):
    rows = {}
    with open(os.path.join(REPO, path), encoding="utf-8") as handle:
        lines = [line.rstrip("\n") for line in handle if line.strip()]
    header = lines[0].split(",")
    for line in lines[1:]:
        cells = line.split(",")
        row = dict(zip(header, cells))
        rows[(row.get("layer", ""), row.get("arm", ""), row.get("scope", ""))] = row
    return rows


def clean_cell(text):
    return text.replace("**", "").replace("`", "").strip()


def split_row(line):
    inner = line.strip()
    if inner.startswith("|"):
        inner = inner[1:]
    if inner.endswith("|"):
        inner = inner[:-1]
    return [clean_cell(cell) for cell in inner.split("|")]


SEPARATOR = re.compile(r"^\s*\|[\s:|-]+\|\s*$")


def check_tables(path, lines, csv_cache):
    """Check 1 and half of check 3."""
    live_rows = 0
    index = 0
    while index < len(lines) - 1:
        line = lines[index]
        if not line.strip().startswith("|") or not SEPARATOR.match(lines[index + 1]):
            index += 1
            continue
        header = [cell.lower() for cell in split_row(line)]
        metrics = [cell for cell in header if cell in COLUMNS]
        if "arm" not in header or not metrics:
            index += 2
            continue

        where = "%s:%d" % (path, index + 1)
        unknown = [c for c in header if c not in COLUMNS and c not in KEY_COLUMNS]
        if unknown:
            fail(where, "results table has unmapped columns: %s" % ", ".join(unknown))
        if "layer" not in header:
            fail(where, "results table has an arm column and no layer column (ADR-0004)")
            index += 2
            continue

        row_index = index + 2
        while row_index < len(lines) and lines[row_index].strip().startswith("|"):
            cells = split_row(lines[row_index])
            row_where = "%s:%d" % (path, row_index + 1)
            if len(cells) != len(header):
                fail(row_where, "row has %d cells against %d headers" % (len(cells), len(header)))
                row_index += 1
                continue
            row = dict(zip(header, cells))
            layer = row["layer"].lower()
            arm = row["arm"]
            scope = row.get("scope", "overall") or "overall"
            if layer == "live":
                live_rows += 1
            csv_path = PUBLISHED_CSV.get(layer)
            if csv_path is None:
                fail(row_where, "layer %r has no published run in PUBLISHED_CSV" % layer)
                row_index += 1
                continue
            if csv_path not in csv_cache:
                csv_cache[csv_path] = load_csv_rows(csv_path)
            source = csv_cache[csv_path].get((layer, arm, scope))
            if source is None:
                fail(row_where, "no row %s/%s/%s in %s" % (layer, arm, scope, csv_path))
                row_index += 1
                continue
            for head in header:
                if head in KEY_COLUMNS:
                    continue
                column = COLUMNS.get(head)
                if column is None:
                    continue
                published = row[head]
                actual = source.get(column, "")
                if published == "n/a" or actual == "n/a":
                    if published != actual:
                        fail(row_where, "%s.%s is %r, %s says %r"
                             % (arm, head, published, csv_path, actual))
                    continue
                a, b = as_number(published), as_number(actual)
                if a is None or b is None or norm(a) != norm(b):
                    fail(row_where, "%s.%s is %r, %s says %r"
                         % (arm, head, published, csv_path, actual))
            row_index += 1
        index = row_index
    return live_rows


FENCE = re.compile(r"^\s*```")
INLINE_CODE = re.compile(r"`[^`]*`")
LINK_TARGET = re.compile(r"\]\([^)]*\)")
TIMECODE = re.compile(r"\b\d+:\d+\b")
NUMBER = re.compile(r"(?<![\w.$-])(\d+(?:\.\d+)?)(?![\w.-])")


def check_numbers(path, lines, facts, allowed):
    """Check 2."""
    in_fence = False
    for number, raw in enumerate(lines, start=1):
        if FENCE.match(raw):
            in_fence = not in_fence
            continue
        if in_fence:
            continue
        text = INLINE_CODE.sub(" ", raw)
        text = LINK_TARGET.sub("]", text)
        text = TIMECODE.sub(" ", text)
        for token in NUMBER.findall(text):
            value = as_number(token)
            if value is None:
                continue
            key = norm(value)
            if key in facts.values or key in allowed:
                continue
            fail("%s:%d" % (path, number),
                 "%s is in no committed run and no allowlist line" % token)


TEST_MODE = re.compile(r"(?i)test\s*mode")


def check_labels(path, lines, live_rows):
    """The other half of check 3."""
    if os.path.basename(path) not in ("RESULTS.md", "README.md"):
        return
    if live_rows == 0:
        fail(path, "no results table carries a live row")
        return
    headings = [line for line in lines if line.startswith("#") and TEST_MODE.search(line)]
    if not headings:
        fail(path, "no heading names Razorpay test mode above the live table")


def main(argv):
    files = argv[1:] or DEFAULT_FILES
    facts = Facts()
    facts.load()
    if len(facts.values) < 100:
        fail("results/", "the fact set has %d entries, which is too few to be a run"
             % len(facts.values))
    allowed = load_allowlist()
    csv_cache = {}

    for path in files:
        full = os.path.join(REPO, path)
        if not os.path.exists(full):
            fail(path, "not found")
            continue
        with open(full, encoding="utf-8") as handle:
            lines = [line.rstrip("\n") for line in handle]
        live_rows = check_tables(path, lines, csv_cache)
        check_numbers(path, lines, facts, allowed)
        check_labels(path, lines, live_rows)

    if failures:
        for line in failures:
            sys.stderr.write("claims-check: %s\n" % line)
        sys.stderr.write("claims-check: %d problem(s) across %d file(s)\n"
                         % (len(failures), len(files)))
        return 1
    print("claims-check: %d file(s) clean against %d facts from the committed runs"
          % (len(files), len(facts.values)))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
