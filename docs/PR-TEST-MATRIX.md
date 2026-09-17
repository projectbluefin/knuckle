# PR Test Matrix — knuckle VM e2e

> **Audience:** Agents and maintainers running PR tests locally (KVM) or via GitHub Actions.
> **Scope:** Defines exactly what to run, what to capture, and what constitutes
> PASS/FAIL for each GitHub label domain.

---

## Running VM e2e Tests

### Option A — GitHub Actions (on-demand)

```bash
# Trigger all 4 passes via workflow_dispatch
gh workflow run vm-e2e.yml --repo projectbluefin/knuckle

# On a specific branch
gh workflow run vm-e2e.yml --repo projectbluefin/knuckle --ref feat/my-branch

# Watch progress
gh run list --repo projectbluefin/knuckle --workflow vm-e2e.yml --limit 3
```

### Option B — Local machine (KVM required)

```bash
just vm-e2e    # runs all 4 passes: DHCP → Static → Sysext → NVIDIA
```

Any Linux machine with `/dev/kvm` accessible works. Ghost (192.168.1.102) remains available as a dedicated test host but is no longer required.

---

## Conventions

### QA Host Layout (local or ghost)

```
.vm/   (local)  or  /var/tmp/knuckle-test/  (dedicated host)
├── flatcar_base_amd64.img          ← shared read-only base (CoW backing)
├── pr-NNN/                         ← one dir per PR under test
│   ├── knuckle                     ← built binary for this PR
│   ├── boot.qcow2                  ← installer VM overlay (ephemeral)
│   ├── target.qcow2                ← install target (ephemeral)
│   ├── e2e_key / e2e_key.pub       ← ephemeral SSH keypair
│   ├── config.ign                  ← ignition for installer VM
│   ├── headless.json               ← headless install config
│   ├── serial-installer.log        ← serial console of installer VM
│   ├── serial-target.log           ← serial console of installed system
│   ├── knuckle.log                 ← knuckle's --log-file output
│   └── report.md                   ← lab report (generated)
└── flatcar_base_amd64.img.lock     ← flock'd during first-time download
```

### Port Allocation

```
port = 20000 + PR_NUMBER
```

| PR | SSH port on ghost | Constraint |
|----|-------------------|------------|
| #170 | 22170 | one port per PR |
| #215 | 22215 | passes within domain are sequential |
| #999 | 22999 | max PR# 3535 stays under 55535 |

VM inside that PR always uses the same port. Passes within a domain run
**sequentially** — boot VM, test, kill VM, boot next VM — never concurrently
on the same port.

### SSH Binding

`hostfwd=tcp::PORT-:22` binds to `127.0.0.1:PORT` on ghost. All SSH is:

```bash
ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
    -o LogLevel=ERROR -i /var/tmp/knuckle-test/pr-NNN/e2e_key \
    -p PORT core@127.0.0.1
```

**You connect FROM ghost, not through it.**

### QEMU Baseline Args (ghost, amd64 KVM)

```bash
QEMU=/usr/bin/qemu-system-x86_64   # or detect via: which qemu-system-x86_64

QEMU_BASE=(
  -m 4096 -smp 2 -enable-kvm
  -net nic,model=virtio
  -net "user,hostfwd=tcp::${PORT}-:22"
  -display none -daemonize
  -serial "file:${WORKDIR}/serial-${TAG}.log"
  -pidfile "${WORKDIR}/qemu-${TAG}.pid"
)
```

### Binary Build on ghost

```bash
cd ~/src/knuckle
git fetch upstream && git checkout pr/NNN   # or: upstream/main for main tests
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
  go build -ldflags="-s -w" -o /var/tmp/knuckle-test/pr-NNN/knuckle ./cmd/knuckle
```

### Ephemeral SSH Key

```bash
WORKDIR=/var/tmp/knuckle-test/pr-NNN
ssh-keygen -t ed25519 -f $WORKDIR/e2e_key -N "" -C "knuckle-pr-NNN" -q
E2E_PUB=$(cat $WORKDIR/e2e_key.pub)
SSH_OPTS="-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR"
VM_SSH="ssh $SSH_OPTS -i $WORKDIR/e2e_key -p $PORT core@127.0.0.1"
VM_SCP="scp $SSH_OPTS -i $WORKDIR/e2e_key -P $PORT"
```

### Ignition for Installer VM (reusable)

```bash
printf '{"ignition":{"version":"3.4.0"},"passwd":{"users":[{"name":"core","sshAuthorizedKeys":["%s"]}]},"systemd":{"units":[{"name":"sshd.service","enabled":true}]}}\n' \
  "$E2E_PUB" > $WORKDIR/config.ign
```

### CoW Overlay (reusable)

```bash
BASE=/var/tmp/knuckle-test/flatcar_base_amd64.img
qemu-img create -f qcow2 -b $BASE -F qcow2 $WORKDIR/boot.qcow2
qemu-img create -f qcow2 $WORKDIR/target.qcow2 20G
```

---

## Domain Test Matrix

### 1. `validate`

**Test type:** unit-only

