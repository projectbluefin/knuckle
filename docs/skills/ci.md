---
name: ci
description: "CI gates, build toolchain, and code quality checks for projectbluefin/knuckle. Use when running or debugging just ci, adding new checks, or working with Go tooling."
metadata:
  type: procedure
  context7-sources:
    - /golangci/golangci-lint
    - /golang/go
    - /google/go-licenses
---

# CI — knuckle

> Authoritative reference: [`CI-AND-TESTING.md`](../CI-AND-TESTING.md)
> This file is the operational skill; the reference doc has the full test pyramid and all threshold history.

## `just ci` — The Gate

```bash
just ci   # tidy → fmt → vet → lint → vuln → test-race → cover-check → headless-test → shell-lint → build
```

Every step must be green before pushing. No `--no-verify`.

### Individual steps

```bash
just tidy          # go mod tidy
just fmt-check     # gofmt -l (must be empty)
just vet           # go vet ./...
just lint          # golangci-lint run ./...
just vuln          # govulncheck ./...
just test          # go test -race ./...
just cover-check   # per-package coverage gate
just headless-test # config-gen e2e (runs on host, no VM)
just shell-lint    # shellcheck scripts/
just build         # GOOS=linux GOARCH=amd64 CGO_ENABLED=0 → bin/knuckle
```

## Coverage Gates

`just cover-check` enforces per-package thresholds. Current gates (authoritative: `Justfile cover-check`):

| Package | Gate |
|---|---|
| `internal/model` | 100% |
| `internal/iso` | 100% |
| `internal/runner` | 100% |
| `internal/demo` | 100% |
| `internal/validate` | 99% |
| `internal/probe` | 100% |
| `internal/install` | 100% |
| `internal/ignition` | 100% |
| `internal/bakery` | 100% |
| `internal/github` | 96% |
| `internal/headless` | 99% |
| `internal/wizard` | 99% |
| `internal/tui` | 99% |
| `scripts/catalog_check` | 100% |
| `cmd/knuckle` | 85% |
| `cmd/compile-butane-fresh` | 100% |
| `cmd/nvidia-check` | 95% |

To add a package to cover-check, add a line to the `cover-check` recipe in `Justfile`.

## Tool Version Bumps

Always update **both** locations together:
- `GOLANGCI_LINT_VERSION` and SHA256 checksums in `Justfile`
- `golangci-lint-version` in `.github/workflows/ci.yml`

```bash
just tools && just ci   # verify after bump
```

## Go Toolchain Bumps

Update all **three** locations together:
- `go.mod`: `go X.Y.Z` line AND `toolchain goX.Y.Z` line
- `.github/workflows/ci.yml`: `go-version: "X.Y.Z"` (two occurrences)

`govulncheck` gates on stdlib CVEs — a stale Go version fails even with no app-level vulns.

## ISO Smoke Test

```bash
just iso stable   # build → output/knuckle-installer-stable-amd64.iso
just iso-smoke output/knuckle-installer-stable-amd64.iso /usr/share/OVMF/OVMF_CODE_4M.fd 120
```

**Serial log invariants (`scripts/iso-smoke.sh`):**
- `systemd.gpt_auto=0` must appear on BOTH BLS entries (primary + serial)
- `initrd-root-device.target`, `initrd-usr-fs.target`, `getty.target` must appear
- `x2dauto` / `xd2root.device` / `dracut.*skip` must NOT appear

> ⚠️ **Flatcar 4230.x regression (issue #737, 2026-06-02):** `systemd-journald`
> starts at ~2.9s kernel time and stops serial logging before
> `initrd-usr-fs.target`. `iso-smoke` will report `FAILED: missing
> initrd-usr-fs.target` even though the boot is healthy. This is a
> **non-required** check; do not hold PRs for it.

## Headless Test

`just headless-test` runs config-gen e2e on the host — no VM needed. Uses canned JSON
inputs from `testdata/headless/`. Verifies the full config generation path including
Butane→Ignition compilation.

## First-Time Contributor CI Gate

New contributors' workflow runs are held pending approval. Check before QA batch:

```bash
gh api repos/projectbluefin/knuckle/actions/runs?status=action_required \
  --jq '.workflow_runs[] | "\(.id) \(.name) \(.head_branch)"'
# For each blocked run:
gh api repos/projectbluefin/knuckle/actions/runs/<ID>/approve --method POST
```

## cmd/knuckle TTY Tests

`TestMain_TUINormalMode` and friends fail with `open /dev/tty: no such device or address`
in non-interactive environments (SSH -f, nohup, ghost QA worktrees). This is a pre-existing
infra limitation tracked as issue #512. For Tier 0 PRs, GitHub Actions CI is authoritative.

## Common CI Failures

| Failure | Cause | Fix |
|---|---|---|
| SA5011 lint: nil deref after `t.Fatal` | golangci-lint-action@v9 catches this; local `run` may not | Add `return` immediately after every `t.Fatal(...)` nil-check guard |
| `go: updating go.mod: existing contents have changed` | Parallel QA runs race on go.mod | Run QA **sequentially** — never in parallel |
| `govulncheck` fails with no app vulns | Stale Go toolchain, stdlib CVE | Bump Go toolchain (all three locations) |
| `cover-check` below threshold | New code without tests | Write tests; never lower thresholds |
| BATS tests fail on main | Test-first PR merged before implementation | Run `bats scripts/tests/qa-test-pr.bats` locally before pushing |

## Lessons Learned

### SA5011: always `return` after `t.Fatal` (2026-05-26)

`golangci-lint-action@v9` in CI catches SA5011 (nil-deref after `t.Fatal`) even when
`golangci-lint run ./...` locally reports clean. Pattern:

```go
if result == nil {
    t.Fatal("expected non-nil result")
    return  // REQUIRED — prevents SA5011 even though t.Fatal stops the test
}
```

Missing `return` blocks the entire merge queue for all open PRs.

### BATS / script alignment (2026-05-24)

When modifying `scripts/qa-test-pr.sh`, the BATS suite greps the mock git log for literal strings. Three hard rules:
1. `remove_worktree_path()` must unconditionally call `git worktree remove --force` when the path exists on disk
2. `--force` before path: `git worktree remove --force "$path"` (not after)
3. Use `git branch -D "$ref"` directly — not `git update-ref -d || git branch -D`
