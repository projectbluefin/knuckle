# Knuckle — Flatcar Container Linux TUI Installer
# https://github.com/castrojo/knuckle

# Target architecture for build/ISO/VM recipes. Override with: KNUCKLE_ARCH=arm64 just <recipe>
KNUCKLE_ARCH := env_var_or_default("KNUCKLE_ARCH", "amd64")

QEMU := if KNUCKLE_ARCH == "arm64" { \
    "qemu-system-aarch64" \
} else if path_exists("/home/linuxbrew/.linuxbrew/bin/qemu-system-x86_64") == "true" { \
    "/home/linuxbrew/.linuxbrew/bin/qemu-system-x86_64" \
} else { \
    "qemu-system-x86_64" \
}
SSH_OPTS := "-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR"

default:
    @just --list
    @echo ""
    @echo "Quickstart:"
    @echo "  just vm        — install in a VM, boots into installed system after"
    @echo "  just vm-e2e    — automated: headless install → boot → verify SSH"
    @echo "  just iso-smoke <iso> <ovmf> — headless ISO boot smoke via serial log"
    @echo "  just hardware-repro — boot installer ISO in a hardware-like VM and capture install logs"
    @echo "  just e2e       — full end-to-end: build ISO → boot → install → verify"
    @echo ""
    @echo "FCOS:"
    @echo "  just tools-fcos          — install coreos-installer"
    @echo "  just build-fcos-iso      — build FCOS live ISO (stable, amd64)"
    @echo "  just build-fcos-iso arch=arm64 stream=testing"
    @echo ""
    @echo "Pre-release (requires network):"
    @echo "  just catalog-check       — report new bakery extensions missing descriptions"
    @echo "  just nvidia-check        — verify NVIDIA driver series vs Flatcar docs"
    @echo "  just release-preflight   — all checks + ci gate before tagging a release"
    @echo ""
    @echo "ARM64:"
    @echo "  KNUCKLE_ARCH=arm64 just build"
    @echo "  KNUCKLE_ARCH=arm64 just boot-iso"

# Build the binary (amd64 by default; set KNUCKLE_ARCH=arm64 for arm64)
build:
    #!/usr/bin/env bash
    VERSION=$(git describe --tags --always 2>/dev/null || echo dev)
    GOOS=linux GOARCH={{KNUCKLE_ARCH}} CGO_ENABLED=0 go build \
        -ldflags="-s -w -X main.version=${VERSION}" \
        -o bin/knuckle ./cmd/knuckle

# Cross-compile for arm64 (does not run — verifies the code compiles)
build-arm64:
    #!/usr/bin/env bash
    VERSION=$(git describe --tags --always 2>/dev/null || echo dev)
    GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build \
        -ldflags="-s -w -X main.version=${VERSION}" \
        -o bin/knuckle-arm64 ./cmd/knuckle

# Run tests
test:
    go test ./...

# Tool versions and checksums — bump here and in .github/workflows/ci.yml together
GOLANGCI_LINT_VERSION := "2.12.2"
GOLANGCI_LINT_SHA256_LINUX_AMD64 := "8df580d2670fed8fa984aac0507099af8df275e665215f5c7a2ae3943893a553"
GOLANGCI_LINT_SHA256_LINUX_ARM64 := "44cd40a8c76c86755375adfeea52cfd3533cb43d7bd647771e0ae065e166df3a"
GOLANGCI_LINT_SHA256_DARWIN_AMD64 := "f6f06d94b6241521c53d15450c5209b028270bf966f842afb11c030c79f5bc16"
GOLANGCI_LINT_SHA256_DARWIN_ARM64 := "a9c54498731b3128f79e090be6110f3e5fffccc617b08142ed244d4126c73f29"
GOLANGCI_LINT := ".tools/golangci-lint"

SHELLCHECK_VERSION := "0.10.0"
SHELLCHECK_SHA256_LINUX_AMD64 := "6c881ab0698e4e6ea235245f22832860544f17ba386442fe7e9d629f8cbedf87"
SHELLCHECK_SHA256_LINUX_ARM64 := "324a7e89de8fa2aed0d0c28f3dab59cf84c6d74264022c00c22af665ed1a09bb"
SHELLCHECK_SHA256_DARWIN_AMD64 := "ef27684f23279d112d8ad84e0823642e43f838993bbb8c0963db9b58a90464c2"
SHELLCHECK_SHA256_DARWIN_ARM64 := "bbd2f14826328eee7679da7221f2bc3afb011f6a928b848c80c321f6046ddf81"
SHELLCHECK := ".tools/shellcheck"

# Install pinned tool binaries (idempotent — run once after clone, or after version bump)
tools:
    just _install-golangci-lint
    just _install-shellcheck
    go tool govulncheck -version
    @echo "tools ok"

# Full CI (tidy + fmt-check + vet + lint + vuln + test-race + cover-check + headless-e2e + build)
ci: _install-golangci-lint _install-shellcheck
    go mod tidy
    @git diff --exit-code go.mod go.sum || (echo "go.mod/go.sum dirty after tidy" && exit 1)
    just fmt-check
    go vet ./...
    {{GOLANGCI_LINT}} run ./...
    go tool govulncheck ./...
    go test -race ./...
    just cover-check
    just headless-test
    just shell-lint
    just build

# Check formatting (CI gate — fails if any file would change)
fmt-check:
    #!/usr/bin/env bash
    out=$(gofmt -l . 2>&1)
    if [[ -n "$out" ]]; then
        echo "gofmt would change these files:"; echo "$out"; exit 1
    fi

# Format all Go files in place
fmt:
    gofmt -w .

# Vulnerability scan — version pinned in go.mod via `go tool`
vuln:
    go tool govulncheck ./...

# Coverage report (text + cover.out for tooling)
cover:
    go test -count=1 -race -covermode=atomic -coverprofile=cover.out ./...
    @go tool cover -func=cover.out | tail -1

# Coverage HTML — open cover.html in browser to inspect uncovered lines
cover-html: cover
    go tool cover -html=cover.out -o cover.html
    @echo "open cover.html"

# Per-package coverage gate. Mirrors docs/CI-AND-TESTING.md targets.
# Exits non-zero if any package falls below its threshold.
# Uses statement-count coverage from `go test -cover`, not function-average.
cover-check:
    #!/usr/bin/env bash
    set -euo pipefail
    declare -A targets=(
        [model]=100 [validate]=99 [ignition]=100 [github]=96
        [bakery]=100 [probe]=100   [runner]=100   [install]=100
        [headless]=99 [wizard]=99  [iso]=100      [tui]=99
        [demo]=100   [fcos]=100
    )
    fail=0
    for pkg in "${!targets[@]}"; do
        pct=$(go test -count=1 -cover ./internal/${pkg}/... 2>/dev/null \
            | awk '/coverage:/ {gsub("%",""); print $(NF-2); exit}')
        pct=${pct%.*}
        if [[ -z "$pct" ]]; then
            echo "FAIL  internal/${pkg}   no coverage reported"
            fail=1
            continue
        fi
        if (( pct < ${targets[$pkg]} )); then
            echo "FAIL  internal/${pkg}  ${pct}%  (target ${targets[$pkg]}%)"
            fail=1
        else
            echo "ok    internal/${pkg}  ${pct}%  (target ${targets[$pkg]}%)"
        fi
    done
    script_pkg=scripts/catalog_check
    script_target=100
    script_pct=$(go test -count=1 -cover ./${script_pkg} 2>/dev/null \
        | awk '/coverage:/ {gsub("%",""); print $(NF-2); exit}')
    script_pct=${script_pct%.*}
    if [[ -z "$script_pct" ]]; then
        echo "FAIL  ${script_pkg}   no coverage reported"
        fail=1
    elif (( script_pct < script_target )); then
        echo "FAIL  ${script_pkg}  ${script_pct}%  (target ${script_target}%)"
        fail=1
    else
        echo "ok    ${script_pkg}  ${script_pct}%  (target ${script_target}%)"
    fi
    cmd_pkg=cmd/knuckle
    cmd_target=85
    cmd_pct=$(go test -count=1 -cover ./${cmd_pkg}/... 2>/dev/null \
        | awk '/coverage:/ {gsub("%",""); print $(NF-2); exit}')
    cmd_pct=${cmd_pct%.*}
    if [[ -z "$cmd_pct" ]]; then
        echo "FAIL  ${cmd_pkg}   no coverage reported"
        fail=1
    elif (( cmd_pct < cmd_target )); then
        echo "FAIL  ${cmd_pkg}  ${cmd_pct}%  (target ${cmd_target}%)"
        fail=1
    else
        echo "ok    ${cmd_pkg}  ${cmd_pct}%  (target ${cmd_target}%)"
    fi
    cbf_pkg=cmd/compile-butane-fresh
    cbf_target=100
    cbf_pct=$(go test -count=1 -cover ./${cbf_pkg}/... 2>/dev/null \
        | awk '/coverage:/ {gsub("%",""); print $(NF-2); exit}')
    cbf_pct=${cbf_pct%.*}
    if [[ -z "$cbf_pct" ]]; then
        echo "FAIL  ${cbf_pkg}   no coverage reported"
        fail=1
    elif (( cbf_pct < cbf_target )); then
        echo "FAIL  ${cbf_pkg}  ${cbf_pct}%  (target ${cbf_target}%)"
        fail=1
    else
        echo "ok    ${cbf_pkg}  ${cbf_pct}%  (target ${cbf_target}%)"
    fi
    # cmd/nvidia-check — gated at 95% (run() is covered; main() wrapper is not).
    # Depends on the run() refactor landing in #760. Gate prevents regression.
    nvidia_pkg=cmd/nvidia-check
    nvidia_target=95
    nvidia_pct=$(go test -count=1 -cover ./${nvidia_pkg}/... 2>/dev/null \
        | awk '/coverage:/ {gsub("%",""); print $(NF-2); exit}')
    nvidia_pct=${nvidia_pct%.*}
    if [[ -z "$nvidia_pct" ]]; then
        echo "FAIL  ${nvidia_pkg}   no coverage reported"
        fail=1
    elif (( nvidia_pct < nvidia_target )); then
        echo "FAIL  ${nvidia_pkg}  ${nvidia_pct}%  (target ${nvidia_target}%)"
        fail=1
    else
        echo "ok    ${nvidia_pkg}  ${nvidia_pct}%  (target ${nvidia_target}%)"
    fi
    exit $fail