**Test commands (run on ghost or dev machine):**
```bash
go test -race -count=1 -coverprofile=cover-validate.out ./internal/validate/...
go tool cover -func=cover-validate.out | grep total
```

**Evidence to collect:**
- `go test -v` output (all test names + PASS/FAIL)
- Coverage percentage line

**Pass criteria:**
- All tests pass (`--- PASS`)
- Coverage ≥ 85%
- No race detector errors

**Estimated time:** 5 seconds

---

### 2. `probe`

**Test type:** unit + VM-binary

**Phase A — unit (on ghost, no VM):**
```bash
go test -race -count=1 -v -coverprofile=cover-probe.out ./internal/probe/...
go tool cover -func=cover-probe.out | grep total
```

**Phase B — VM binary (verify real lsblk/ip parsing on Flatcar):**

Boot installer VM, SCP binary, run probe dump:
```bash
# Boot installer VM
$QEMU ${QEMU_BASE[@]} \
  -drive if=virtio,file=$WORKDIR/boot.qcow2,format=qcow2 \
  -drive if=virtio,file=$WORKDIR/target.qcow2,format=qcow2 \
  -fw_cfg name=opt/org.flatcar-linux/config,file=$WORKDIR/config.ign

# Wait for SSH
for i in $(seq 1 30); do $VM_SSH true 2>/dev/null && break; sleep 3; done

# Deploy + run probe verification
$VM_SCP $WORKDIR/knuckle core@127.0.0.1:/tmp/knuckle
$VM_SSH "sudo /tmp/knuckle --probe-dump 2>/dev/null || true"
# Note: --probe-dump doesn't exist yet; substitute with:
$VM_SSH "lsblk -J && ip -j addr" | tee $WORKDIR/probe-output.json

# Kill VM
kill $(cat $WORKDIR/qemu-installer.pid) 2>/dev/null || true
```

**Evidence to collect:**
- `go test -v` output
- `$WORKDIR/probe-output.json` — raw lsblk/ip JSON from Flatcar
- Whether `/dev/disk/by-id/` entries appear (confirms by-id path works)

**Pass criteria:**
- All unit tests pass
- Coverage ≥ 80%
- `lsblk -J` inside VM produces valid JSON with disk entries
- `/dev/disk/by-id/` symlinks exist (`ls /dev/disk/by-id/ | wc -l` ≥ 1)

**Estimated time:** 3 min (1 min boot + 1 min test + 1 min cleanup)

---

### 3. `ignition`

**Test type:** unit + VM-binary (boot generated Ignition, verify config applied)

**Phase A — unit:**
```bash
go test -race -count=1 -v -coverprofile=cover-ignition.out ./internal/ignition/...
# Update golden files if needed:
go test -run TestGenerate ./internal/ignition/... -update
go tool cover -func=cover-ignition.out | grep total
```

**Phase B — Ignition boot verification:**

Generate Ignition via headless --dry-run (writes /tmp/knuckle-ignition-preview.json):
```bash
# Config with hostname, timezone, static network, sysexts
cat > $WORKDIR/ignition-test.json <<'EOF'
{
  "channel": "stable", "hostname": "ign-verify", "timezone": "America/New_York",
  "network": {"mode": "static", "interface": "eth0",
              "address": "10.0.2.15/24", "gateway": "10.0.2.2"},
  "users": [{"username": "core", "ssh_keys": ["SSH_PUB_PLACEHOLDER"]}],
  "disk": "/dev/vdb", "update_strategy": "off", "reboot": false
}
EOF
# Replace placeholder
sed -i "s|SSH_PUB_PLACEHOLDER|$E2E_PUB|" $WORKDIR/ignition-test.json

# Generate Ignition via dry-run (extracts ign from log)
$WORKDIR/knuckle --headless --config $WORKDIR/ignition-test.json --dry-run \
  --log-file $WORKDIR/knuckle-dryrun.log
# Extract generated ignition from dry-run log
grep -A200 'ignition json:' $WORKDIR/knuckle-dryrun.log | tail -n +2 \
  > $WORKDIR/generated.ign 2>/dev/null || true
# Note: if --dry-run doesn't emit ign to log, use --headless without --dry-run
# and capture the /tmp/knuckle-*.ign file before install starts (race-y).
# Better: add a --emit-ignition flag (future work).
```

Alternative (current best approach — use headless with a fake disk path):
```bash
# headless + dry-run produces the ignition file and exits without flatcar-install
$WORKDIR/knuckle --headless --config $WORKDIR/ignition-test.json \
  --dry-run --log-file $WORKDIR/dry-run.log 2>&1 | tee $WORKDIR/dry-run-output.txt

# knuckle writes generated ignition to /tmp/knuckle-NNN.ign before --dry-run exits
ls -la /tmp/knuckle-*.ign 2>/dev/null | tail -1
IGNFILE=$(ls -t /tmp/knuckle-*.ign 2>/dev/null | head -1)
[ -n "$IGNFILE" ] && cp "$IGNFILE" $WORKDIR/generated.ign
```

