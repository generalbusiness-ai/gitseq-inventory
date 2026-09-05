#!/usr/bin/env python3
"""Run the application delivery gates at one clean head; keep evidence and the old oracle outside this repo."""

import hashlib
import io
import json
import os
from pathlib import Path
import subprocess
import tarfile
import tempfile

ROOT = Path(__file__).resolve().parents[1]
MODULE = "github.com/generalbusiness-ai/tailapps/jsonataddl"
VERSION = "v0.1.2"
UPSTREAM = "9280be8b9b1d610c41bc6461188fe7ecbb70bf64"
OLD = "79e8400888a00a38ccdf96722eebcba2491a9780"
SUM = "h1:yWVB9oLiPV5pPt3zoPuxUe6nNKx53WIqFWswYOkgOYM="
MODSUM = "h1:fpGrE/1ODSULhyxThhr3TL4JtpfcX3PdA4kJBnKWJRY="
PINS = {
    MODULE: VERSION,
    "github.com/jsonata-go/jsonata": "v0.0.0-20250709164031-599f35f32e5f",
    "github.com/ncruces/go-sqlite3": "v0.35.3",
}
EVIDENCE = Path(tempfile.mkdtemp(prefix="inventory-corpora-"))
ENV = dict(os.environ, GOTOOLCHAIN="go1.26.7", GOWORK="off", GOFLAGS="",
           GOPRIVATE="", GONOPROXY="", GONOSUMDB="", GOINSECURE="",
           GOPROXY="https://proxy.golang.org", GOSUMDB="sum.golang.org",
           INVENTORY_CORPUS_ORACLE="")


def run(label, *args, cwd=ROOT, env=ENV, failure=False):
    result = subprocess.run(args, cwd=cwd, env=env, capture_output=True, text=True)
    (EVIDENCE / (label + ".stdout")).write_text(result.stdout)
    (EVIDENCE / (label + ".stderr")).write_text(result.stderr)
    if (result.returncode != 0) != failure:
        raise RuntimeError(f"{label}: exit {result.returncode}\n{result.stderr[-3000:]}\n{result.stdout[-3000:]}")
    return result.stdout


def passed(output, required):
    events = [json.loads(line) for line in output.splitlines() if line.startswith("{")]
    successes = {e.get("Test") for e in events if e.get("Action") == "pass"}
    if any(e.get("Action") in ("fail", "skip") for e in events) or not required <= successes:
        raise RuntimeError("missing passing tests: " + str(sorted(required - successes)))
    return sorted(required)


def hashes(directory):
    return {str(p.relative_to(directory)): hashlib.sha256(p.read_bytes()).hexdigest()
            for p in sorted(directory.rglob("*")) if p.is_file()}