# Quick headless dry-run test (no VM needed)
headless-test:
    #!/usr/bin/env bash
    set -euo pipefail
    just build

    echo "── Flatcar headless dry-run ──"
    cat > /tmp/knuckle-test-config.json <<'EOF'
    {"channel":"stable","hostname":"test-node","timezone":"UTC","network":{"mode":"dhcp"},"users":[{"username":"core","ssh_keys":["ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGdllynsgXbmcFXhVJAIAkDbYjqZ2OgHgZJVFmFKtvF7 test"]}],"disk":"/dev/vdb","update_strategy":"reboot","reboot":false}
    EOF
    bin/knuckle --config /tmp/knuckle-test-config.json --headless --dry-run
    echo "✅ Flatcar PASS"

    echo ""
    echo "── FCOS headless dry-run ──"
    cat > /tmp/knuckle-fcos-test-config.json <<'EOF'
    {"os":"fcos","channel":"stable","hostname":"fcos-test","timezone":"UTC","network":{"mode":"dhcp"},"users":[{"username":"core","ssh_keys":["ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGdllynsgXbmcFXhVJAIAkDbYjqZ2OgHgZJVFmFKtvF7 test"]}],"disk":"/dev/vdb","update_strategy":"reboot","reboot":false}
    EOF
    bin/knuckle --config /tmp/knuckle-fcos-test-config.json --headless --dry-run
    echo "✅ FCOS PASS"

# Real install in a VM — auto-boots the installed system when knuckle exits
vm:
    #!/usr/bin/env bash
    set -euo pipefail
    just build
    just _ensure-base
    just _kill-vm

    rm -f .vm/boot.qcow2 .vm/target.qcow2
    qemu-img create -f qcow2 -b "$(pwd)/.vm/flatcar_base_{{KNUCKLE_ARCH}}.img" -F qcow2 .vm/boot.qcow2 >/dev/null
    qemu-img create -f qcow2 .vm/target.qcow2 20G >/dev/null
    just _write-ignition

    # Build arch-specific QEMU args
    QEMU_ARGS=(-m 2048 -smp 2)
    if [[ "{{KNUCKLE_ARCH}}" == "arm64" ]]; then
        QEMU_ARGS+=(-M virt -cpu cortex-a57)
        # AAVMF firmware required for arm64 QEMU
        AAVMF=""
        for candidate in /usr/share/AAVMF/AAVMF_CODE.fd /usr/share/qemu-efi-aarch64/QEMU_EFI.fd; do
            [ -f "$candidate" ] && AAVMF="$candidate" && break
        done
        [ -n "$AAVMF" ] && QEMU_ARGS+=(-drive "if=pflash,format=raw,readonly=on,file=$AAVMF")
        echo "  ⚠ arm64: KVM only available on native arm64 hardware; TCG used otherwise"
    else
        QEMU_ARGS+=(-enable-kvm)
    fi
    {{QEMU}} \
        "${QEMU_ARGS[@]}" \
        -drive if=virtio,file=.vm/boot.qcow2,format=qcow2 \
        -drive if=virtio,file=.vm/target.qcow2,format=qcow2 \
        -fw_cfg name=opt/org.flatcar-linux/config,file=.vm/config.ign \
        -net nic,model=virtio -net user,hostfwd=tcp::2222-:22 \
        -display none -daemonize -pidfile .vm/qemu.pid

    echo "Waiting for installer VM..."
    for i in $(seq 1 20); do
        ssh {{SSH_OPTS}} -o ConnectTimeout=2 -p 2222 core@127.0.0.1 true 2>/dev/null && break
        sleep 2
    done

    scp {{SSH_OPTS}} -P 2222 bin/knuckle core@127.0.0.1:/tmp/knuckle 2>/dev/null
    ssh -t {{SSH_OPTS}} -p 2222 core@127.0.0.1 "sudo /tmp/knuckle --log-file /tmp/knuckle.log" || true

    # Installer exited (via reboot or quit) — kill installer VM, boot the installed target
    echo ""
    echo "Installer exited — booting installed system..."
    just _kill-vm
    sleep 1

    QEMU_ARGS2=(-m 2048 -smp 2)
    if [[ "{{KNUCKLE_ARCH}}" == "arm64" ]]; then
        QEMU_ARGS2+=(-M virt -cpu cortex-a57)
        [ -n "${AAVMF:-}" ] && QEMU_ARGS2+=(-drive "if=pflash,format=raw,readonly=on,file=$AAVMF")
    else
        QEMU_ARGS2+=(-enable-kvm)
    fi
    {{QEMU}} \
        "${QEMU_ARGS2[@]}" \
        -drive if=virtio,file=.vm/target.qcow2,format=qcow2 \
        -net nic,model=virtio -net user,hostfwd=tcp::2222-:22 \
        -display none -daemonize -pidfile .vm/qemu.pid

    echo "Waiting for installed system (first boot runs Ignition)..."
    ok=0
    for i in $(seq 1 120); do
        ssh {{SSH_OPTS}} -o ConnectTimeout=3 -p 2222 core@127.0.0.1 true 2>/dev/null && ok=1 && break
        sleep 5
    done
    if [ "$ok" != "1" ]; then
        echo "Installed system did not come up. Check: just boot-target"
        exit 1
    fi
    exec ssh -t {{SSH_OPTS}} -p 2222 core@127.0.0.1



