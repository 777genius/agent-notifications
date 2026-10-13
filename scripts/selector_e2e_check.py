#!/usr/bin/env python3
"""Reject missing, skipped, or failed required bootstrap acceptance cases."""
import json
from pathlib import Path
import sys

REQUIRED = {
    "TestSetupProductsScopedPresence",
    "TestBootstrapSelectorPTY",
    "TestBootstrapSelectionDoesNotApproveConsent",
    "TestBootstrapInteractiveChannelConsent",
    "TestBootstrapStrictResultBytes",
    "TestBootstrapStaleSelectedFacts",
    "TestBootstrapPartialProgress",
    "TestBootstrapNormalizedRouteConsent",
    "TestBootstrapPendingJSON",
    "TestBootstrapStaleSelectedFacts/last-binding-or-keep-off-before-first-effect",
    "TestBootstrapStaleSelectedFacts/own-hooks-generation-then-portable-admission",
    "TestBootstrapStaleSelectedFacts/unchanged-projection-own-generation-success",
    "TestBootstrapStaleSelectedFacts/post-admission-existing-CAS",
    "TestBootstrapPartialProgress/Claude_completed_Codex_committed_followup_failure_Gemini_remainder",
    "TestBootstrapNormalizedRouteConsent/false-false-and-preserved-disabled-on-disk",
}


def check(path):
    passed, invalid = set(), set()
    for line in Path(path).read_text().splitlines():
        event = json.loads(line)
        name = event.get("Test", "")
        action = event.get("Action")
        if name.startswith(("TestBootstrap", "TestSetupProductsScopedPresence")):
            if action == "pass":
                passed.add(name)
            elif action in ("skip", "fail"):
                invalid.add(name)
    missing = REQUIRED - passed
    if missing or invalid:
        raise SystemExit("Bootstrap E2E qualification failed: " + json.dumps({
            "missing": sorted(missing), "skipped_or_failed": sorted(invalid),
        }))
    print(f"Bootstrap E2E qualified: {len(passed)} passing cases, no skips")


if __name__ == "__main__":
    check(sys.argv[1])