Boot VM with the generated Ignition:
```bash
qemu-img create -f qcow2 -b $BASE -F qcow2 $WORKDIR/boot-igntest.qcow2

$QEMU ${QEMU_BASE[@]} \
  -drive if=virtio,file=$WORKDIR/boot-igntest.qcow2,format=qcow2 \
  -fw_cfg name=opt/org.flatcar-linux/config,file=$WORKDIR/generated.ign

for i in $(seq 1 30); do $VM_SSH true 2>/dev/null && break; sleep 3; done

# Verify config was applied
$VM_SSH "hostname"                                           | tee $WORKDIR/ign-hostname.txt
$VM_SSH "readlink /etc/localtime"                           | tee $WORKDIR/ign-timezone.txt
$VM_SSH "cat /etc/systemd/network/10-static.network"        | tee $WORKDIR/ign-network.txt
$VM_SSH "grep REBOOT_STRATEGY /etc/flatcar/update.conf"     | tee $WORKDIR/ign-update.txt

kill $(cat $WORKDIR/qemu-igntest.pid) 2>/dev/null || true
```

**Evidence to collect:**
- `go test -v` unit output
- Coverage percentage
- `$WORKDIR/ign-hostname.txt` — must contain `ign-verify`
- `$WORKDIR/ign-timezone.txt` — must contain `America/New_York`
- `$WORKDIR/ign-network.txt` — must contain `Address=10.0.2.15/24` and `Gateway=10.0.2.2`
- `$WORKDIR/ign-update.txt` — must contain `REBOOT_STRATEGY=off`

**Pass criteria:**
- All unit tests pass (including golden file diffs)
- Coverage ≥ 85%
- All 4 on-disk values match expected (hostname, timezone, network, update strategy)

**Estimated time:** 8 min (unit 1 min + dry-run 30s + VM boot 3 min + verify 1 min + cleanup)

---

### 4. `headless`

**Test type:** unit + headless-install (dry-run on host, then real install in VM)

**Phase A — unit:**
```bash
go test -race -count=1 -v -coverprofile=cover-headless.out ./internal/headless/...
go tool cover -func=cover-headless.out | grep total
```

**Phase B — dry-run on host (no VM needed, fast):**
```bash
$WORKDIR/knuckle --headless --config $WORKDIR/headless-dhcp.json \
  --dry-run --log-file $WORKDIR/headless-dryrun.log
echo "exit: $?"
```

Where `headless-dhcp.json`:
```json
{
  "channel": "stable", "hostname": "hl-test", "timezone": "UTC",
  "network": {"mode": "dhcp"},
  "users": [{"username": "core", "ssh_keys": ["ssh-ed25519 AAAA... test"]}],
  "disk": "/dev/vdb", "update_strategy": "off", "reboot": false
}
```

**Phase C — real headless install in VM:**
```bash
# Standard vm-e2e DHCP pass (see install domain for full commands)
# Key difference: capture knuckle.log from inside VM
$VM_SCP $WORKDIR/knuckle core@127.0.0.1:/tmp/knuckle
$VM_SCP $WORKDIR/headless-dhcp.json core@127.0.0.1:/tmp/headless.json
$VM_SSH "timeout 15m sudo /tmp/knuckle --headless --config /tmp/headless.json \
  --log-file /tmp/knuckle.log" 2>&1 | tee $WORKDIR/install-output.txt
$VM_SSH "cat /tmp/knuckle.log" > $WORKDIR/knuckle.log
```

