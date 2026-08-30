# CI & Testing — knuckle

> The contract between contributors, CI, and the principal-engineer review.
> If something here disagrees with reality, **reality wins** and this doc gets
> fixed.

## Test Pyramid

```
                ┌────────────────────────────────────────┐
                │   VM e2e  (automated, 4-pass)           │   just vm-e2e
                └──────────────────┬─────────────────────┘
                 ┌─────────────────┴──────────────────────┐
                 │  Headless e2e  (config generation)     │   just headless-test
                 └────────────────┬───────────────────────┘
              ┌─────────────────── ┴───────────────────────┐
              │  Integration  (network, off-CI)             │  go test -tags=integration
              └──────────────────┬──────────────────────────┘
              ┌──────────────────┴───────────────────────────┐
              │  Golden  (internal/ignition)                  │  go test ... -update
              └──────────────────┬────────────────────────────┘
        ┌─────────────────────── ┴─────────────────────────────┐
        │  Unit  (internal/**)                                  │  go test -race ./...
        └───────────────────────────────────────────────────────┘
```

| Layer          | Runs in CI? | What it covers                                                          |
| -------------- | :---------: | ----------------------------------------------------------------------- |
| Unit           |     ✅      | All packages, race-clean                                                |
| Golden         |     ✅      | Ignition output stability (regenerate with `-update`)                   |
| Headless e2e   |     ✅      | `just headless-test` — build + canned JSON, validates config generation |
| ISO smoke      |     ⚠️      | `just iso-smoke <iso> <ovmf>` — headless serial-log ISO boot assertions |
| Integration    |   ✅ nightly | Tagged `//go:build integration`. Real HTTP to GitHub + Flatcar         |
| VM e2e         |  ✅ on-demand | `just vm-e2e` locally or `vm-e2e.yml` GHA workflow (4-pass)           |

## Coverage Gate

`just cover-check` enforces per-package thresholds. Current numbers as of
2026-05-28:

| Package                      | Now    | Gate | Aspiration (TEST-PLAN.md) |
| ---------------------------- | ------ | ---- | ------------------------- |
| `internal/model`             |  100%  | 100% | ≥ 90%                     |
| `internal/iso`               |  100%  | 100% | (n/a)                     |
| `internal/runner`            |  100%  | 100% | ≥ 80%                     |
| `internal/demo`              |  100%  | 100% | (n/a)                     |
| `internal/validate`          |  99.5% |  99% | ≥ 95%                     |
| `internal/probe`             |  100%  | 100% | ≥ 85%                     |
| `internal/install`           |  100%  | 100% | ≥ 80%                     |
| `internal/ignition`          |  100%  | 100% | ≥ 90%                     |
| `internal/bakery`            |  100%  | 100% | ≥ 85%                     |
| `scripts/catalog_check`      |  100%  | 100% | (n/a)                     |
| `internal/wizard`            |  99.5% |  99% | ≥ 85%                     |
| `internal/headless`          |  99%   |  99% | (n/a)                     |
| `internal/tui`               |  98.7% |  99% | ≥ 85%                     |
| `internal/github`            |  97%   |  96% | (n/a)                     |
| `cmd/knuckle`                |  ~85%  |  85% | (n/a)                     |
| `cmd/compile-butane-fresh`   |  100%  | 100% | (n/a)                     |
| `cmd/nvidia-check`           |  98.9% |  95% | (n/a)                     |
| `cmd/nvidia-check`           |  98.9% |  95% | (n/a) — depends on #760   |

Gates are set conservatively below current numbers so CI fails on
**regression**, not on aspirational drift. When a package's actual coverage
rises and stays there, raise the gate in `Justfile :: cover-check`.

## CI Workflows

### `.github/workflows/ci.yml`

| Job                 | What it does                                                                  | Required to merge |
| ------------------- | ----------------------------------------------------------------------------- | :---------------: |
| `build-test`        | `go mod tidy` (clean), `gofmt`, `go vet`, `go build`, `go test -race`        |        ✅         |
| `lint`              | `golangci-lint run` (v2.12.2 via GHA action)                                 |        ✅         |
| `vuln`              | `go tool govulncheck ./...` (version pinned in `go.mod`)                     |        ✅         |
| `coverage`          | `just cover-check` + uploads `cover.out` artifact (14-day retention)         |        ✅         |
| `headless-e2e`      | `just headless-test` — build + canned JSON config, validates config generation |        ✅         |
| `iso-smoke recipe`  | `just --dry-run iso-smoke …` — keeps the headless ISO smoke recipe wired into CI |        ✅         |
| `headless ISO boot smoke` | Full ISO boot via serial console, fetches Flatcar PXE artifacts from CDN | ❌ non-required |