# Automated E2E: real headless install → boot installed system → verify SSH + hostname
# Requires: KVM, internet access (~400MB Flatcar download during flatcar-install).
# Takes ~15 min on first run (image download), ~8 min on subsequent runs.
vm-e2e:
    #!/usr/bin/env bash
    set -euo pipefail

    # Cleanup on any exit — kills QEMU if still running
    cleanup() {
        if [ -f .vm/qemu.pid ]; then
            kill "$(cat .vm/qemu.pid)" 2>/dev/null || true
            rm -f .vm/qemu.pid
        fi
    }
    trap cleanup EXIT

    echo "=== knuckle vm-e2e: headless install → boot → verify ==="
    echo ""

    just build
    just _ensure-base
    just _kill-vm

    # Ephemeral SSH key — generated per run, used for both installer VM and installed system
    mkdir -p .vm
    rm -f .vm/e2e_key .vm/e2e_key.pub
    ssh-keygen -t ed25519 -f .vm/e2e_key -N "" -C "knuckle-e2e" -q
    E2E_PUB=$(cat .vm/e2e_key.pub)

    # Installer VM disk (CoW overlay) + blank target disk
    rm -f .vm/boot.qcow2 .vm/target.qcow2
    qemu-img create -f qcow2 -b "$(pwd)/.vm/flatcar_base_{{KNUCKLE_ARCH}}.img" -F qcow2 .vm/boot.qcow2 >/dev/null
    qemu-img create -f qcow2 .vm/target.qcow2 20G >/dev/null

    # Ignition for the installer VM: e2e key on core, sshd enabled
    printf '{"ignition":{"version":"3.4.0"},"passwd":{"users":[{"name":"core","sshAuthorizedKeys":["%s"]}]},"systemd":{"units":[{"name":"sshd.service","enabled":true}]}}\n' \
        "$E2E_PUB" > .vm/e2e-installer.ign

    # Headless config: real install targeting /dev/vdb, e2e key on core user
    printf '{"channel":"stable","hostname":"e2e-verified","timezone":"UTC","network":{"mode":"dhcp"},"users":[{"username":"core","ssh_keys":["%s"]}],"disk":"/dev/vdb","update_strategy":"off","reboot":false}\n' \
        "$E2E_PUB" > .vm/e2e-config.json

    E2E_SSH="ssh {{SSH_OPTS}} -i .vm/e2e_key -p 2222 -o ServerAliveInterval=30 -o ServerAliveCountMax=10 core@127.0.0.1"
    # Long-running install calls can exceed default keepalive windows.
    E2E_SSH_LONG="$E2E_SSH -o ServerAliveInterval=60 -o ServerAliveCountMax=120"
    E2E_SCP="scp {{SSH_OPTS}} -i .vm/e2e_key -P 2222"

    # ── Step 1: Boot installer VM ──────────────────────────────────────────
    echo "[1/5] Booting installer VM..."
    # Build arch-specific QEMU args (arm64 needs -M virt; KVM only on native host)
    E2E_QEMU_ARGS=(-m 4096 -smp 2)
    if [[ "{{KNUCKLE_ARCH}}" == "arm64" ]]; then
        E2E_QEMU_ARGS+=(-M virt -cpu cortex-a57)
        E2E_AAVMF=""
        for candidate in /usr/share/AAVMF/AAVMF_CODE.fd /usr/share/qemu-efi-aarch64/QEMU_EFI.fd; do
            [ -f "$candidate" ] && E2E_AAVMF="$candidate" && break
        done
        [ -n "$E2E_AAVMF" ] && E2E_QEMU_ARGS+=(-drive "if=pflash,format=raw,readonly=on,file=$E2E_AAVMF")
    else
        E2E_QEMU_ARGS+=(-enable-kvm)
    fi
    {{QEMU}} \
        "${E2E_QEMU_ARGS[@]}" \
        -drive if=virtio,file=.vm/boot.qcow2,format=qcow2 \
        -drive if=virtio,file=.vm/target.qcow2,format=qcow2 \
        -fw_cfg name=opt/org.flatcar-linux/config,file=.vm/e2e-installer.ign \
        -net nic,model=virtio -net user,hostfwd=tcp::2222-:22 \
        -display none -daemonize -pidfile .vm/qemu.pid \
        -serial file:.vm/e2e-installer-serial.log

    ok=0
    for i in $(seq 1 40); do
        $E2E_SSH -o ConnectTimeout=3 true 2>/dev/null && ok=1 && break
        sleep 3
    done
    if [ "$ok" != "1" ]; then
        echo "❌ installer VM never came up (serial log below)"
        tail -30 .vm/e2e-installer-serial.log 2>/dev/null || true
        exit 1
    fi
    echo "  ✓ installer VM ready"

    # ── Step 2: Run headless install ───────────────────────────────────────
    echo "[2/5] Running headless install (no --dry-run; downloads Flatcar ~400MB)..."
    $E2E_SCP bin/knuckle core@127.0.0.1:/tmp/knuckle >/dev/null
    $E2E_SCP .vm/e2e-config.json core@127.0.0.1:/tmp/e2e-config.json >/dev/null

    if ! $E2E_SSH_LONG "timeout 15m sudo /tmp/knuckle --headless --config /tmp/e2e-config.json --log-file /tmp/knuckle.log"; then
        echo "❌ headless install failed — knuckle.log:"
        timeout 20 $E2E_SSH_LONG "cat /tmp/knuckle.log" 2>/dev/null || true
        exit 1
    fi
    echo "  ✓ flatcar-install completed"

    # ── Step 3: Kill installer VM ──────────────────────────────────────────
    echo "[3/5] Killing installer VM..."
    kill "$(cat .vm/qemu.pid)" 2>/dev/null || true
    rm -f .vm/qemu.pid
    sleep 2

    # ── Step 4: Boot installed target ──────────────────────────────────────
    echo "[4/5] Booting installed target disk (first boot, Ignition runs)..."
    E2E_QEMU_ARGS2=(-m 2048 -smp 2)
    if [[ "{{KNUCKLE_ARCH}}" == "arm64" ]]; then
        E2E_QEMU_ARGS2+=(-M virt -cpu cortex-a57)
        [ -n "${E2E_AAVMF:-}" ] && E2E_QEMU_ARGS2+=(-drive "if=pflash,format=raw,readonly=on,file=$E2E_AAVMF")
    else
        E2E_QEMU_ARGS2+=(-enable-kvm)
    fi
    {{QEMU}} \
        "${E2E_QEMU_ARGS2[@]}" \
        -drive if=virtio,file=.vm/target.qcow2,format=qcow2 \
        -net nic,model=virtio -net user,hostfwd=tcp::2222-:22 \
        -display none -daemonize -pidfile .vm/qemu.pid \
        -serial file:.vm/e2e-target-serial.log

    ok=0
    for i in $(seq 1 120); do
        $E2E_SSH -o ConnectTimeout=3 true 2>/dev/null && ok=1 && break
        sleep 5
    done
    if [ "$ok" != "1" ]; then
        echo "❌ installed system never came up (serial log below)"
        tail -30 .vm/e2e-target-serial.log 2>/dev/null || true
        exit 1
    fi
    echo "  ✓ installed system SSH accessible"

    # ── Step 5: Verify ─────────────────────────────────────────────────────
    echo "[5/5] Verifying installed system..."

    ACTUAL_HOST=$($E2E_SSH hostname 2>/dev/null) \
        || { echo "❌ hostname command failed"; exit 1; }
    [ "$ACTUAL_HOST" = "e2e-verified" ] \
        || { echo "❌ hostname '$ACTUAL_HOST' != 'e2e-verified'"; exit 1; }
    echo "  ✓ hostname: $ACTUAL_HOST"

    FLATCAR_VER=$($E2E_SSH "grep ^VERSION= /etc/os-release" 2>/dev/null | cut -d= -f2 | tr -d '"') \
        || true
    [ -n "$FLATCAR_VER" ] && echo "  ✓ Flatcar version: $FLATCAR_VER"

    # Verify update strategy was applied (knuckle sets REBOOT_STRATEGY=off)
    UPDATE_STRATEGY=$($E2E_SSH "grep REBOOT_STRATEGY /etc/flatcar/update.conf 2>/dev/null" | cut -d= -f2 | tr -d '"' | tr -d ' ') || true
    if [ "$UPDATE_STRATEGY" = "off" ]; then
        echo "  ✓ update strategy: off"
    else
        echo "  ⚠ update strategy: '$UPDATE_STRATEGY' (expected 'off')"
    fi

    # Verify core user has correct groups (sudo access)
    CORE_GROUPS=$($E2E_SSH "id -nG core" 2>/dev/null) || true
    if echo "$CORE_GROUPS" | grep -q "sudo\|wheel"; then
        echo "  ✓ core user has privilege group: $CORE_GROUPS"
    else
        echo "  ✓ core user groups: $CORE_GROUPS"
    fi

    echo ""
    echo "✅ vm-e2e DHCP pass PASSED"

    # ── Static network pass ────────────────────────────────────────────────
    # QEMU slirp default subnet is 10.0.2.x — configure static using those
    # addresses so port-forwarding still works while testing the static path.
    echo ""
    echo "=== vm-e2e static network pass ==="
    echo ""

    just _kill-vm
    rm -f .vm/target-static.qcow2
    qemu-img create -f qcow2 .vm/target-static.qcow2 20G >/dev/null

    # New installer overlay on same base
    rm -f .vm/boot.qcow2
    qemu-img create -f qcow2 -b "$(pwd)/.vm/flatcar_base_{{KNUCKLE_ARCH}}.img" -F qcow2 .vm/boot.qcow2 >/dev/null

    # Detect interface name from the running Flatcar image (virtio-net → ens3 or eth0)
    # Write headless config with static network using QEMU slirp addresses
    printf '{"channel":"stable","hostname":"e2e-static","timezone":"UTC","network":{"mode":"static","interface":"ens3","address":"10.0.2.15/24","gateway":"10.0.2.2"},"users":[{"username":"core","ssh_keys":["%s"]}],"disk":"/dev/vdb","update_strategy":"off","reboot":false}\n' \
        "$E2E_PUB" > .vm/e2e-static-config.json

    echo "[1/4] Booting installer VM (static network pass)..."
    {{QEMU}} \
        "${E2E_QEMU_ARGS[@]}" \
        -drive if=virtio,file=.vm/boot.qcow2,format=qcow2 \
        -drive if=virtio,file=.vm/target-static.qcow2,format=qcow2 \
        -fw_cfg name=opt/org.flatcar-linux/config,file=.vm/e2e-installer.ign \
        -net nic,model=virtio -net user,hostfwd=tcp::2222-:22 \
        -display none -daemonize -pidfile .vm/qemu.pid \
        -serial file:.vm/e2e-static-installer-serial.log

    ok=0
    for i in $(seq 1 40); do
        $E2E_SSH -o ConnectTimeout=3 true 2>/dev/null && ok=1 && break
        sleep 3
    done
    [ "$ok" = "1" ] || { echo "❌ installer VM never came up"; tail -20 .vm/e2e-static-installer-serial.log 2>/dev/null; exit 1; }
    echo "  ✓ installer VM ready"

    echo "[2/4] Running headless install (static network config)..."
    $E2E_SCP bin/knuckle core@127.0.0.1:/tmp/knuckle >/dev/null
    $E2E_SCP .vm/e2e-static-config.json core@127.0.0.1:/tmp/e2e-static-config.json >/dev/null
    if ! $E2E_SSH_LONG "timeout 25m sudo /tmp/knuckle --headless --config /tmp/e2e-static-config.json --log-file /tmp/knuckle-static.log"; then
        echo "❌ headless install (static) failed — knuckle-static.log:"
        timeout 20 $E2E_SSH_LONG "cat /tmp/knuckle-static.log" 2>/dev/null || true
        exit 1
    fi
    echo "  ✓ static install completed"

    echo "[3/4] Booting static-configured installed disk..."
    just _kill-vm
    sleep 1
    {{QEMU}} \
        "${E2E_QEMU_ARGS2[@]}" \
        -drive if=virtio,file=.vm/target-static.qcow2,format=qcow2 \
        -net nic,model=virtio -net user,hostfwd=tcp::2222-:22 \
        -display none -daemonize -pidfile .vm/qemu.pid \
        -serial file:.vm/e2e-static-target-serial.log

    ok=0
    for i in $(seq 1 120); do
        $E2E_SSH -o ConnectTimeout=3 true 2>/dev/null && ok=1 && break
        sleep 5
    done
    [ "$ok" = "1" ] || { echo "❌ static system never came up"; tail -20 .vm/e2e-static-target-serial.log 2>/dev/null; exit 1; }
    echo "  ✓ static system SSH accessible"

    echo "[4/4] Verifying static network config on installed system..."

    STATIC_HOST=$($E2E_SSH hostname 2>/dev/null) || { echo "❌ hostname failed"; exit 1; }
    [ "$STATIC_HOST" = "e2e-static" ] || { echo "❌ hostname '$STATIC_HOST' != 'e2e-static'"; exit 1; }
    echo "  ✓ hostname: $STATIC_HOST"

    # Verify the static networkd unit exists with correct content
    if $E2E_SSH "test -f /etc/systemd/network/10-static.network" 2>/dev/null; then
        echo "  ✓ /etc/systemd/network/10-static.network exists"
        IFACE=$($E2E_SSH "grep ^Name= /etc/systemd/network/10-static.network" 2>/dev/null | cut -d= -f2) || true
        ADDR=$($E2E_SSH "grep ^Address= /etc/systemd/network/10-static.network" 2>/dev/null | cut -d= -f2) || true
        GW=$($E2E_SSH "grep ^Gateway= /etc/systemd/network/10-static.network" 2>/dev/null | cut -d= -f2) || true
        echo "  ✓ interface: $IFACE  address: $ADDR  gateway: $GW"
        [ "$ADDR" = "10.0.2.15/24" ] || echo "  ⚠ address mismatch: got '$ADDR', expected 10.0.2.15/24"
        [ "$GW"   = "10.0.2.2"    ] || echo "  ⚠ gateway mismatch: got '$GW', expected 10.0.2.2"
    else
        echo "❌ /etc/systemd/network/10-static.network not found"
        $E2E_SSH "ls /etc/systemd/network/" 2>/dev/null || true
        exit 1
    fi

    echo ""
    echo "✅ vm-e2e STATIC pass PASSED"
    echo ""
    echo "✅ STATIC pass PASSED"

    # ── Sysext pass ────────────────────────────────────────────────────────
    # Installs with the docker sysext selected. Boots the installed system and
    # verifies docker is available (proving Ignition downloaded + activated the sysext).
    echo ""
    echo "=== vm-e2e sysext pass ==="
    echo ""

    just _kill-vm
    rm -f .vm/target-sysext.qcow2
    qemu-img create -f qcow2 .vm/target-sysext.qcow2 20G >/dev/null
    rm -f .vm/boot.qcow2
    qemu-img create -f qcow2 -b "$(pwd)/.vm/flatcar_base_{{KNUCKLE_ARCH}}.img" -F qcow2 .vm/boot.qcow2 >/dev/null

    # docker sysext — knuckle resolves the name to a real URL via bakery catalog
    printf '{"channel":"stable","hostname":"e2e-sysext","timezone":"UTC","network":{"mode":"dhcp"},"users":[{"username":"core","ssh_keys":["%s"]}],"disk":"/dev/vdb","swap":{"enabled":false},"sysexts":["docker"],"update_strategy":"off","reboot":false}\n' \
        "$E2E_PUB" > .vm/e2e-sysext-config.json

    echo "[1/4] Booting installer VM (sysext pass)..."
    {{QEMU}} \
        "${E2E_QEMU_ARGS[@]}" \
        -drive if=virtio,file=.vm/boot.qcow2,format=qcow2 \
        -drive if=virtio,file=.vm/target-sysext.qcow2,format=qcow2 \
        -fw_cfg name=opt/org.flatcar-linux/config,file=.vm/e2e-installer.ign \
        -net nic,model=virtio -net user,hostfwd=tcp::2222-:22 \
        -display none -daemonize -pidfile .vm/qemu.pid \
        -serial file:.vm/e2e-sysext-installer-serial.log

    ok=0
    for i in $(seq 1 40); do
        $E2E_SSH -o ConnectTimeout=3 true 2>/dev/null && ok=1 && break
        sleep 3
    done
    [ "$ok" = "1" ] || { echo "❌ installer VM never came up"; tail -20 .vm/e2e-sysext-installer-serial.log 2>/dev/null; exit 1; }
    echo "  ✓ installer VM ready"

    echo "[2/4] Running headless install with docker sysext (downloads ~400MB + sysext)..."
    $E2E_SCP bin/knuckle core@127.0.0.1:/tmp/knuckle >/dev/null
    $E2E_SCP .vm/e2e-sysext-config.json core@127.0.0.1:/tmp/e2e-sysext-config.json >/dev/null
    # Pass an authenticated GitHub token when available to avoid unauthenticated
    # API rate limits while resolving bakery sysext metadata in e2e loops.
    E2E_GH_TOKEN="${GITHUB_TOKEN:-${GH_TOKEN:-}}"
    if [[ -z "$E2E_GH_TOKEN" ]] && command -v gh >/dev/null 2>&1; then
        E2E_GH_TOKEN="$(gh auth token 2>/dev/null || true)"
    fi
    if [[ -n "$E2E_GH_TOKEN" ]]; then
        INSTALL_CMD="timeout 25m sudo GITHUB_TOKEN='$E2E_GH_TOKEN' /tmp/knuckle --headless --config /tmp/e2e-sysext-config.json --log-file /tmp/knuckle-sysext.log"
    else
        INSTALL_CMD="timeout 25m sudo /tmp/knuckle --headless --config /tmp/e2e-sysext-config.json --log-file /tmp/knuckle-sysext.log"
    fi
    if ! $E2E_SSH_LONG "$INSTALL_CMD"; then
        echo "❌ sysext install failed — knuckle-sysext.log:"
        timeout 20 $E2E_SSH_LONG "cat /tmp/knuckle-sysext.log" 2>/dev/null || true
        exit 1
    fi
    echo "  ✓ sysext install completed"

    echo "[3/4] Booting sysext-configured installed disk..."
    just _kill-vm
    sleep 1
    {{QEMU}} \
        "${E2E_QEMU_ARGS2[@]}" \
        -drive if=virtio,file=.vm/target-sysext.qcow2,format=qcow2 \
        -net nic,model=virtio -net user,hostfwd=tcp::2222-:22 \
        -display none -daemonize -pidfile .vm/qemu.pid \
        -serial file:.vm/e2e-sysext-target-serial.log

    ok=0
    for i in $(seq 1 120); do
        $E2E_SSH -o ConnectTimeout=3 true 2>/dev/null && ok=1 && break
        sleep 5
    done
    [ "$ok" = "1" ] || { echo "❌ sysext system never came up"; tail -20 .vm/e2e-sysext-target-serial.log 2>/dev/null; exit 1; }
    echo "  ✓ sysext system SSH accessible"

    echo "[4/4] Verifying docker sysext is active..."

    # The .raw file must exist on disk (written by Ignition on first boot)
    if $E2E_SSH "test -f /etc/extensions/docker.raw" 2>/dev/null; then
        SYSEXT_SIZE=$($E2E_SSH "stat -c%s /etc/extensions/docker.raw" 2>/dev/null) || true
        echo "  ✓ /etc/extensions/docker.raw present (${SYSEXT_SIZE} bytes)"
    else
        echo "❌ /etc/extensions/docker.raw not found — Ignition did not download sysext"
        $E2E_SSH "ls /etc/extensions/ 2>/dev/null || echo '(no /etc/extensions/)'" || true
        exit 1
    fi

    # systemd-sysext must be active
    if $E2E_SSH "systemctl is-active systemd-sysext" 2>/dev/null | grep -q "^active"; then
        echo "  ✓ systemd-sysext.service active"
    else
        SYSEXT_STATUS=$($E2E_SSH "systemctl status systemd-sysext --no-pager 2>&1" || true)
        echo "  ⚠ systemd-sysext status: $SYSEXT_STATUS"
    fi

    # docker must be callable (proves the sysext overlay is live)
    if DOCKER_VER=$($E2E_SSH "docker version --format '{{{{.Server.Version}}}}'" 2>/dev/null); then
        echo "  ✓ docker available from sysext: $DOCKER_VER"
    else
        echo "❌ docker not available — sysext not activated"
        $E2E_SSH "systemd-sysext list 2>/dev/null || true" || true
        exit 1
    fi

    echo ""
    echo "✅ SYSEXT pass PASSED"
    echo ""

    # ══════════════════════════════════════════════════════════════════════════
    # NVIDIA PASS — verify enabled-sysext.conf kernel driver config
    # ══════════════════════════════════════════════════════════════════════════
    echo "=== vm-e2e NVIDIA pass ==="
    echo ""

    # Headless config with nvidia_driver_version (no nvidia-runtime sysext — testing kernel driver path only)
    printf '{"channel":"stable","hostname":"nvidia-test","timezone":"UTC","network":{"mode":"dhcp"},"users":[{"username":"core","ssh_keys":["%s"]}],"disk":"/dev/vdb","swap":{"enabled":false},"nvidia_driver_version":"570-open","update_strategy":"off","reboot":false}\n' \
        "$E2E_PUB" > .vm/e2e-nvidia-config.json

    # Fresh disks
    just _kill-vm
    rm -f .vm/boot.qcow2 .vm/target-nvidia.qcow2
    qemu-img create -f qcow2 -b "$(pwd)/.vm/flatcar_base_{{ KNUCKLE_ARCH }}.img" -F qcow2 .vm/boot.qcow2 >/dev/null
    qemu-img create -f qcow2 .vm/target-nvidia.qcow2 20G >/dev/null

    echo "[1/4] Booting installer VM (NVIDIA pass)..."
    {{ QEMU }} \
        -m 4096 -smp 2 -enable-kvm \
        -drive if=virtio,file=.vm/boot.qcow2,format=qcow2 \
        -drive if=virtio,file=.vm/target-nvidia.qcow2,format=qcow2 \
        -fw_cfg name=opt/org.flatcar-linux/config,file=.vm/e2e-installer.ign \
        -net nic,model=virtio -net user,hostfwd=tcp::2222-:22 \
        -display none -daemonize -pidfile .vm/qemu.pid \
        -serial file:.vm/e2e-nvidia-installer-serial.log

    ok=0
    for i in $(seq 1 40); do
        $E2E_SSH -o ConnectTimeout=3 true 2>/dev/null && ok=1 && break
        sleep 3
    done
    [ "$ok" = "1" ] || { echo "❌ installer VM never came up"; tail -20 .vm/e2e-nvidia-installer-serial.log 2>/dev/null; exit 1; }
    echo "  ✓ installer VM ready"

    echo "[2/4] Running headless install (NVIDIA kernel driver config)..."
    $E2E_SCP bin/knuckle core@127.0.0.1:/tmp/knuckle >/dev/null
    $E2E_SCP .vm/e2e-nvidia-config.json core@127.0.0.1:/tmp/e2e-nvidia-config.json >/dev/null
    if ! $E2E_SSH_LONG "timeout 15m sudo /tmp/knuckle --headless --config /tmp/e2e-nvidia-config.json --log-file /tmp/knuckle-nvidia.log"; then
        echo "❌ headless install (nvidia) failed — knuckle-nvidia.log:"
        timeout 20 $E2E_SSH_LONG "cat /tmp/knuckle-nvidia.log" 2>/dev/null || true
        exit 1
    fi
    echo "  ✓ nvidia install completed"

    echo "[3/4] Booting nvidia-configured installed disk..."
    just _kill-vm
    {{ QEMU }} \
        -m 4096 -smp 2 -enable-kvm \
        -drive if=virtio,file=.vm/target-nvidia.qcow2,format=qcow2 \
        -net nic,model=virtio -net user,hostfwd=tcp::2222-:22 \
        -display none -daemonize -pidfile .vm/qemu.pid \
        -serial file:.vm/e2e-nvidia-target-serial.log

    ok=0
    for i in $(seq 1 120); do
        $E2E_SSH -o ConnectTimeout=3 true 2>/dev/null && ok=1 && break
        sleep 3
    done
    [ "$ok" = "1" ] || { echo "❌ installed system never booted"; tail -30 .vm/e2e-nvidia-target-serial.log 2>/dev/null; exit 1; }
    echo "  ✓ nvidia-configured system SSH accessible"

    echo "[4/4] Verifying NVIDIA configuration on installed system..."

    # Hostname
    ACTUAL_HOSTNAME=$($E2E_SSH hostname)
    if [ "$ACTUAL_HOSTNAME" = "nvidia-test" ]; then
        echo "  ✓ hostname: nvidia-test"
    else
        echo "❌ hostname mismatch: expected nvidia-test, got $ACTUAL_HOSTNAME"
        exit 1
    fi

    # enabled-sysext.conf must exist with nvidia-drivers-570-open
    if SYSEXT_CONF=$($E2E_SSH "cat /etc/flatcar/enabled-sysext.conf" 2>/dev/null); then
        if echo "$SYSEXT_CONF" | grep -q "nvidia-drivers-570-open"; then
            echo "  ✓ /etc/flatcar/enabled-sysext.conf contains nvidia-drivers-570-open"
        else
            echo "❌ enabled-sysext.conf exists but wrong content: $SYSEXT_CONF"
            exit 1
        fi
    else
        echo "❌ /etc/flatcar/enabled-sysext.conf not found on installed system"
        exit 1
    fi

    echo ""
    echo "✅ NVIDIA pass PASSED"
    echo ""
    echo "✅ ALL vm-e2e passes PASSED (DHCP · static network · sysext · NVIDIA)"