**Evidence to collect:**
- Unit test output + coverage
- Dry-run exit code (must be 0)
- `$WORKDIR/install-output.txt` (stdout of headless install)
- `$WORKDIR/knuckle.log` (knuckle's internal log from VM)

**Pass criteria:**
- All unit tests pass; coverage ≥ 70%
- Dry-run exits 0
- Real install exits 0 (knuckle.log shows `flatcar-install` completed)
- `knuckle.log` contains no `ERROR` lines

**Estimated time:** 15 min (unit 30s + dry-run 10s + VM boot 3 min + install 10 min)

---

### 5. `install`

**Test type:** unit + full-vm-install

This is the gold standard test. Run the full vm-e2e DHCP pass and verify the installed system boots.

**Phase A — unit:**
```bash
go test -race -count=1 -v -coverprofile=cover-install.out ./internal/install/...
go tool cover -func=cover-install.out | grep total
```

**Phase B — full install + boot verification:**
```bash
# Step 1: Boot installer VM
PORT=20000+NNN
qemu-img create -f qcow2 -b $BASE -F qcow2 $WORKDIR/boot.qcow2
qemu-img create -f qcow2 $WORKDIR/target.qcow2 20G

$QEMU -m 4096 -smp 2 -enable-kvm \
  -drive if=virtio,file=$WORKDIR/boot.qcow2,format=qcow2 \
  -drive if=virtio,file=$WORKDIR/target.qcow2,format=qcow2 \
  -fw_cfg name=opt/org.flatcar-linux/config,file=$WORKDIR/config.ign \
  -net nic,model=virtio -net "user,hostfwd=tcp::${PORT}-:22" \
  -display none -daemonize \
  -serial file:$WORKDIR/serial-installer.log \
  -pidfile $WORKDIR/qemu-installer.pid

for i in $(seq 1 40); do $VM_SSH true 2>/dev/null && break; sleep 3; done

# Step 2: Run headless install (downloads Flatcar ~400MB, writes to /dev/vdb)
$VM_SCP $WORKDIR/knuckle core@127.0.0.1:/tmp/knuckle
$VM_SCP $WORKDIR/headless-dhcp.json core@127.0.0.1:/tmp/config.json
$VM_SSH "timeout 15m sudo /tmp/knuckle --headless --config /tmp/config.json \
  --log-file /tmp/knuckle.log" 2>&1 | tee $WORKDIR/install-output.txt
$VM_SSH "cat /tmp/knuckle.log" > $WORKDIR/knuckle.log

# Step 3: Kill installer VM
kill $(cat $WORKDIR/qemu-installer.pid) 2>/dev/null || true; sleep 2

# Step 4: Boot installed target
$QEMU -m 2048 -smp 2 -enable-kvm \
  -drive if=virtio,file=$WORKDIR/target.qcow2,format=qcow2 \
  -net nic,model=virtio -net "user,hostfwd=tcp::${PORT}-:22" \
  -display none -daemonize \
  -serial file:$WORKDIR/serial-target.log \
  -pidfile $WORKDIR/qemu-target.pid

for i in $(seq 1 60); do $VM_SSH true 2>/dev/null && break; sleep 5; done

# Step 5: Verify
$VM_SSH "hostname"                                       | tee $WORKDIR/verify-hostname.txt
$VM_SSH "grep ^VERSION= /etc/os-release"                | tee $WORKDIR/verify-version.txt
$VM_SSH "grep REBOOT_STRATEGY /etc/flatcar/update.conf" | tee $WORKDIR/verify-update.txt

kill $(cat $WORKDIR/qemu-target.pid) 2>/dev/null || true
```

**Evidence to collect:**
- Unit test output + coverage
- `$WORKDIR/install-output.txt` + `$WORKDIR/knuckle.log`
- `$WORKDIR/verify-hostname.txt` — expected: `hl-test`
- `$WORKDIR/verify-version.txt` — expected: non-empty `VERSION=X.Y.Z`
- `$WORKDIR/verify-update.txt` — expected: `REBOOT_STRATEGY=off`
- `$WORKDIR/serial-installer.log` + `$WORKDIR/serial-target.log` (attach on failure)

**Pass criteria:**
- All unit tests pass; coverage ≥ 70%
- knuckle exits 0 during headless install
- Installed system boots and accepts SSH
- hostname / update strategy verified on installed system

**Estimated time:** 20 min (unit 30s + boot 3 min + install 12 min + target boot 4 min)

---

### 6. `wizard`

**Test type:** unit + headless-install

**Phase A — unit:**
```bash
go test -race -count=1 -v -coverprofile=cover-wizard.out ./internal/wizard/...
go tool cover -func=cover-wizard.out | grep total
```

**Phase B — headless-install (wizard drives the same logic path):**

Same as headless Phase C above. The wizard's step state machine is exercised
by every headless run. Add a second config variant to cover wizard branches:

```bash
# Config 2: static network (covers network wizard branch)
cat > $WORKDIR/headless-static.json <<EOF
{"channel":"stable","hostname":"wiz-static","timezone":"UTC",
 "network":{"mode":"static","interface":"eth0","address":"10.0.2.15/24","gateway":"10.0.2.2"},
 "users":[{"username":"core","ssh_keys":["$E2E_PUB"]}],
 "disk":"/dev/vdb","update_strategy":"off","reboot":false}
EOF
$WORKDIR/knuckle --headless --config $WORKDIR/headless-static.json \
  --dry-run --log-file $WORKDIR/wizard-static-dryrun.log
```

**Evidence to collect:**
- Unit test output + coverage
- Dry-run exit codes for DHCP + static configs (both must be 0)
- wizard test output showing all step transitions (from -v flag)

**Pass criteria:**
- All unit tests pass; coverage ≥ 70%
- Both dry-run configs exit 0

**Estimated time:** 3 min

---

### 7. `bakery`

**Test type:** unit-only (integration tests behind `//go:build integration`)

**Phase A — unit (no network):**
```bash
go test -race -count=1 -v -coverprofile=cover-bakery.out ./internal/bakery/...
go tool cover -func=cover-bakery.out | grep total
```

**Phase B — integration (network required, not in CI gate):**
```bash
go test -race -count=1 -v -tags=integration ./internal/bakery/... \
  -run TestFetch 2>&1 | tee $WORKDIR/bakery-integration.txt
```

**Evidence to collect:**
- Unit test output + coverage
- Integration output (if run): shows live catalog entry count, Flatcar version string

**Pass criteria:**
- All unit tests pass; coverage ≥ 80%
- (Integration) catalog returns ≥ 10 sysext entries; version string matches semver

**Estimated time:** 30 seconds (unit-only); +2 min with integration

---

### 8. `github`

**Test type:** unit-only (integration tests behind `//go:build integration`)

**Phase A — unit:**
```bash
go test -race -count=1 -v -coverprofile=cover-github.out ./internal/github/...
go tool cover -func=cover-github.out | grep total
```

**Phase B — integration (network, not CI gate):**
```bash
go test -race -count=1 -v -tags=integration ./internal/github/... \
  -run TestFetchReal 2>&1 | tee $WORKDIR/github-integration.txt
```

**Evidence to collect:**
- Unit test output + coverage
- (Integration) number of SSH keys returned for known user

**Pass criteria:**
- All unit tests pass; coverage ≥ 90%
- (Integration) key fetch returns ≥ 1 key with valid `ssh-` prefix

**Estimated time:** 15 seconds (unit-only); +30 seconds with integration

---

### 9. `sysext`

**Test type:** unit + full-vm-install

This domain validates the full sysext path: bakery catalog lookup → Ignition
`storage.files` entry for `.raw` download → systemd-sysext activation on first boot.

**Phase A — unit (ignition sysext template):**
```bash
go test -race -count=1 -v -run TestSysext ./internal/ignition/...
go test -race -count=1 -v -run TestSysext ./internal/bakery/...
```

**Phase B — full VM install with sysext:**
```bash
# Config: docker sysext selected
cat > $WORKDIR/headless-sysext.json <<EOF
{"channel":"stable","hostname":"sysext-test","timezone":"UTC",
 "network":{"mode":"dhcp"},
 "users":[{"username":"core","ssh_keys":["$E2E_PUB"]}],
 "disk":"/dev/vdb","sysexts":["docker"],
 "update_strategy":"off","reboot":false}
EOF

# Boot installer VM (same pattern as install domain)
# ... [boot, wait for SSH, SCP knuckle + config] ...

$VM_SSH "timeout 25m sudo /tmp/knuckle --headless \
  --config /tmp/sysext.json --log-file /tmp/knuckle.log" \
  2>&1 | tee $WORKDIR/sysext-install-output.txt
$VM_SSH "cat /tmp/knuckle.log" > $WORKDIR/sysext-knuckle.log

# Kill installer VM; boot target
# ... [standard boot-target pattern] ...

# Verify sysext
$VM_SSH "test -f /etc/extensions/docker.raw && echo 'docker.raw: present'" \
  | tee $WORKDIR/sysext-verify-raw.txt
$VM_SSH "stat -c%s /etc/extensions/docker.raw 2>/dev/null" \
  | tee $WORKDIR/sysext-verify-size.txt
$VM_SSH "systemctl is-active systemd-sysext" \
  | tee $WORKDIR/sysext-verify-active.txt
$VM_SSH "docker version --format '{{.Server.Version}}' 2>/dev/null || echo 'docker not available'" \
  | tee $WORKDIR/sysext-verify-docker.txt
```

**Evidence to collect:**
- Unit test output
- `$WORKDIR/sysext-verify-raw.txt` — must say "docker.raw: present"
- `$WORKDIR/sysext-verify-size.txt` — must be > 0 bytes
- `$WORKDIR/sysext-verify-active.txt` — must say "active"
- `$WORKDIR/sysext-verify-docker.txt` — must contain a version string

**Pass criteria:**
- Unit tests pass
- `docker.raw` present and > 0 bytes
- `systemd-sysext` active
- `docker version` exits 0 and returns a semver string

**Estimated time:** 25 min (unit 30s + install with sysext 20 min + target boot 4 min)

---

### 10. `security`

**Test type:** unit + VM-binary

**Phase A — unit:**
```bash
go test -race -count=1 -v -coverprofile=cover-security.out \
  ./internal/install/... ./internal/ignition/...
# Look specifically for file-permission and YAML-escaping tests
go test -race -count=1 -v -run TestIgnitionFile ./internal/install/...
go test -race -count=1 -v -run TestYAML ./internal/ignition/...
```

**Phase B — VM binary (verify on-disk file modes):**
```bash
# Run dry-run inside installer VM; check /tmp/knuckle-*.ign permissions
$VM_SSH "sudo /tmp/knuckle --headless --config /tmp/config.json \
  --dry-run --log-file /tmp/knuckle.log; \
  stat -c '%a %n' /tmp/knuckle-*.ign 2>/dev/null || echo 'no ign files'" \
  | tee $WORKDIR/security-filemodes.txt
```

**Evidence to collect:**
- Unit test output
- `$WORKDIR/security-filemodes.txt` — must show `600` (not `644` or `666`)
- Confirm file was cleaned up (removed after dry-run)

**Pass criteria:**
- Security unit tests pass (file permission assertions, YAML escaping corner cases)
- Ignition temp file mode is `600`, not world-readable
- Temp file is removed after use (not left on disk after dry-run completes)

**Estimated time:** 5 min (unit 1 min + VM boot 3 min + verify 1 min)

---

### 11. `tui`

**Test type:** unit + VM-binary (smoke test: does it launch without panic?)

**Phase A — unit:**
```bash
go test -race -count=1 -v -coverprofile=cover-tui.out ./internal/tui/...
go tool cover -func=cover-tui.out | grep total
```

**Phase B — VM binary smoke test:**

The TUI cannot be driven non-interactively without expect/tmux scripting.
The VM-binary test proves the binary launches and doesn't immediately crash.

```bash
# Deploy binary to installer VM
$VM_SCP $WORKDIR/knuckle core@127.0.0.1:/tmp/knuckle

# Launch knuckle in background, wait 5s, check it's still running
$VM_SSH "sudo /tmp/knuckle --log-file /tmp/knuckle.log &
  sleep 5
  if pgrep -x knuckle > /dev/null; then
    echo 'TUI_RUNNING'
    pkill knuckle 2>/dev/null || true
  else
    echo 'TUI_CRASHED'
    cat /tmp/knuckle.log
    exit 1
  fi" | tee $WORKDIR/tui-smoke.txt

# Check log for panic
$VM_SSH "grep -i 'panic\|nil pointer\|fatal error' /tmp/knuckle.log || echo 'no panics'" \
  | tee $WORKDIR/tui-panic-check.txt
```

**Evidence to collect:**
- Unit test output + coverage
- `$WORKDIR/tui-smoke.txt` — must contain `TUI_RUNNING`
- `$WORKDIR/tui-panic-check.txt` — must say "no panics"
- `$WORKDIR/knuckle.log` from VM (attach on failure)

**Pass criteria:**
- All unit tests pass; coverage ≥ 70%
- TUI process alive after 5 seconds
- No panic/nil-pointer in log

**Estimated time:** 8 min (unit 1 min + VM boot 3 min + smoke 1 min + cleanup)

**Note:** Full interactive TUI verification requires a human with a terminal.
The VM-binary smoke test only proves the binary doesn't panic on startup.
See `knuckle-testlab` skill for interactive TUI verification protocol.

---

### 12. `iso`

**Test type:** unit + VM-binary (boot ISO, verify SSH reaches live system)

**Phase A — unit:**
```bash
go test -race -count=1 -v -coverprofile=cover-iso.out ./internal/iso/...
go tool cover -func=cover-iso.out | grep total
```

**Phase B — build ISO + boot test:**
```bash
# Build ISO (on ghost — requires xorriso, mtools, cpio)
cd ~/src/knuckle  # checkout for this PR
just iso stable   # outputs output/knuckle-installer-stable-amd64.iso
ls -lh output/knuckle-installer-stable-amd64.iso

# Find OVMF on ghost
OVMF=""
for f in /usr/share/OVMF/OVMF_CODE_4M.fd /usr/share/OVMF/OVMF_CODE.fd \
          /usr/share/edk2/ovmf/OVMF_CODE_4M.fd; do
  [ -f "$f" ] && OVMF="$f" && break
done
[ -n "$OVMF" ] || { echo "OVMF not found — install ovmf"; exit 1; }

# Create Ignition for live system (SSH access)
printf '{"ignition":{"version":"3.4.0"},"passwd":{"users":[{"name":"core","sshAuthorizedKeys":["%s"]}]},"systemd":{"units":[{"name":"sshd.service","enabled":true}]}}\n' \
  "$E2E_PUB" > $WORKDIR/iso-config.ign

qemu-img create -f qcow2 $WORKDIR/iso-target.qcow2 20G

# Boot ISO with UEFI firmware
# Note: ISO live system gets Ignition via fw_cfg (NOT kernel cmdline — confirmed broken on QEMU)
$QEMU -m 4096 -smp 2 -enable-kvm \
  -drive if=pflash,format=raw,readonly=on,file="$OVMF" \
  -cdrom output/knuckle-installer-stable-amd64.iso \
  -drive if=virtio,file=$WORKDIR/iso-target.qcow2,format=qcow2 \
  -fw_cfg name=opt/org.flatcar-linux/config,file=$WORKDIR/iso-config.ign \
  -net nic,model=virtio -net "user,hostfwd=tcp::${PORT}-:22" \
  -display none -daemonize \
  -serial file:$WORKDIR/serial-iso.log \
  -pidfile $WORKDIR/qemu-iso.pid

# Wait for SSH (ISO live system takes ~30s to boot)
for i in $(seq 1 60); do $VM_SSH true 2>/dev/null && break; sleep 3; done

# Verify live system
$VM_SSH "uname -r"                   | tee $WORKDIR/iso-verify-kernel.txt
$VM_SSH "cat /etc/flatcar/release"   | tee $WORKDIR/iso-verify-release.txt
$VM_SSH "which knuckle"              | tee $WORKDIR/iso-verify-knuckle.txt
$VM_SSH "systemctl is-active knuckle-installer.service 2>/dev/null || \
  ls /usr/bin/knuckle 2>/dev/null || echo 'knuckle not found'" \
  | tee $WORKDIR/iso-verify-service.txt

# Check ISO xorriso partition type (EFI SP GUID, not Basic Data)
xorriso -indev output/knuckle-installer-stable-amd64.iso \
  -find / -name boot 2>&1 | grep -i 'GPT\|EFI' | tee $WORKDIR/iso-partition-type.txt

kill $(cat $WORKDIR/qemu-iso.pid) 2>/dev/null || true
```

**Evidence to collect:**
- Unit test output (100% expected)
- ISO file size (`ls -lh *.iso` — must be > 200MB, < 1.5GB)
- `$WORKDIR/iso-verify-kernel.txt` — must contain a kernel version
- `$WORKDIR/iso-verify-knuckle.txt` — must contain `/usr/bin/knuckle`
- `$WORKDIR/iso-partition-type.txt` — must mention `EFI` (not `Basic Data`)
- `$WORKDIR/serial-iso.log` (attach on failure)

**Pass criteria:**
- Unit tests pass (100% coverage gate)
- ISO builds without error
- ISO live system boots and SSH responds
- `/usr/bin/knuckle` present in live system
- GPT partition type is EFI SP GUID (C12A7328), not Basic Data (EBD0A0A2)

**Estimated time:** 15 min (unit 30s + build 5 min + boot 3 min + verify 1 min)

---

### 13. `ci`

**Test type:** unit-only (lint/validate CI YAML)

**Commands:**
```bash
# actionlint — validates GitHub Actions workflow syntax
# Install if not present: go install github.com/rhysd/actionlint/cmd/actionlint@latest
actionlint .github/workflows/ci.yml .github/workflows/release.yml \
  .github/workflows/security.yml 2>&1 | tee $WORKDIR/actionlint-output.txt

# yamllint (if installed)
yamllint -d relaxed .github/workflows/ 2>&1 | tee $WORKDIR/yamllint-output.txt

# Verify CI runs just ci (the full gate is in the workflow)
grep -c 'just ci' .github/workflows/ci.yml

# Check for the merge_group trigger (required for merge queue)
grep 'merge_group' .github/workflows/ci.yml | tee $WORKDIR/ci-merge-group.txt
```

**Evidence to collect:**
- `$WORKDIR/actionlint-output.txt` — must be empty (no errors)
- `$WORKDIR/ci-merge-group.txt` — must be non-empty (merge_group trigger present)

**Pass criteria:**
- `actionlint` reports zero errors
- `merge_group` trigger present in ci.yml
- All required status check names in ci.yml match the ruleset

**Estimated time:** 30 seconds

---

## Lab Report Template

Save as `$WORKDIR/report.md` at the end of each domain test run.

```markdown
# PR #NNN — `<domain>` Test Report

**Date:** YYYY-MM-DD HH:MM UTC
**PR:** https://github.com/projectbluefin/knuckle/pull/NNN
**Branch:** `<branch-name>`
**Commit:** `<git rev-parse HEAD>`
**Ghost port:** PPPPP
**Tester:** <agent or human>

---

## Summary

| Phase | Result | Notes |
|-------|--------|-------|
| Unit tests | ✅ PASS / ❌ FAIL | coverage: XX% |
| VM smoke / dry-run | ✅ PASS / ❌ FAIL / N/A | |
| Install / boot | ✅ PASS / ❌ FAIL / N/A | elapsed: Xm |
| Verification checks | ✅ PASS / ❌ FAIL / N/A | |

**Overall: ✅ PASS / ❌ FAIL**

---

## Unit Test Output

```
<paste full go test -v output>
```

**Coverage:** XX.X%

---

## VM Evidence

### Install output
```
<paste $WORKDIR/install-output.txt>
```

### knuckle.log (from inside VM)
```
<paste $WORKDIR/knuckle.log, or "N/A">
```

### Verification checks
```
hostname:          <value>    (expected: <expected>)   [PASS/FAIL]
timezone:          <value>    (expected: <expected>)   [PASS/FAIL]
network config:    <value>    (expected: <expected>)   [PASS/FAIL]
update strategy:   <value>    (expected: <expected>)   [PASS/FAIL]
sysext active:     <value>    (expected: <expected>)   [PASS/FAIL]
file permissions:  <value>    (expected: 600)          [PASS/FAIL]
```

---

## Artifacts

- `serial-installer.log`: <attached / not collected>
- `serial-target.log`: <attached / not collected>
- `probe-output.json`: <attached / not collected>
- `generated.ign`: <attached / not collected>

---

## Failures / Notes

<freetext — describe any failures, unexpected output, or deviations>

---

## Time Breakdown

- Started: HH:MM UTC
- Unit tests: Xm Xs
- VM boot: Xm Xs
- Install: Xm Xs
- Verification: Xm Xs
- Cleanup: Xm Xs
- **Total: Xm**
```

---

## Parallel PR Testing Strategy

Multiple PRs can be tested concurrently on ghost because:
1. Each PR gets its own workdir (`/var/tmp/knuckle-test/pr-NNN/`)
2. Each PR gets its own port (`20000 + NNN`)
3. Each PR gets its own qemu.pid file — kills don't cross-contaminate

### Parallel Launch Pattern (from dev machine)

```bash
# Test PR #170 (domain: install) and PR #172 (domain: sysext) in parallel
ssh jorge@192.168.1.102 "cd ~/src/knuckle && \
  nohup bash scripts/pr-test.sh 170 install > /var/tmp/knuckle-test/pr-170/runner.log 2>&1 &
  nohup bash scripts/pr-test.sh 172 sysext  > /var/tmp/knuckle-test/pr-172/runner.log 2>&1 &
  echo 'launched pr-170 and pr-172'
  wait"
```

### Concurrency Limits

ghost has 32 cores and 64GB RAM. Each VM uses `-m 4096` (4GB). Safe ceiling:

| Scenario | RAM used | Concurrent VMs |
|----------|----------|----------------|
| 4 VMs × 4GB | 16 GB | ✅ comfortable |
| 8 VMs × 4GB | 32 GB | ✅ safe |
| 12 VMs × 4GB | 48 GB | ⚠ watch swap |

**Practical limit: 6 concurrent PRs under test.**

During a sysext or install pass (15–25 min each), each VM does heavy disk I/O.
Limit full-vm-install domains to 3 concurrent. Unit-only domains are free.

### Port Collision Check

Before launching a PR test:
```bash
ss -tln | grep ":$(echo "20000 + NNN" | bc)"
# If port in use, the previous run wasn't cleaned up — kill it first
```

---

## Cleanup Strategy

### After Each Test Run (automatic)

Each domain test script traps EXIT and runs:
```bash
cleanup() {
  for pidfile in $WORKDIR/qemu-*.pid; do
    [ -f "$pidfile" ] || continue
    kill "$(cat $pidfile)" 2>/dev/null || true
    rm -f "$pidfile"
  done
}
trap cleanup EXIT
```

### Manual Cleanup (after reviewing report)

```bash
# Kill any leftover VMs for a specific PR
PR=170
WORKDIR=/var/tmp/knuckle-test/pr-${PR}
for f in $WORKDIR/qemu-*.pid; do
  kill "$(cat $f)" 2>/dev/null || true
  rm -f "$f"
done

# Remove all ephemeral disk images (keep report.md and logs)
rm -f $WORKDIR/*.qcow2 $WORKDIR/e2e_key $WORKDIR/e2e_key.pub
```

### Full Workdir Cleanup (after PR merged/closed)

```bash
rm -rf /var/tmp/knuckle-test/pr-170/
```

### Base Image — NEVER delete

```bash
# This file is shared by ALL PR tests as a CoW backing store.
# Deleting it while any VM is running using it as backing → instant corruption.
/var/tmp/knuckle-test/flatcar_base_amd64.img   # DO NOT DELETE
```

To update the base image safely:
1. Verify no VMs running: `pgrep -a qemu-system-x86_64`
2. Delete old image: `rm /var/tmp/knuckle-test/flatcar_base_amd64.img`
3. Re-download: `curl -L ... | bunzip2 > flatcar_base_amd64.img`

---

## Domain → Test Level Cheat Sheet

| Domain | Type | VM needed | Time | CI equivalent |
|--------|------|-----------|------|---------------|
| `validate` | unit-only | ❌ | 5s | `go test ./internal/validate/...` |
| `probe` | unit + VM-binary | ✅ installer VM | 3m | `go test ./internal/probe/...` |
| `ignition` | unit + VM-binary | ✅ ignition-boot VM | 8m | `go test ./internal/ignition/...` |
| `headless` | unit + headless-install | ✅ installer VM | 15m | `just headless-test` |
| `install` | unit + full-vm-install | ✅ installer + target | 20m | `just vm-e2e` (DHCP pass) |
| `wizard` | unit + headless dry-run | ❌ | 3m | `go test ./internal/wizard/...` |
| `bakery` | unit-only (+ integration) | ❌ | 30s | `go test ./internal/bakery/...` |
| `github` | unit-only (+ integration) | ❌ | 15s | `go test ./internal/github/...` |
| `sysext` | unit + full-vm-install | ✅ installer + target | 25m | `just vm-e2e` (sysext pass) |
| `security` | unit + VM-binary | ✅ installer VM | 5m | `go test ./internal/install/... ./internal/ignition/...` |
| `tui` | unit + VM-binary smoke | ✅ installer VM | 8m | `go test ./internal/tui/...` |
| `iso` | unit + VM-binary | ✅ ISO live VM | 15m | `just iso` |
| `ci` | lint/yaml-only | ❌ | 30s | `actionlint` |

---

## Known Gaps (as of 2026-05-22)

1. **TUI interactive verification** — the VM-binary smoke test only checks for
   panics. Actual form rendering, navigation, and input are untested
   automatically. Requires human + terminal per `knuckle-testlab` skill.

2. **Multi-user config** — no VM test for 2-user Ignition (both users SSH in
   with separate keys). Unit tests exist but not end-to-end verified.

3. **Password-only auth** — no VM test for password login (no SSH key path).
   Would require expect/tmux scripting inside VM.

4. **ARM64** — all ghost tests above are amd64. ARM64 QEMU tests require native
   arm64 hardware or nested virtualization (TCG only, very slow).

5. **ISO headless install** — `hardware-repro` recipe proves ISO boots and knuckle
   launches, but doesn't exercise full install via ISO path end-to-end.

6. **`--probe-dump` flag** — doesn't exist yet. Probe VM verification currently
   uses raw `lsblk -J` rather than knuckle's own parsing output.

7. **NVIDIA VM pass** — currently only verifies `enabled-sysext.conf` config
   (kernel driver config). Doesn't verify actual NVIDIA kernel module loading
   because QEMU doesn't expose NVIDIA PCI devices.