def main():
    print("Corpus evidence:", EVIDENCE, flush=True)
    head = run("candidate", "git", "rev-parse", "HEAD").strip()
    if run("clean-before", "git", "status", "--porcelain").strip():
        raise RuntimeError("corpus delivery gate requires a clean committed candidate")
    if json.loads(run("module-file", "go", "mod", "edit", "-json")).get("Replace"):
        raise RuntimeError("module replacements are forbidden")
    for name, version in PINS.items():
        pin = json.loads(run("pin-" + name.rsplit("/", 1)[-1], "go", "list", "-m", "-json", name))
        if pin["Version"] != version or pin.get("Replace"):
            raise RuntimeError("dependency pin drift: " + name)
    listing = run("all-modules", "go", "list", "-m", "-f", "{{.Path}}{{if .Replace}} REPLACED{{end}}", "all")
    if "REPLACED" in listing or "github.com/generalbusiness-ai/tailapps" in listing.splitlines():
        raise RuntimeError("replacement or Tailapps root dependency found")
    dependencies = run("package-dependencies", "go", "list", "-deps", "-test", "./...")
    if any(p.startswith("github.com/generalbusiness-ai/gitseq/spike/") for p in dependencies.splitlines()):
        raise RuntimeError("active or test dependency still imports the Gitseq spike")
    download = json.loads(run("download", "go", "mod", "download", "-json", MODULE + "@" + VERSION))
    if download["Sum"] != SUM or download["GoModSum"] != MODSUM:
        raise RuntimeError("immutable module checksum mismatch")
    # Proxy downloads need not include Origin. When present it must agree.
    origin = download.get("Origin")
    if origin and (origin.get("Hash") != UPSTREAM or origin.get("Ref") != "refs/tags/jsonataddl/" + VERSION):
        raise RuntimeError("unexpected upstream source identity")
    source = Path(download["Dir"])
    required_a = {"TestConformanceCorpus", "TestConformanceCorpusProjectionCases"}
    manifests = sorted((source / "corpus/v1").glob("*/manifest.json"))
    if not manifests:
        raise RuntimeError("released module contains no corpus manifests")
    for path in manifests:
        data = json.loads(path.read_text())
        required_a.add("TestConformanceCorpus/" + path.parent.name)
        for test in data.get("evaluations", []):
            required_a.add("TestConformanceCorpus/" + path.parent.name + "/" + test["name"])
        for test in data.get("projection", []):
            required_a.add("TestConformanceCorpusProjectionCases/" + path.parent.name + "/" + test["name"])
    before = hashes(source / "corpus")

    # Build the reviewed executable in its own immutable source/module graph.
    # No candidate package, retained test import or replace points to the spike.
    old_source = EVIDENCE / "old-source"
    run("oracle-init", "git", "init", "-q", str(old_source))
    run("oracle-fetch", "git", "fetch", "--depth=1", "https://github.com/generalbusiness-ai/gitseq.git", OLD, cwd=old_source)
    run("oracle-checkout", "git", "checkout", "--detach", OLD, cwd=old_source)
    if run("oracle-head", "git", "rev-parse", "HEAD", cwd=old_source).strip() != OLD:
        raise RuntimeError("wrong old oracle commit")
    oracle = EVIDENCE / "old-inventory"
    run("oracle-build", "go", "build", "-mod=readonly", "-o", str(oracle), "./spike/cmd/jsonata-inventory", cwd=old_source)
    run("oracle-modules", "go", "mod", "verify", cwd=old_source)
    if run("oracle-clean", "git", "status", "--porcelain", cwd=old_source).strip():
        raise RuntimeError("old oracle source/module changed")
    oracle_env = dict(ENV, INVENTORY_CORPUS_ORACLE=str(oracle))
    required_local = {"TestCorpusB", "TestCorpusC", "TestCorpusInputFreeze", "TestPinnedIdentity", "TestEveryComponentChangesIdentity", "TestTypedValuesCrossStorageReadAndQuery", "TestLegacyJSONAffinityRefusesContinueAndAutomaticReset"}
    cases = json.loads((ROOT / "internal/recordruntime/testdata/compatibility.json").read_text())["cases"]
    required_local.update("TestCorpusB/" + c["name"].replace(" ", "_") for c in cases)
    required_local.update("TestCorpusC/" + name for name in ("decisions", "facts", "stock", "reservations"))
    required_local.update("TestCorpusInputFreeze/" + name for name in ("empty", "ordered", "bound", "first-duplicate"))
    results = []
    for iteration in (1, 2):
        a = run(f"A-{iteration}", "go", "test", "-mod=readonly", "-count=1", "-json", "-run", "^TestConformanceCorpus(ProjectionCases)?$", MODULE)
        local = run(f"BC-input-{iteration}", "go", "test", "-mod=readonly", "-count=1", "-json", "-run", "^(TestCorpus.*|TestPinnedIdentity|TestEveryComponentChangesIdentity|TestTypedValuesCrossStorageReadAndQuery|TestLegacyJSONAffinityRefusesContinueAndAutomaticReset)$", "./internal/recordruntime", env=oracle_env)
        application = run(f"application-{iteration}", "go", "test", "-mod=readonly", "-count=1", "-json", "./")
        required_application = {"TestPublicFixtureBoundary", "TestBindingAndProjectionIdentity",
                                "TestInventoryReplaysIntoEquivalentBoundedProjections", "TestApplicationQuerySurfaceRemainsReadOnly"}
        results.append({"run": iteration, "A": passed(a, required_a), "BC-input": passed(local, required_local),
                        "application": passed(application, required_application)})
        if local.count("live old oracle and candidate match sealed rows") != 4:
            raise RuntimeError("live oracle did not run every comparison")
        print("Corpora A/B/C and input freeze passed, repetition", iteration, flush=True)
    if before != hashes(source / "corpus"):
        raise RuntimeError("released corpus was modified")

    # Mutate a disposable copy of the exact candidate, never the reviewed tree.
    mutant = EVIDENCE / "wrong-upsert"
    mutant.mkdir()
    archive = subprocess.check_output(["git", "archive", head], cwd=ROOT)
    with tarfile.open(fileobj=io.BytesIO(archive)) as tar:
        tar.extractall(mutant, filter="data")
    fold = mutant / "folds/inventory.jsonata"
    original = fold.read_text()
    needle = '"available": $available + event.qty'
    if original.count(needle) != 1:
        raise RuntimeError("upsert mutation target changed")
    fold.write_text(original.replace(needle, needle + " - 1"))
    output = run("C-wrong-upsert", "go", "test", "-mod=readonly", "-count=1", "-json", "-run", "^TestCorpusC$", "./internal/recordruntime", cwd=mutant, env=oracle_env, failure=True)
    events = [json.loads(line) for line in output.splitlines() if line.startswith("{")]
    if not any(e.get("Action") == "fail" and e.get("Test") == "TestCorpusC/stock" for e in events):
        raise RuntimeError("wrong upsert did not fail the stock comparison")
    run("module-verify", "go", "mod", "verify")
    if run("clean-after", "git", "status", "--porcelain").strip() or run("head-after", "git", "rev-parse", "HEAD").strip() != head:
        raise RuntimeError("candidate changed during the gate")
    summary = {"candidate": head, "module": MODULE, "version": VERSION, "upstream": UPSTREAM,
               "sum": SUM, "go_mod_sum": MODSUM, "pins": PINS, "old_oracle": OLD,
               "corpus_hashes": before, "fixture_hashes": hashes(ROOT / "internal/recordruntime/testdata"),
               "runs": results, "wrong_upsert": "TestCorpusC/stock failed as required",
               "exclusions": ["runtime and source identity", "program names"]}
    (EVIDENCE / "summary.json").write_text(json.dumps(summary, indent=2) + "\n")
    print("PASS: all corpora twice and wrong-upsert proof at", head, flush=True)


if __name__ == "__main__":
    main()