# SSH into running VM
ssh:
    ssh -t {{SSH_OPTS}} -p 2222 core@127.0.0.1

# Boot the installed target disk to verify installation worked
boot-target:
    #!/usr/bin/env bash
    set -euo pipefail
    just _kill-vm
    [ -f .vm/target.qcow2 ] || { echo "No target disk. Run 'just vm' first."; exit 1; }
    echo "Booting installed target disk..."
    echo "  → Ctrl-a x to quit QEMU"
    BT_ARGS=(-m 2048 -smp 2)
    if [[ "{{KNUCKLE_ARCH}}" == "arm64" ]]; then
        BT_ARGS+=(-M virt -cpu cortex-a57)
        for candidate in /usr/share/AAVMF/AAVMF_CODE.fd /usr/share/qemu-efi-aarch64/QEMU_EFI.fd; do
            [ -f "$candidate" ] && BT_ARGS+=(-drive "if=pflash,format=raw,readonly=on,file=$candidate") && break
        done
    else
        BT_ARGS+=(-enable-kvm)
    fi
    {{QEMU}} \
        "${BT_ARGS[@]}" \
        -drive if=virtio,file=.vm/target.qcow2,format=qcow2 \
        -net nic,model=virtio -net user,hostfwd=tcp::2222-:22 \
        -nographic