> **Known flake — `headless ISO boot smoke`:** Not a required check — does not
> block merges. Two known failure modes:
>
> 1. **CDN 404** (`curl: (22) 404`): artifact not yet propagated or CDN hiccup.
>    Transient; retry or ignore.
>
> 2. **`initrd-usr-fs.target` not in serial log** (regression since 2026-06-02,
>    tracked in issue #737): Flatcar stable moved to 4230.2.3. Once
>    `systemd-journald` starts (~2.9s kernel time), systemd stops writing to the
>    serial console; `initrd-usr-fs.target` is reached but only logged to the
>    journal, not serial. Fix options: pin Flatcar PXE version to a known-good
>    build, add `systemd.journald.forward_to_console=1` to the QEMU kernel
>    cmdline in `iso-smoke.sh`, or update the success signal to something visible
>    before journald takes over.
>
> If it fails, always verify it also reproduces on `main` before investigating.

**Tool version pinning:** `govulncheck` is pinned in `go.mod` via `go tool`.
`golangci-lint` is pinned in `Justfile::GOLANGCI_LINT_VERSION` (local) and
`ci.yml::golangci-lint-action version` (CI). Bump both together.

**Go toolchain pinning:** Go version is pinned in both `go.mod` (`go` + `toolchain`
directives) and `ci.yml` (`go-version`). Update all three together when bumping.
`govulncheck` gates on stdlib CVEs — a stale toolchain will fail the `vuln` check
even when no application-level vulnerabilities exist.

Concurrency: per-ref, with `cancel-in-progress: true` — pushes to the same
branch cancel earlier in-flight runs.

Permissions: `contents: read` at workflow scope. Each job specifies its own
needs. `persist-credentials: false` on every `actions/checkout` — keeps the
`GITHUB_TOKEN` out of the working directory.

### `.github/workflows/bonedigger.yml`

Issue lifecycle automation (added 2026-06-03):

| Trigger | What it does |
| ------- | ------------ |
| Issue events | Pipeline state widget embedded in issue bodies |
| `/claim`, `/unclaim` | Assign/unassign the commenter as owner |
| `/approve`, `/lgtm` | Move issue to approved/queued state |
| `/wontfix` | Close as won't fix with label |
| Stale scan | Detects and cleans up stale claims |

### `.github/workflows/security.yml`

| Job                 | When                          | What                                          |
| ------------------- | ----------------------------- | --------------------------------------------- |
| `codeql`            | push, PR, weekly cron         | CodeQL Go scan, `security-and-quality` suite  |
| `dependency-review` | PR only                       | Block PRs that introduce high-severity CVEs   |
| `scorecard`         | push to main, weekly cron     | OSSF Scorecard → SARIF upload to code scanning |

Weekly cron is Mondays at 06:37 UTC — odd minute on purpose, avoids the
00/30 GitHub Actions stampede.

### `.github/workflows/nightly.yml`

Nightly and manually-triggered extended validation, including the migrated integration suite.

| Job                   | What it does                                              | Timeout |
| --------------------- | --------------------------------------------------------- | ------- |
| `nightly-test`        | Runs `go test -race -count=1 ./...` 10x to catch flakes   | 30 min  |
| `nightly-coverage`    | Runs `just cover-check`, uploads `cover.out`, flags Codecov nightly | 15 min |
| `nightly-vuln`        | Runs `govulncheck ./...` against latest advisories        | 10 min  |
| `nightly-integration` | Runs `go test -race -count=1 -v -tags=integration ./...`  | 15 min  |

### `.github/workflows/vm-e2e.yml`

Full VM install + boot + domain assertions. Triggered via `workflow_dispatch` (on-demand) and on `push` to `main` when `internal/**`, `cmd/**`, `scripts/**`, `go.mod`, or `go.sum` change.

| Job       | What it does                                                         | Timeout |
| --------- | -------------------------------------------------------------------- | ------- |
| `prepare` | Downloads + caches Flatcar base image (keyed on version.txt)         | 10 min  |
| `dhcp`    | Full install with DHCP networking; verifies hostname + update strategy | 35 min |
| `static`  | Full install with static IP 10.0.2.15/24; verifies networkd unit content | 35 min |
| `sysext`  | Full install with docker sysext; verifies docker.raw + systemd-sysext active | 45 min |
| `nvidia`  | Full install with NVIDIA config; verifies sysupdate.d entries present | 35 min |

All jobs run in parallel after `prepare`. Each uses KVM-enabled GHA ubuntu-latest runners.
Flatcar base image (~480 MB) is cached by `actions/cache` keyed on `FLATCAR_VERSION` from version.txt.

### `.github/workflows/release.yml`

Builds the binary + installer ISO on `v*` tags, publishes a GitHub Release
with SHA256 sidecars and cosign `.bundle` files (keyless Sigstore signing).
See `scripts/build-iso-ci.sh` for the ISO recipe used in CI
(`grub-mkstandalone` path). Local builds use `scripts/build-iso.sh`.

## Local Reproduction

Everything CI does is reachable from `just`:

```bash
just ci          # full pre-push gate
just fmt-check   # mirrors CI gofmt step
just vuln        # govulncheck (installs to $GOBIN)
just cover-check # per-package thresholds
```

If `just ci` passes locally but fails in CI, the gap is one of:
- Go version drift (CI pins exact patch version, e.g. `1.26.4`; check `go env GOVERSION`)
- `govulncheck` failing on stdlib CVEs — means your local Go is older than CI; run `go get toolchain@go1.X.Y`
- A network-dependent test running unintentionally (check for missing
  `//go:build integration` tag)
- An untracked file in your checkout that CI doesn't see

## Adding a Test

- Unit tests live next to the code (`foo.go` → `foo_test.go`).
- Fixtures go in the package's local `testdata/` (compiler ignores it).
- Golden files use the `-update` pattern: `go test ./internal/ignition -update`.
  Commit the rewritten `*.golden.json` deliberately; review the diff.
- Don't reach for the network in a unit test. Use `httptest.NewServer` or a
  `SpyRunner` stub. Integration tests that hit real APIs go behind
  `//go:build integration`.

### Test Scaffold Patterns

**Bubble Tea TUI — `noopMsg{}` to reach form-state delegation**

`tui.Update()` has a `case tea.WindowSizeMsg:` that returns early when
`m.activeForm != nil`, bypassing form-state lines 244-256. A sentinel message
type that falls through all switch arms forces delegation to the active form:

```go
type noopMsg struct{}

m := newTestModel(t)
m.activeForm = buildTestForm() // set form with pre-configured State
m, _ = m.Update(noopMsg{})    // reaches form delegation path
```

**Bubble Tea TUI — testing `huh` form state transitions**

`huh.Form.State` is an exported field; `huh.StateCompleted` and
`huh.StateAborted` are exported constants. No mocking required:

```go
f := huh.NewForm(huh.NewGroup(huh.NewNote().Title("test")))
f.State = huh.StateCompleted
m.activeForm = f
m, _ = m.Update(noopMsg{})
// assert m.activeForm == nil (form cleared after completion)
```

**`internal/github` — `io.ReadAll` error path via custom transport**

Inject a failing `http.RoundTripper` to exercise the `io.ReadAll` error branch
(github.go line 66-68) without touching any real network:

```go
type errReader struct{}
func (errReader) Read([]byte) (int, error) { return 0, errors.New("injected read error") }

type brokenBodyTransport struct{}
func (brokenBodyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
    return &http.Response{
        StatusCode: http.StatusOK,
        Header:     make(http.Header),
        Body:       io.NopCloser(errReader{}),
    }, nil
}

client := &http.Client{Transport: brokenBodyTransport{}}
```

**Dead code note — `panelWidth < 20` in `renderDetailPanel` (tui.go:984-986)**

This guard is unreachable. `effectiveWidth < 60` returns early first; when
`effectiveWidth >= 60`, `panelWidth = min(52, effectiveWidth-32) >= 28 > 20`
always. The `panelWidth < 20` block is dead code and can be removed if desired.

## Adding a CI Job

- Pin the action by version, not `@main`.
- Set `permissions:` at job scope, request the minimum.
- Set `persist-credentials: false` on `actions/checkout`.
- Add it to the matrix in this doc and in `AGENTS.md`'s checklist.

## VM e2e Passes

`just vm-e2e` runs four automated passes in sequence inside QEMU (no manual
intervention required). Each pass builds a fresh overlay image, installs via
`knuckle --headless --config`, boots the installed system, and SSH-verifies the result.

| Pass          | Config                                    | Verifies                                              | Timeout |
| ------------- | ----------------------------------------- | ----------------------------------------------------- | ------- |
| DHCP          | DHCP networking, hostname, update groups  | hostname, OEM update strategy, locksmith enabled      | 15m     |
| Static        | Static IP 10.0.2.15/24, gw 10.0.2.2      | `/etc/systemd/network/10-static.network` content      | 15m     |
| Sysext        | DHCP + docker sysext                      | `/etc/extensions/docker.raw` present, `docker version` runs | 25m |
| NVIDIA        | DHCP + NVIDIA GPU config (emulated)       | `/etc/sysupdate.d/nv-*.conf` present, kernel module config | 15m |

The static pass uses QEMU's slirp NAT subnet so SSH port-forwarding still works
even with a static IP configured inside the VM. Interface name is currently
hardcoded to `ens3` — may need `eth0` on some Flatcar versions (open issue).

## Hardware-like Repro

`just hardware-repro` is the closest local reproduction path for "boot the
installer ISO, then fail inside `flatcar-install`" without needing bare metal.
It boots the built installer ISO with:

- `q35` machine type
- UEFI (`OVMF_CODE.fd` + writable `OVMF_VARS.fd`)
- SATA target disk (`ide-hd`, serial `target-disk`)
- `e1000e` NIC
- QEMU `fw_cfg` Ignition so SSH is available in the live installer

The recipe runs `knuckle --headless` inside that live ISO environment so it
exercises the same `internal/install` handoff as the TUI, while making failure
capture deterministic. On every run it writes:

- `.vm/hardware-install-output.log` — CLI output from the headless install run
- `.vm/hardware-knuckle.log` — `/tmp/knuckle.log` from the live installer
- `.vm/hardware-journal.log` — selected system journal from the live boot
- `.vm/hardware-disk-inventory.log` — `lsblk` plus `/dev/disk/by-id` inventory
- `.vm/hardware-installer-serial.log` — VM serial console output

## VM e2e (Local or GHA)

VM e2e tests can run on any Linux machine with KVM, or via the `vm-e2e.yml` GHA workflow.
See `docs/TEST-PLAN.md` for the full E2E scenario table.

**Running tests locally:** Any Linux machine with KVM can serve as the test host. `just vm-e2e` runs all 4 passes locally. See [GHOST-LAB.md](GHOST-LAB.md) for setup on a dedicated host.

**Running in GHA:** Use `gh workflow run vm-e2e.yml` or trigger via the Actions tab. Results appear as job logs; serial logs and knuckle.log are uploaded as artifacts on failure.

Test tiers by domain label (see `knuckle-qa` skill for full matrix):
| Labels | Tier | Runs |
|---|---|---|
| `validate`, `model`, `runner`, `wizard`, `tui`, etc. | 0 | `just ci` on dev machine |
| `domain:probe`, `domain:security` | 1 | Tier 0 + ghost VM tool check + dry-run |
| `domain:install`, `domain:headless`, `domain:ignition` | 2 | Tier 1 + real headless install on ghost |
| `domain:iso` | 3 | Tier 2 + `hardware-repro` on ghost |

## ISO Build Internals

The installer ISO injects knuckle into Flatcar's `usr.squashfs` — the only
reliable method for Flatcar PXE live boot.

- **`squashfs-root/` = `/usr/` in the live system.** `squashfs-root/bin/knuckle` → `/usr/bin/knuckle`. Units in `squashfs-root/lib/systemd/system/`.
- **Binary:** `scripts/build-iso.sh` uses `bin/knuckle` (`just build`, `CGO_ENABLED=0`). Never the repo-root binary — may contain AVX instructions that crash with `trap invalid opcode` in QEMU.
- **QEMU:** always `-cpu host`. Without it AVX binaries crash silently. Use `-display gtk` to see the TUI on tty1.
- **Ignition in QEMU:** pass via `-fw_cfg name=opt/org.flatcar-linux/config,file=config.ign`. The `ignition.config.data=` kernel cmdline is silently ignored on the QEMU platform.
- **`systemd.gpt_auto=0` is REQUIRED on every BLS entry.** Without it, bare-metal GPT disks trigger `systemd-gpt-auto-generator` → dracut `xd2root` hook is skipped → installer fails to appear. Both `knuckle.conf` and `knuckle-serial.conf` must include this. Root cause of the v0.6.2 regression (fixed v0.7.0).
- **`rd.driver.pre=loop` is REQUIRED on every BLS entry.** `/usr` is a loop-mounted squashfs inside the initrd. If the `loop` module is not autoloaded before `sysroot-usr.mount`, mount fails with `failed to setup loop device for /usr.squashfs` → `initrd-root-fs.target` fails → emergency shell 10-15s in, before knuckle starts. Reproduced on bare-metal Supermicro M11SDV-8C-LN4F board; QEMU boots win the race and hide it, so `iso-smoke` will NOT catch a regression here. Flatcar's initramfs uses the same mechanism for btrfs (`rd.driver.pre=btrfs` via `/etc/cmdline.d`); dracut merges all occurrences rather than overriding.
- **Pure systemd-boot, no GRUB.** ESP contains only `EFI/BOOT/BOOTX64.EFI` (from `systemd-boot-efi`), `loader/loader.conf`, and two BLS entries.
- **Flatcar `.DIGESTS.asc`** = PGP clearsigned. Verify with `gpg --decrypt asc > content`, not `gpg --verify` (detached form — always fails on clearsigned input).
- **Build cache:** squashfs is content-addressed on `sha256sum bin/knuckle` — skips repack when binary unchanged.
- **`just vm-e2e` does NOT test ISO boot.** Use `just iso-smoke <iso> <ovmf>` for headless serial-log ISO boot validation on ghost.

## Roadmap

Tracked in `docs/REVIEW-2026-05-19.md` (passes 1-2) and session notes from
2026-05-20 (passes 3-4). Highlights:

- ~~Promote `just headless-test` into a CI job~~ — **done** (2026-05-19,
  `headless-e2e` job in `ci.yml`).
- ~~SSH keys silently dropped in headless path~~ — **fixed** (2026-05-20,
  `resolveSysexts()` + `StepUser` handler).
- ~~Sysexts silently dropped in headless path~~ — **fixed** (2026-05-20,
  `resolveSysexts()` added to headless `Run()`).
- ~~Goroutine leak on force-quit during install~~ — **fixed** (2026-05-20,
  `installCancel` stored in Model, called on all quit paths).
- ~~`just vm` required manual reboot step~~ — **fixed** (2026-05-20, `just vm`
  now boots installed system automatically after knuckle exits).
- ~~Move vm-e2e and integration tests to GHA~~ — **done** (2026-05-30,
  `vm-e2e.yml` + `nightly-integration` job).
- ~~Blocker B1~~ GPG signature verification — **closed**. `internal/bakery/verify.go` validates Flatcar release `.DIGESTS.asc` against the embedded signing key. Separate from knuckle's own release artifact signing (cosign keyless via Sigstore in `release.yml`).
- ~~Blocker B2~~ Reboot via runner — **closed**.
- ~~Blocker B3~~ `validate.DiskPath()` in headless — **closed**.
- ~~Blocker B4~~ SSH keys reaching Ignition — **closed** (2026-05-20).
- Land `FuzzHostname`, `FuzzCIDR`, `FuzzSSHKey` and run with `-fuzztime=30s`
  in a nightly job.
- N-SEC1 MEDIUM: add max-length check on sysext download URLs in `bakery.go`.
- Verify `ens3` vs `eth0` interface name in static-network vm-e2e pass.
- Add fixture gaps: `lsblk-empty.json`, `lsblk-all-removable.json`,
  `ip_addr-ipv6-only.json`, `bakery-malformed-digests` (from QA review).
