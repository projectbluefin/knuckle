# Org test-quality templates

Reusable, copy-paste test-quality patterns **extracted from
[`projectbluefin/knuckle`](https://github.com/projectbluefin/knuckle)**, which
runs a per-package coverage gate, Codecov, and 5x flaky-test detection in CI.

This exists because of [issue #746](https://github.com/projectbluefin/knuckle/issues/746):
other org repos (bootc-installer, testsuite, …) have lower coverage floors and
can adopt knuckle's patterns without re-deriving them. Each artifact below is
self-contained — copy it into your repo and adjust the numbers.

## Artifacts

| Artifact | What it is | Copy to your repo as |
| -------- | ---------- | -------------------- |
| [`coverage-gate/`](coverage-gate/) | Per-package Go coverage gate (script + config) | `scripts/coverage-gate.sh` + `coverage-gate.toml` |
| [`codecov.yml`](codecov.yml) | Standardized Codecov config (project ≥70%, patch ≥70%) | `codecov.yml` |
| [`actions/flaky-test-detect`](actions/flaky-test-detect/) | Composite action: run suite 5x, fail on any flake | `.github/actions/flaky-test-detect/action.yml` |

## 1. Per-package coverage gate

`coverage-gate/coverage-gate.sh` is a dependency-free (just `bash` + `go`) port
of knuckle's `just cover-check`. It reads a `package:threshold` config, runs
`go test -cover` per package, and exits non-zero if any package is below its
gate or reports no coverage.

```bash
chmod +x coverage-gate.sh
./coverage-gate.sh --config coverage-gate.toml   # or: ./coverage-gate.sh
```

Start from `coverage-gate.example.toml` (knuckle's own thresholds) and lower the
gates to your repo's current coverage — set them **below** your real numbers so
CI fails on regression, not on aspirational drift. Run `go test -cover ./...`
to see current numbers.

CI usage:

```yaml
- uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7
- uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7
  with:
    go-version: "1.26"
- run: ./scripts/coverage-gate.sh --config coverage-gate.toml
```

> **Ponytail note:** this intentionally re-implements ~60 lines of shell rather
> than depending on knuckle's Justfile recipe, because other repos aren't Go
> monorepos with `just`. If your repo *is* knuckle-like, you can skip the script
> and call `just cover-check` directly.

## 2. Codecov config

`codecov.yml` is knuckle's Codecov config with thresholds relaxed to the org
floor (project ≥70%, patch ≥70%) and both status checks marked
non-informational (so they block/flag on miss). Knuckle's stricter 95%/85%
values are fine for knuckle itself but unrealistic for repos starting lower.

See the header comments in the file for the full schema explanation.

## 3. Flaky test detection

`actions/flaky-test-detect/action.yml` is a composite action wrapping knuckle's
"run the suite 5x, fail CI if any run fails" step (from `ci.yml::build-test`).
It's parameterized on run count and test command so it works for `go`, `pytest`,
etc.

```yaml
- name: Detect flaky tests
  uses: ./.github/actions/flaky-test-detect
  with:
    run_count: 5
    test_cmd: "go test -count=1 ./..."
```

Set `fail_on_flake: false` to run it report-only while you build confidence.

## Adopting across the org

1. Copy the artifact(s) you want into your repo.
2. Run the gate locally to capture current numbers, then set gates below them.
3. Wire the gate + Codecov + flaky-detect into a CI job (pin every action by
   version, set `contents: read`, `persist-credentials: false`).
4. Raise gates over time as coverage improves.