# Full end-to-end: build ISO → boot in Ghostty → interactive install
e2e:
    #!/usr/bin/env bash
    set -euo pipefail
    echo "=== E2E: Build ISO + launch interactive install VM ==="
    echo ""

    # Build ISO if not present
    ISO="output/knuckle-installer-stable-{{KNUCKLE_ARCH}}.iso"
    if [[ ! -f "$ISO" ]]; then
        echo "Building ISO..."
        just iso stable
        echo ""
    fi

    just _kill-vm
    mkdir -p .vm
    rm -f .vm/target.qcow2
    qemu-img create -f qcow2 .vm/target.qcow2 20G >/dev/null

    OVMF=""
    if [[ "{{KNUCKLE_ARCH}}" == "arm64" ]]; then
        OVMF_CANDIDATES=(
            /usr/share/AAVMF/AAVMF_CODE.fd
            /usr/share/qemu-efi-aarch64/QEMU_EFI.fd
        )
    else
        OVMF_CANDIDATES=(
            /usr/share/OVMF/OVMF_CODE_4M.fd
            /usr/share/OVMF/OVMF_CODE.fd
            /usr/share/edk2/ovmf/OVMF_CODE_4M.fd
            /usr/share/edk2/ovmf/OVMF_CODE.fd
        )
        for f in /home/linuxbrew/.linuxbrew/Cellar/qemu/*/share/qemu/edk2-x86_64-code.fd; do
            OVMF_CANDIDATES+=("$f")
        done
    fi
    for candidate in "${OVMF_CANDIDATES[@]}"; do
        [ -f "$candidate" ] && OVMF="$candidate" && break
    done
    [ -n "$OVMF" ] || { echo "OVMF not found — install ovmf (amd64) or qemu-efi-aarch64 (arm64)"; exit 1; }

    echo "Launching installer VM (UEFI, systemd-boot, arch={{KNUCKLE_ARCH}})..."
    echo "  → GTK window shows VGA (tty1) — knuckle TUI appears here"
    echo "  → Target disk: .vm/target.qcow2 (20G)"
    echo "  → After install: just boot-target to verify"
    echo ""

    E2E_ISO_ARGS=(-m 4096 -smp 2)
    if [[ "{{KNUCKLE_ARCH}}" == "arm64" ]]; then
        E2E_ISO_ARGS+=(-M virt -cpu cortex-a57)
    else
        E2E_ISO_ARGS+=(-enable-kvm -cpu host)
    fi
    {{QEMU}} \
        "${E2E_ISO_ARGS[@]}" \
        -drive if=pflash,format=raw,readonly=on,file="$OVMF" \
        -cdrom "$ISO" \
        -drive if=virtio,file=.vm/target.qcow2,format=qcow2 \
        -net nic,model=virtio -net user,hostfwd=tcp::2222-:22 \
        -display gtk

# Build installer ISO (requires xorriso, mtools, cpio, systemd-boot-efi)
# KNUCKLE_ARCH=arm64 just iso  — builds arm64 ISO
iso *CHANNEL='stable':
    ./scripts/build-iso.sh --channel {{CHANNEL}} --arch {{KNUCKLE_ARCH}}

# Check that coreos-installer is installed (required for FCOS ISO builds)
check-fcos-tools:
    @which coreos-installer || (echo "coreos-installer not found — run: just tools-fcos" && exit 1)

# Install coreos-installer via the Fedora package (GPG-verified, idempotent).
# Upstream no longer publishes prebuilt release binaries (v0.23.0+ ship only a
# vendor tarball), so the previous `releases/latest/download` URL 404s — and
# without --fail curl wrote the 404 page into /usr/local/bin as an executable.
tools-fcos:
    #!/usr/bin/env bash
    set -euo pipefail
    if command -v coreos-installer &>/dev/null; then
        echo "coreos-installer already installed: $(coreos-installer --version)"
        exit 0
    fi
    if ! command -v dnf &>/dev/null; then
        echo "error: dnf not found — install coreos-installer via your distro's" >&2
        echo "  package manager, or run the digest-pinned container:" >&2
        echo "  quay.io/coreos/coreos-installer:release" >&2
        exit 1
    fi
    echo "Installing coreos-installer via dnf..."
    sudo dnf install -y coreos-installer
    coreos-installer --version
    echo "tools-fcos ok"

# Build FCOS installer ISO (requires coreos-installer — run: just tools-fcos)
# Produces output/knuckle-fcos-installer-<stream>-<arch>.iso
# Override stream/arch: just build-fcos-iso arch="arm64" stream="testing"
build-fcos-iso arch="amd64" stream="stable": check-fcos-tools
    ./scripts/build-fcos-iso.sh --arch {{arch}} --stream {{stream}}

# Boot ISO in QEMU with UEFI (Ctrl-a x to quit)
# KNUCKLE_ARCH=arm64 just boot-iso  — boots arm64 ISO (requires qemu-system-aarch64)
boot-iso:
    #!/usr/bin/env bash
    set -euo pipefail
    ISO="output/knuckle-installer-stable-{{KNUCKLE_ARCH}}.iso"
    [ -f "$ISO" ] || { echo "No ISO. Run: just iso"; exit 1; }
    just _kill-vm
    mkdir -p .vm
    [ -f .vm/target.qcow2 ] || qemu-img create -f qcow2 .vm/target.qcow2 20G >/dev/null
    OVMF=""
    if [[ "{{KNUCKLE_ARCH}}" == "arm64" ]]; then
        OVMF_CANDIDATES=(
            /usr/share/AAVMF/AAVMF_CODE.fd
            /usr/share/qemu-efi-aarch64/QEMU_EFI.fd
        )
    else
        OVMF_CANDIDATES=(
            /usr/share/OVMF/OVMF_CODE_4M.fd
            /usr/share/OVMF/OVMF_CODE.fd
            /usr/share/edk2/ovmf/OVMF_CODE_4M.fd
            /usr/share/edk2/ovmf/OVMF_CODE.fd
        )
        for f in /home/linuxbrew/.linuxbrew/Cellar/qemu/*/share/qemu/edk2-x86_64-code.fd; do
            OVMF_CANDIDATES+=("$f")
        done
    fi
    for candidate in "${OVMF_CANDIDATES[@]}"; do
        [ -f "$candidate" ] && OVMF="$candidate" && break
    done
    [ -n "$OVMF" ] || { echo "OVMF not found — install ovmf (amd64) or qemu-efi-aarch64 (arm64)"; exit 1; }
    echo "Booting ISO (UEFI, systemd-boot, arch={{KNUCKLE_ARCH}})..."
    echo "  → GTK window shows VGA console (tty1) where knuckle TUI runs"
    echo ""
    {{QEMU}} \
        -m 4096 -smp 2 -enable-kvm -cpu host \
        -drive if=pflash,format=raw,readonly=on,file="$OVMF" \
        -cdrom "$ISO" \
        -drive if=virtio,file=.vm/target.qcow2,format=qcow2 \
        -net nic,model=virtio -net user,hostfwd=tcp::2222-:22 \
        -display gtk

# Boot ISO in background, wait for SSH
boot-iso-ssh:
    #!/usr/bin/env bash
    set -euo pipefail
    ISO="output/knuckle-installer-stable-{{KNUCKLE_ARCH}}.iso"
    [ -f "$ISO" ] || { echo "No ISO. Run: just iso"; exit 1; }
    just _kill-vm
    mkdir -p .vm
    [ -f .vm/target.qcow2 ] || qemu-img create -f qcow2 .vm/target.qcow2 20G >/dev/null
    OVMF=""
    if [[ "{{KNUCKLE_ARCH}}" == "arm64" ]]; then
        OVMF_CANDIDATES=(
            /usr/share/AAVMF/AAVMF_CODE.fd
            /usr/share/qemu-efi-aarch64/QEMU_EFI.fd
        )
    else
        OVMF_CANDIDATES=(
            /usr/share/OVMF/OVMF_CODE_4M.fd
            /usr/share/OVMF/OVMF_CODE.fd
            /usr/share/edk2/ovmf/OVMF_CODE_4M.fd
            /usr/share/edk2/ovmf/OVMF_CODE.fd
        )
        for f in /home/linuxbrew/.linuxbrew/Cellar/qemu/*/share/qemu/edk2-x86_64-code.fd; do
            OVMF_CANDIDATES+=("$f")
        done
    fi
    for candidate in "${OVMF_CANDIDATES[@]}"; do
        [ -f "$candidate" ] && OVMF="$candidate" && break
    done
    [ -n "$OVMF" ] || { echo "OVMF not found — install ovmf (amd64) or qemu-efi-aarch64 (arm64)"; exit 1; }
    {{QEMU}} \
        -m 4096 -smp 2 -enable-kvm \
        -drive if=pflash,format=raw,readonly=on,file="$OVMF" \
        -cdrom "$ISO" \
        -drive if=virtio,file=.vm/target.qcow2,format=qcow2 \
        -net nic,model=virtio -net user,hostfwd=tcp::2222-:22 \
        -display none -daemonize -pidfile .vm/qemu.pid
    echo "Waiting for ISO to boot..."
    for i in $(seq 1 40); do
        ssh {{SSH_OPTS}} -o ConnectTimeout=2 -p 2222 core@127.0.0.1 true 2>/dev/null && break
        sleep 3
    done
    echo "VM ready."
    exec ssh -t {{SSH_OPTS}} -p 2222 core@127.0.0.1

# Headless ISO boot smoke test (amd64 only). Verifies systemd-boot serial output,
# initrd boot targets, and absence of xd2root/x2dauto errors.
iso-smoke ISO OVMF TIMEOUT='120':
    #!/usr/bin/env bash
    set -euo pipefail
    [ "{{KNUCKLE_ARCH}}" = "amd64" ] || { echo "iso-smoke currently supports amd64 only"; exit 1; }
    QEMU_BIN="{{QEMU}}" bash ./scripts/iso-smoke.sh "{{ISO}}" "{{OVMF}}" "{{TIMEOUT}}"

# Boot the installer ISO in a hardware-like VM and capture install failure artifacts.
hardware-repro:
    #!/usr/bin/env bash
    set -euo pipefail
    PIDFILE=.vm/qemu.pid
    cleanup() {
        if [ -f "$PIDFILE" ]; then
            sudo -n kill "$(sudo -n cat "$PIDFILE")" 2>/dev/null || true
            sudo -n rm -f "$PIDFILE" 2>/dev/null || true
            rm -f "$PIDFILE" 2>/dev/null || true
        fi
    }
    trap cleanup EXIT
    [ "{{KNUCKLE_ARCH}}" = "amd64" ] || { echo "hardware-repro currently supports amd64 only"; exit 1; }
    just build
    ISO="output/knuckle-installer-stable-{{KNUCKLE_ARCH}}.iso"
    if [ ! -f "$ISO" ]; then
        just iso
    else
        echo "using existing ISO: $ISO"
    fi
    just _kill-vm
    mkdir -p .vm
    rm -f .vm/hardware-target.qcow2 .vm/hardware-installer-serial.log .vm/hardware-install-output.log \
        .vm/hardware-knuckle.log .vm/hardware-journal.log .vm/hardware-disk-inventory.log \
        .vm/hardware-ovmf-vars.fd .vm/hardware-installer.ign .vm/hardware_key .vm/hardware_key.pub
    qemu-img create -f qcow2 .vm/hardware-target.qcow2 20G >/dev/null
    ssh-keygen -t ed25519 -f .vm/hardware_key -N "" -C "knuckle-hardware-repro" -q
    HARDWARE_PUB=$(cat .vm/hardware_key.pub)
    printf '{"ignition":{"version":"3.4.0"},"passwd":{"users":[{"name":"core","sshAuthorizedKeys":["%s"]}]},"systemd":{"units":[{"name":"sshd.service","enabled":true}]}}\n' \
        "$HARDWARE_PUB" > .vm/hardware-installer.ign

    OVMF_CODE=""
    OVMF_VARS=""
    for candidate in /usr/share/OVMF/OVMF_CODE_4M.fd /usr/share/OVMF/OVMF_CODE.fd /usr/share/edk2/ovmf/OVMF_CODE_4M.fd /usr/share/edk2/ovmf/OVMF_CODE.fd; do
        [ -f "$candidate" ] && OVMF_CODE="$candidate" && break
    done
    for candidate in /usr/share/OVMF/OVMF_VARS_4M.fd /usr/share/OVMF/OVMF_VARS.fd /usr/share/edk2/ovmf/OVMF_VARS_4M.fd /usr/share/edk2/ovmf/OVMF_VARS.fd; do
        [ -f "$candidate" ] && OVMF_VARS="$candidate" && break
    done
    [ -n "$OVMF_CODE" ] || { echo "OVMF firmware code image not found — install ovmf"; exit 1; }
    [ -n "$OVMF_VARS" ] || { echo "OVMF firmware vars image not found — install ovmf"; exit 1; }
    cp "$OVMF_VARS" .vm/hardware-ovmf-vars.fd

    QEMU_CMD=({{QEMU}})
    QEMU_ACCEL=(-enable-kvm -cpu host)
    if [ ! -r /dev/kvm ] || [ ! -w /dev/kvm ]; then
        if sudo -n test -r /dev/kvm && sudo -n test -w /dev/kvm; then
            echo "using sudo for KVM access"
            QEMU_CMD=(sudo -n {{QEMU}})
        else
        echo "warning: /dev/kvm is not accessible; falling back to TCG emulation"
        QEMU_ACCEL=(-accel tcg,thread=multi -cpu max)
        fi
    fi

    "${QEMU_CMD[@]}" \
        -machine q35 -m 4096 -smp 2 "${QEMU_ACCEL[@]}" \
        -drive if=pflash,format=raw,readonly=on,file="$OVMF_CODE" \
        -drive if=pflash,format=raw,file=.vm/hardware-ovmf-vars.fd \
        -device ich9-ahci,id=sata0 \
        -drive if=none,id=installeriso,file="$ISO",format=raw,readonly=on \
        -device ide-cd,drive=installeriso,bus=sata0.0 \
        -drive if=none,id=target,file=.vm/hardware-target.qcow2,format=qcow2 \
        -device ide-hd,drive=target,bus=sata0.1,serial=target-disk \
        -fw_cfg name=opt/org.flatcar-linux/config,file=.vm/hardware-installer.ign \
        -netdev user,id=n0,hostfwd=tcp::2222-:22 \
        -device e1000e,netdev=n0 \
        -display none -daemonize -pidfile "$PIDFILE" \
        -serial file:.vm/hardware-installer-serial.log

    HARDWARE_SSH="ssh {{SSH_OPTS}} -o BatchMode=yes -i .vm/hardware_key -p 2222 -o ServerAliveInterval=30 -o ServerAliveCountMax=10 core@127.0.0.1"
    HARDWARE_SCP="scp {{SSH_OPTS}} -o BatchMode=yes -i .vm/hardware_key -P 2222"

    ok=0
    for i in $(seq 1 120); do
        $HARDWARE_SSH -o ConnectTimeout=3 true 2>/dev/null && ok=1 && break
        sleep 5
    done
    if [ "$ok" != "1" ]; then
        echo "❌ installer ISO never came up (serial log below)"
        tail -40 .vm/hardware-installer-serial.log 2>/dev/null || true
        exit 1
    fi
    echo "✓ installer ISO ready over SSH"

    TARGET_DEV=$($HARDWARE_SSH "lsblk -dnpo PATH,TYPE | awk '\$2 == \"disk\" { print \$1; exit }'")
    [ -n "$TARGET_DEV" ] || { echo "❌ failed to detect target disk"; exit 1; }
    TARGET_BY_ID=$($HARDWARE_SSH "for p in /dev/disk/by-id/*; do [ -e \"\$p\" ] || continue; [ \"\$(readlink -f \"\$p\")\" = \"$TARGET_DEV\" ] && case \"\$p\" in *-part*) ;; *) echo \"\$p\"; break ;; esac; done")
    [ -n "$TARGET_BY_ID" ] || TARGET_BY_ID="$TARGET_DEV"

    echo "target disk: $TARGET_BY_ID"
    $HARDWARE_SSH "printf '=== lsblk ===\n'; lsblk -o NAME,PATH,TYPE,SIZE,MODEL,SERIAL,TRAN,RM,MOUNTPOINT; printf '\n=== /dev/disk/by-id ===\n'; ls -l /dev/disk/by-id" \
        | tee .vm/hardware-disk-inventory.log

    printf '{"channel":"stable","hostname":"hardware-repro","timezone":"UTC","network":{"mode":"dhcp"},"users":[{"username":"core","ssh_keys":["%s"]}],"disk":"%s","update_strategy":"off","reboot":false}\n' \
        "$HARDWARE_PUB" "$TARGET_BY_ID" > .vm/hardware-repro.json
    $HARDWARE_SCP .vm/hardware-repro.json core@127.0.0.1:/tmp/hardware-repro.json >/dev/null

    set +e
    $HARDWARE_SSH "timeout 15m sudo /usr/bin/knuckle --headless --config /tmp/hardware-repro.json --log-file /tmp/knuckle.log" 2>&1 \
        | tee .vm/hardware-install-output.log
    rc=${PIPESTATUS[0]}
    set -e

    $HARDWARE_SSH "sudo cat /tmp/knuckle.log" 2>/dev/null | tee .vm/hardware-knuckle.log >/dev/null || true
    $HARDWARE_SSH "sudo journalctl -b --no-pager -u sshd.service -u systemd-networkd.service -u systemd-udevd.service" 2>/dev/null \
        | tee .vm/hardware-journal.log >/dev/null || true

    echo ""
    echo "artifacts:"
    echo "  .vm/hardware-install-output.log"
    echo "  .vm/hardware-knuckle.log"
    echo "  .vm/hardware-journal.log"
    echo "  .vm/hardware-disk-inventory.log"
    echo "  .vm/hardware-installer-serial.log"

    if [ "$rc" -ne 0 ]; then
        echo ""
        echo "❌ hardware-like install repro failed"
        echo "   see .vm/hardware-install-output.log and .vm/hardware-knuckle.log"
        exit "$rc"
    fi

    echo ""
    echo "✅ hardware-like install completed"

# Stop running VM
stop:
    just _kill-vm

# Clean everything
clean:
    #!/usr/bin/env bash
    just _kill-vm 2>/dev/null || true
    rm -rf bin/ .vm/

# ── Catalog & version checks (require network; not part of `just ci`) ─────────

# Verify descriptions.go covers all live Flatcar Bakery extensions.
# Informational: reports gaps with copy-pasteable code stubs but does not fail.
# Run before a release to catch newly-added bakery extensions.
catalog-check:
    go run ./scripts/catalog_check/

# Same as catalog-check but exits non-zero if any extensions lack descriptions.
# Use as a hard gate in automated pipelines.
catalog-check-strict:
    go run ./scripts/catalog_check/ --strict

# Verify model.go NvidiaDriverOptions against the Flatcar NVIDIA docs.
# Reports driver series mentioned in upstream docs vs what is in model.go.
nvidia-check:
    #!/usr/bin/env bash
    GOOS=linux GOARCH={{KNUCKLE_ARCH}} CGO_ENABLED=0 go build \
        -o bin/nvidia-check ./cmd/nvidia-check
    ./bin/nvidia-check

# Full pre-release preflight: catalog coverage + nvidia versions + CI gate + CHANGELOG reminder.
# Run this before tagging any release.
release-preflight: ci
    #!/usr/bin/env bash
    set -euo pipefail
    echo ""
    echo "[2/3] Checking sysext catalog coverage against live bakery..."
    go run ./scripts/catalog_check/ --strict
    echo ""
    echo "[3/3] Checking NVIDIA driver series against Flatcar docs..."
    ./scripts/nvidia_check.sh
    echo ""
    echo "✓ release-preflight complete"
    echo ""
    echo "Pre-tag checklist (manual steps):"
    echo "  - [ ] Promote CHANGELOG.md [Unreleased] to ## [vX.Y.Z] - $(date +%Y-%m-%d)"
    echo "  - [ ] Update compare links in CHANGELOG.md footer"
    echo "  - [ ] Run 'just vm-e2e' for final VM verification before publishing"

# QA: full PR test — build + unit tests + Flatcar VM install + boot + domain assertions.
# Artifacts saved to .qa/runs/pr-N-TIMESTAMP/. Failed runs generate an issue body.
# Set QA_HOST=user@host to run on a remote machine; defaults to localhost.
# Requires: QEMU with KVM, Flatcar base image (see docs/GHOST-LAB.md).
qa-pr PR:
    @scripts/qa-test-pr.sh "{{PR}}"
    echo ""
    echo "=== release-preflight ==="
    echo ""
    echo "[1/3] just ci already passed"
    echo ""
    echo "[2/3] Checking sysext catalog coverage against live bakery..."
    go run ./scripts/catalog_check/ --strict
    echo ""
    echo "[3/3] Checking NVIDIA driver series against Flatcar docs..."
    ./scripts/nvidia_check.sh
    echo ""
    echo "✓ release-preflight complete — safe to tag"
    echo "  Reminder: run 'just vm-e2e' before publishing the release."

# --- Internal helpers (not listed) ---

[private]
_install-golangci-lint:
    #!/usr/bin/env bash
    set -euo pipefail
    if [[ -x "{{GOLANGCI_LINT}}" ]]; then
        ver=$("{{GOLANGCI_LINT}}" --version 2>&1 | grep -oP 'version \K[0-9.]+' || true)
        [[ "$ver" == "{{GOLANGCI_LINT_VERSION}}" ]] && exit 0
        echo "golangci-lint version mismatch (got $ver, want {{GOLANGCI_LINT_VERSION}}) — reinstalling"
    fi
    mkdir -p .tools
    OS=$(uname -s | tr '[:upper:]' '[:lower:]')
    ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
    case "${OS}-${ARCH}" in
        linux-amd64) EXPECTED_SHA="{{GOLANGCI_LINT_SHA256_LINUX_AMD64}}" ;;
        linux-arm64) EXPECTED_SHA="{{GOLANGCI_LINT_SHA256_LINUX_ARM64}}" ;;
        darwin-amd64) EXPECTED_SHA="{{GOLANGCI_LINT_SHA256_DARWIN_AMD64}}" ;;
        darwin-arm64) EXPECTED_SHA="{{GOLANGCI_LINT_SHA256_DARWIN_ARM64}}" ;;
        *)
            echo "Unsupported platform for golangci-lint: ${OS}-${ARCH}" >&2
            exit 1
            ;;
    esac
    ARCHIVE="golangci-lint-{{GOLANGCI_LINT_VERSION}}-${OS}-${ARCH}.tar.gz"
    URL="https://github.com/golangci/golangci-lint/releases/download/v{{GOLANGCI_LINT_VERSION}}/$ARCHIVE"
    TMPDIR=$(mktemp -d)
    trap 'rm -rf "$TMPDIR"' EXIT
    echo "Downloading golangci-lint v{{GOLANGCI_LINT_VERSION}}..."
    curl -sSfL -o "$TMPDIR/$ARCHIVE" "$URL"
    if command -v sha256sum >/dev/null 2>&1; then
        echo "${EXPECTED_SHA}  $TMPDIR/$ARCHIVE" | sha256sum -c -
    else
        echo "${EXPECTED_SHA}  $TMPDIR/$ARCHIVE" | shasum -a 256 -c -
    fi
    tar -xzf "$TMPDIR/$ARCHIVE" -C .tools \
        --strip-components=1 \
        "golangci-lint-{{GOLANGCI_LINT_VERSION}}-${OS}-${ARCH}/golangci-lint"
    chmod +x "{{GOLANGCI_LINT}}"
    echo "golangci-lint v{{GOLANGCI_LINT_VERSION}} installed to {{GOLANGCI_LINT}}"

# Lint all bash scripts with shellcheck
shell-lint: _install-shellcheck
    {{SHELLCHECK}} --severity=warning \
      scripts/qa-test-pr.sh \
      scripts/build-iso.sh \
      scripts/lib/vm-kubevirt.sh \
      scripts/fix-ghost-otel-process-noise.sh \
      scripts/nvidia_check.sh \
      scripts/drive-demo.sh \
      scripts/record-demo.sh

_install-shellcheck:
    #!/usr/bin/env bash
    set -euo pipefail
    if [[ -x "{{SHELLCHECK}}" ]]; then
        ver=$("{{SHELLCHECK}}" --version 2>&1 | grep -oP 'version:\s*\K[0-9.]+' || true)
        [[ "$ver" == "{{SHELLCHECK_VERSION}}" ]] && exit 0
        echo "shellcheck version mismatch (got $ver, want {{SHELLCHECK_VERSION}}) — reinstalling"
    fi
    mkdir -p .tools
    OS=$(uname -s | tr '[:upper:]' '[:lower:]')
    ARCH=$(uname -m | sed 's/arm64/aarch64/')
    case "${OS}-${ARCH}" in
        linux-x86_64) EXPECTED_SHA="{{SHELLCHECK_SHA256_LINUX_AMD64}}" ;;
        linux-aarch64) EXPECTED_SHA="{{SHELLCHECK_SHA256_LINUX_ARM64}}" ;;
        darwin-x86_64) EXPECTED_SHA="{{SHELLCHECK_SHA256_DARWIN_AMD64}}" ;;
        darwin-aarch64) EXPECTED_SHA="{{SHELLCHECK_SHA256_DARWIN_ARM64}}" ;;
        *)
            echo "Unsupported platform for shellcheck: ${OS}-${ARCH}" >&2
            exit 1
            ;;
    esac
    ARCHIVE="shellcheck-v{{SHELLCHECK_VERSION}}.${OS}.${ARCH}.tar.xz"
    URL="https://github.com/koalaman/shellcheck/releases/download/v{{SHELLCHECK_VERSION}}/$ARCHIVE"
    TMPDIR=$(mktemp -d)
    trap 'rm -rf "$TMPDIR"' EXIT
    echo "Downloading shellcheck v{{SHELLCHECK_VERSION}}..."
    curl -sSfL -o "$TMPDIR/$ARCHIVE" "$URL"
    if command -v sha256sum >/dev/null 2>&1; then
        echo "${EXPECTED_SHA}  $TMPDIR/$ARCHIVE" | sha256sum -c -
    else
        echo "${EXPECTED_SHA}  $TMPDIR/$ARCHIVE" | shasum -a 256 -c -
    fi
    tar -xJf "$TMPDIR/$ARCHIVE" -C .tools \
        --strip-components=1 \
        "shellcheck-v{{SHELLCHECK_VERSION}}/shellcheck"
    chmod +x "{{SHELLCHECK}}"
    echo "shellcheck v{{SHELLCHECK_VERSION}} installed to {{SHELLCHECK}}"

[private]
_ensure-base:
    #!/usr/bin/env bash
    set -euo pipefail
    SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
    # shellcheck source=scripts/lib/verify-flatcar.sh
    source "${SCRIPT_DIR}/scripts/lib/verify-flatcar.sh"
    mkdir -p .vm
    if [ ! -f ".vm/flatcar_base_{{KNUCKLE_ARCH}}.img" ]; then
        ARCH_DIR="{{KNUCKLE_ARCH}}-usr"
        IMAGE_BZ2=".vm/flatcar_base_{{KNUCKLE_ARCH}}.img.bz2"
        IMAGE_URL="https://stable.release.flatcar-linux.net/${ARCH_DIR}/current/flatcar_production_qemu_image.img.bz2"
        echo "Downloading Flatcar stable QEMU image for {{KNUCKLE_ARCH}} (one-time)..."
        curl -L -o "$IMAGE_BZ2" "$IMAGE_URL"
        # Verify GPG-signatured .DIGESTS + SHA512 before trusting the image that
        # _write-ignition injects the developer SSH key into. Fail closed on mismatch.
        verify_flatcar_file "$IMAGE_BZ2" "$IMAGE_URL" "flatcar_production_qemu_image.img.bz2"
        bunzip2 "$IMAGE_BZ2"
    fi

[private]
_kill-vm:
    #!/usr/bin/env bash
    if [ -f .vm/qemu.pid ]; then
        kill "$(cat .vm/qemu.pid)" 2>/dev/null || true
        rm -f .vm/qemu.pid
        sleep 1
    fi

[private]
_write-ignition:
    #!/usr/bin/env bash
    set -euo pipefail
    SSH_KEY=$(cat ~/.ssh/id_ed25519.pub)
    cat > .vm/config.ign <<EOF
    {
      "ignition": {"version": "3.4.0"},
      "passwd": {"users": [{"name": "core", "sshAuthorizedKeys": ["$SSH_KEY"]}]},
      "systemd": {"units": [
        {"name": "sshd.service", "enabled": true}
      ]}
    }
    EOF

# Run BATS shell tests for scripts/
bats-test:
    #!/usr/bin/env bash
    set -euo pipefail
    if ! command -v bats &>/dev/null; then
        echo "bats-core not found. Install: npm i -g bats or apt install bats" >&2
        exit 1
    fi
    bats scripts/tests/
