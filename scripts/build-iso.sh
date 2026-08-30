#!/usr/bin/env bash
# Build a UEFI-bootable ISO containing Flatcar Linux + knuckle installer.
#
# Uses systemd-boot (UEFI-only, BLS entries). GRUB is not used.
# The ISO boots into a live Flatcar environment with knuckle auto-launching
# on the console. No network required during boot.
#
# Requirements (Linux): xorriso mformat mcopy (mtools) cpio gzip systemd-boot-efi
#   Ubuntu:  sudo apt-get install -y xorriso mtools cpio systemd-boot-efi
#   Fedora:  sudo dnf install -y xorriso mtools cpio systemd-boot-unsigned
#
# Usage: ./scripts/build-iso.sh [--channel stable|beta|alpha|lts|edge] [--arch amd64|arm64] [--binary /path/to/knuckle]
set -euo pipefail

# ── Argument parsing ─────────────────────────────────────────────────────────
CHANNEL="stable"
ARCH="amd64"
BINARY_OVERRIDE=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        --channel=*) CHANNEL="${1#--channel=}"; shift ;;
        --channel)   CHANNEL="$2"; shift 2 ;;
        --arch=*)    ARCH="${1#--arch=}"; shift ;;
        --arch)      ARCH="$2"; shift 2 ;;
        --binary=*)  BINARY_OVERRIDE="${1#--binary=}"; shift ;;
        --binary)    BINARY_OVERRIDE="$2"; shift 2 ;;
        stable|beta|alpha|lts|edge) CHANNEL="$1"; shift ;;
        *) echo "Unknown argument: $1" >&2; exit 1 ;;
    esac
done

# ── Validate arch + channel combination ─────────────────────────────────────
if [[ "$ARCH" != "amd64" && "$ARCH" != "arm64" ]]; then
    echo "error: --arch must be amd64 or arm64 (got '$ARCH')" >&2; exit 1
fi
case "$CHANNEL" in
    stable|beta|alpha|lts|edge) ;;
    *) echo "error: --channel must be stable, beta, alpha, lts, or edge (got '$CHANNEL')" >&2; exit 1 ;;
esac
if [[ "$ARCH" == "arm64" && "$CHANNEL" == "lts" ]]; then
    echo "error: LTS channel is not available for arm64" >&2; exit 1
fi

# ── Paths ────────────────────────────────────────────────────────────────────
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
BUILD_DIR="$ROOT_DIR/.iso-build"
OUTPUT_DIR="$ROOT_DIR/output"

BINARY=""
if [[ -n "$BINARY_OVERRIDE" ]]; then
    BINARY="$BINARY_OVERRIDE"
elif [[ -f "$ROOT_DIR/bin/knuckle-${ARCH}" ]]; then
    BINARY="$ROOT_DIR/bin/knuckle-${ARCH}"
elif [[ -f "$ROOT_DIR/bin/knuckle" ]]; then
    BINARY="$ROOT_DIR/bin/knuckle"
elif [[ -f "$ROOT_DIR/knuckle" ]]; then
    # CI fallback: binary placed at repo root (no bin/ dir in CI artifacts)
    BINARY="$ROOT_DIR/knuckle"
fi

# Flatcar release server arch directory: "amd64-usr" or "arm64-usr"
ARCH_DIR="${ARCH}-usr"
BASE_URL="https://flatcar.cdn.cncf.io/${CHANNEL}/${ARCH_DIR}/current"

# ── Dependency check ─────────────────────────────────────────────────────────
for cmd in xorriso mformat mcopy mmd cpio gzip; do
    if ! command -v "$cmd" &>/dev/null; then
        echo "Missing: $cmd" >&2
        echo "  Ubuntu: sudo apt-get install -y xorriso mtools cpio" >&2
        exit 1
    fi
done

# Locate systemd-boot EFI binary — arch-specific file name
# amd64: systemd-bootx64.efi  →  EFI/BOOT/BOOTX64.EFI  (UEFI spec)
# arm64: systemd-bootaa64.efi →  EFI/BOOT/BOOTAA64.EFI (UEFI spec)
if [[ "$ARCH" == "arm64" ]]; then
    SDBOOT_FILENAME="systemd-bootaa64.efi"
    EFI_BOOT_NAME="BOOTAA64.EFI"
else
    SDBOOT_FILENAME="systemd-bootx64.efi"
    EFI_BOOT_NAME="BOOTX64.EFI"
fi
SDBOOT_EFI=""
for candidate in     "/usr/lib/systemd/boot/efi/${SDBOOT_FILENAME}"     "/lib/systemd/boot/efi/${SDBOOT_FILENAME}"     "/usr/share/systemd/boot/efi/${SDBOOT_FILENAME}"; do
    if [[ -f "$candidate" ]]; then
        SDBOOT_EFI="$candidate"
        break
    fi
done
if [[ -z "$SDBOOT_EFI" ]]; then
    echo "Missing: ${SDBOOT_FILENAME}" >&2
    echo "  Ubuntu: sudo apt-get install -y systemd-boot-efi" >&2
    echo "  Fedora: sudo dnf install -y systemd-boot-unsigned" >&2
    exit 1
fi

echo "=== Building knuckle installer ISO (channel: $CHANNEL, arch: $ARCH) ==="
echo "  systemd-boot: $SDBOOT_EFI"

# ── 1. Build knuckle binary (skipped when binary already present) ─────────────
if [[ ! -f "$BINARY" ]]; then
    echo "[1/5] Building knuckle..."
    VERSION="$(git -C "$ROOT_DIR" describe --tags --always 2>/dev/null || echo dev)"
    (cd "$ROOT_DIR" && GOOS=linux GOARCH="$ARCH" CGO_ENABLED=0 \
        go build -ldflags="-s -w -X main.version=${VERSION}" -o "bin/knuckle-${ARCH}" ./cmd/knuckle)
    BINARY="$ROOT_DIR/bin/knuckle-${ARCH}"
else
    echo "[1/5] Using existing knuckle binary: $BINARY"
fi

# ── verify_pxe_file: SHA512 + GPG verification for a downloaded PXE file ──────
# Usage: verify_pxe_file <local_file> <upstream_base_url> <upstream_filename>
# Fetches <url>.DIGESTS and <url>.DIGESTS.asc, verifies the GPG signature against
# the embedded Flatcar release key, then confirms the SHA512 of the local file.
# Exits non-zero on GPG or SHA512 mismatch. Skips silently if DIGESTS unreachable.
FLATCAR_KEY="$ROOT_DIR/internal/bakery/keys/flatcar-signing.asc"
verify_pxe_file() {
    local local_file="$1"
    local upstream_url="$2"
    local upstream_name="$3"

    local digests_url="${upstream_url}.DIGESTS"
    local asc_url="${digests_url}.asc"
    local tmp_dir
    tmp_dir="$(mktemp -d)"
    local digests_file="$tmp_dir/DIGESTS"
    local asc_file="$tmp_dir/DIGESTS.asc"

    # Fetch DIGESTS (soft-fail if unavailable — CDN may not publish per-file digests)
    if ! curl -fsSL --max-time 30 -o "$digests_file" "$digests_url" 2>/dev/null; then
        rm -rf "$tmp_dir"
        echo "  ⚠ DIGESTS not available for $upstream_name — skipping verification" >&2
        return 0
    fi

    # GPG signature verification.
    # Flatcar .DIGESTS.asc is a clearsigned message (PGP SIGNED MESSAGE, not a
    # detached signature), so we use --decrypt which verifies and extracts the
    # content in one step.  We then use the verified content for the SHA512 check
    # instead of the separately downloaded unsigned .DIGESTS file.
    if curl -fsSL --max-time 30 -o "$asc_file" "$asc_url" 2>/dev/null; then
        local gpg_home="$tmp_dir/gnupg"
        local verified_digests="$tmp_dir/DIGESTS.verified"
        mkdir -p "$gpg_home"
        chmod 700 "$gpg_home"
        GNUPGHOME="$gpg_home" gpg --quiet --import "$FLATCAR_KEY" 2>/dev/null
        if ! GNUPGHOME="$gpg_home" gpg --quiet --decrypt "$asc_file" > "$verified_digests" 2>/dev/null; then
            rm -rf "$tmp_dir"
            echo "error: GPG signature verification failed for $upstream_name" >&2
            exit 1
        fi
        echo "  ✓ GPG signature verified: $upstream_name"
        # Use the verified (signature-checked) content for the SHA512 lookup.
        digests_file="$verified_digests"
    else
        echo "  ⚠ .DIGESTS.asc unavailable — GPG check skipped for $upstream_name" >&2
    fi

    # SHA512 verification against the DIGESTS file (filename-bound, not just hash)
    local insha512=0 expected_hash=""
    while IFS= read -r line; do
        if [[ "$line" == "# SHA512 HASH" ]]; then
            insha512=1; continue
        fi
        if [[ "$line" =~ ^# ]]; then
            insha512=0; continue
        fi
        if [[ "$insha512" -eq 1 && -n "$line" ]]; then
            local hash fname
            hash="${line%%  *}"
            fname="${line##*  }"
            if [[ "$fname" == "$upstream_name" ]]; then
                expected_hash="$hash"
                break
            fi
        fi
    done < "$digests_file"

    if [[ -z "$expected_hash" ]]; then
        rm -rf "$tmp_dir"
        echo "error: SHA512 for $upstream_name not found in DIGESTS" >&2
        exit 1
    fi

    local actual_hash
    actual_hash="$(sha512sum "$local_file" | awk '{print $1}')"
    if [[ "$actual_hash" != "$expected_hash" ]]; then
        rm -rf "$tmp_dir"
        echo "error: SHA512 mismatch for $upstream_name (expected $expected_hash, got $actual_hash)" >&2
        exit 1
    fi
    echo "  ✓ SHA512 verified: $upstream_name"

    rm -rf "$tmp_dir"
}

# ── 2. Download Flatcar PXE artifacts ────────────────────────────────────────
mkdir -p "$BUILD_DIR"
echo "[2/5] Fetching Flatcar PXE artifacts ($CHANNEL)..."

KERNEL="$BUILD_DIR/vmlinuz"
INITRD="$BUILD_DIR/initrd.cpio.gz"

if [[ ! -f "$KERNEL" ]]; then
    curl -fsSL --retry 3 --retry-delay 5 --retry-all-errors -o "$KERNEL" "$BASE_URL/flatcar_production_pxe.vmlinuz"
    verify_pxe_file "$KERNEL" "$BASE_URL/flatcar_production_pxe.vmlinuz" "flatcar_production_pxe.vmlinuz"
fi
if [[ ! -f "$INITRD" ]]; then
    curl -fsSL --retry 3 --retry-delay 5 --retry-all-errors -o "$INITRD" "$BASE_URL/flatcar_production_pxe_image.cpio.gz"
    verify_pxe_file "$INITRD" "$BASE_URL/flatcar_production_pxe_image.cpio.gz" "flatcar_production_pxe_image.cpio.gz"
fi

echo "  kernel : $(du -h "$KERNEL"  | cut -f1)"
echo "  initrd : $(du -h "$INITRD"  | cut -f1)"

# ── 3. Inject knuckle into Flatcar usr.squashfs ───────────────────────────────
# Flatcar PXE initrd = cpio(etc/ + usr.squashfs).  The squashfs is mounted at
# /usr/ in the live environment.  We unpack it, add our binary + systemd units,
# and repack.  The modified initrd is self-contained — no network, no Ignition,
# no fw_cfg tricks needed.
#
# Cache: squashfs is only rebuilt when the knuckle binary changes.
BINARY_HASH=$(sha256sum "$BINARY" | cut -c1-16)
MODIFIED_SQUASH="$BUILD_DIR/usr-modified-${BINARY_HASH}.squashfs"
MODIFIED_INITRD="$BUILD_DIR/modified-initrd-${BINARY_HASH}.cpio.gz"

if [[ -f "$MODIFIED_INITRD" ]]; then
    echo "[3/5] Using cached modified initrd (binary unchanged)."
else
    echo "[3/5] Injecting knuckle into Flatcar squashfs..."
    # Remove stale cached files from previous binary versions
    rm -f "$BUILD_DIR"/usr-modified-*.squashfs "$BUILD_DIR"/modified-initrd-*.cpio.gz

    SQUASH_WORK="$BUILD_DIR/squash-work"
    rm -rf "$SQUASH_WORK"
    mkdir -p "$SQUASH_WORK"

    # Extract usr.squashfs (and the empty etc/) from the Flatcar PXE cpio
    echo "  extracting squashfs from initrd..."
    (cd "$SQUASH_WORK" && zcat "$INITRD" | cpio -id --quiet 2>/dev/null)

    # Unpack the squashfs into squashfs-root/ (= Flatcar's /usr/ tree)
    # -no-xattrs: skip SELinux xattrs (require root); Flatcar doesn't enforce SELinux
    echo "  unpacking squashfs ($(du -h "$SQUASH_WORK/usr.squashfs" | cut -f1))..."
    unsquashfs -no-xattrs -d "$SQUASH_WORK/squashfs-root" "$SQUASH_WORK/usr.squashfs" > /dev/null 2>&1

    SR="$SQUASH_WORK/squashfs-root"   # = /usr/ in the live system

    # Install knuckle binary at /usr/bin/knuckle
    cp "$BINARY" "$SR/bin/knuckle"
    chmod 755 "$SR/bin/knuckle"

    # Install systemd units at /usr/lib/systemd/system/
    mkdir -p \
        "$SR/lib/systemd/system/multi-user.target.wants" \
        "$SR/lib/systemd/system/getty@tty1.service.d"

    # Autologin core on tty1 so the TUI gets a real PTY immediately
    cat > "$SR/lib/systemd/system/getty@tty1.service.d/autologin.conf" <<'DROPIN'
[Service]
ExecStart=
ExecStart=-/sbin/agetty --autologin core --noclear %I $TERM
DROPIN

    cat > "$SR/lib/systemd/system/knuckle-installer.service" <<'UNIT'
[Unit]
Description=Knuckle Flatcar Installer
After=multi-user.target network-online.target
Wants=network-online.target
# Conflict with both VGA getty and serial getty so whichever is active gets
# stopped before knuckle claims /dev/console.  The kernel's last console=
# parameter determines which device /dev/console resolves to at runtime.
Conflicts=getty@tty1.service serial-getty@ttyS0.service
ConditionPathExists=/usr/bin/knuckle

[Service]
Type=idle
ExecStart=/usr/bin/knuckle --log-file /tmp/knuckle.log
# tty-force: open /dev/console even if another process briefly holds it.
StandardInput=tty-force
StandardOutput=tty
# /dev/console follows the last console= kernel cmdline parameter:
#   VGA  boot entry (console=ttyS0 console=tty0)  -> tty0  -> VGA display
#   serial boot entry (console=ttyS0)              -> ttyS0 -> serial port
# This single unit therefore works correctly for both boot modes.
TTYPath=/dev/console
TTYReset=yes
TTYVHangup=yes
Restart=on-failure
RestartSec=3

[Install]
WantedBy=multi-user.target
UNIT

    ln -sf /usr/lib/systemd/system/knuckle-installer.service \
        "$SR/lib/systemd/system/multi-user.target.wants/knuckle-installer.service"

    # Repack the squashfs (lz4 for speed; -no-xattrs since we extracted without them)
    echo "  repacking squashfs..."
    mksquashfs "$SR" "$MODIFIED_SQUASH" -noappend -comp lz4 -no-xattrs -quiet

    # Rebuild the initrd cpio: etc/ (empty, from original) + modified usr.squashfs
    echo "  repacking initrd cpio..."
    (
        cd "$SQUASH_WORK"
        cp "$MODIFIED_SQUASH" usr.squashfs
        { echo .; find etc usr.squashfs; } | cpio -o -H newc --quiet | gzip > "$MODIFIED_INITRD"
    )
    rm -rf "$SQUASH_WORK"
    echo "  done: $(du -h "$MODIFIED_INITRD" | cut -f1)"
fi

# ── 4. Build FAT32 ESP with systemd-boot ─────────────────────────────────────
echo "[4/5] Building EFI System Partition (systemd-boot)..."

ISO_DIR="$BUILD_DIR/iso-root"
rm -rf "$ISO_DIR"
mkdir -p "$ISO_DIR"

INITRD_SIZE=$(stat -c%s "$MODIFIED_INITRD")
KERNEL_SIZE=$(stat -c%s "$KERNEL")
ESP_SIZE_MB=$(( (INITRD_SIZE + KERNEL_SIZE + 32*1024*1024) / 1024 / 1024 ))

EFI_IMG="$BUILD_DIR/efi.img"
dd if=/dev/zero of="$EFI_IMG" bs=1M count="$ESP_SIZE_MB" 2>/dev/null
mformat -i "$EFI_IMG" -F ::

# Boot loader
mmd -i "$EFI_IMG" ::/EFI ::/EFI/BOOT
mcopy -i "$EFI_IMG" "$SDBOOT_EFI" "::/EFI/BOOT/${EFI_BOOT_NAME}"

# systemd-boot configuration (BLS)
mmd -i "$EFI_IMG" ::/loader ::/loader/entries

printf 'default knuckle\ntimeout 5\neditor no\n' \
    | mcopy -i "$EFI_IMG" - ::/loader/loader.conf

# Primary entry: VGA console (tty0 is listed last so /dev/console -> tty0)
# Serial output is still mirrored to ttyS0 for diagnostics.
printf 'title   Knuckle \xe2\x80\x93 Install Flatcar Container Linux\nlinux   /vmlinuz\ninitrd  /initrd.img\noptions console=ttyS0,115200n8 console=tty0 systemd.gpt_auto=0 rd.driver.pre=loop\n' \
    | mcopy -i "$EFI_IMG" - ::/loader/entries/knuckle.conf

# Alternate entry: serial only (headless servers)
printf 'title   Knuckle \xe2\x80\x93 Install Flatcar (serial console)\nlinux   /vmlinuz\ninitrd  /initrd.img\noptions console=ttyS0,115200n8 systemd.gpt_auto=0 rd.driver.pre=loop\n' \
    | mcopy -i "$EFI_IMG" - ::/loader/entries/knuckle-serial.conf

mcopy -i "$EFI_IMG" "$KERNEL"          ::/vmlinuz
mcopy -i "$EFI_IMG" "$MODIFIED_INITRD" ::/initrd.img

echo "  ESP : $(du -h "$EFI_IMG" | cut -f1)"

# ── 5. Assemble ISO ───────────────────────────────────────────────────────────
echo "[5/5] Assembling ISO..."

# iso-root/ contains only the ESP image; all content is in the ESP.
cp "$EFI_IMG" "$ISO_DIR/efi.img"

mkdir -p "$OUTPUT_DIR"
ISO_OUT="$OUTPUT_DIR/knuckle-installer-${CHANNEL}-${ARCH}.iso"

xorriso -as mkisofs \
    -o "$ISO_OUT" \
    -R -J -joliet-long \
    -V "KNUCKLE_INSTALL" \
    -eltorito-alt-boot \
    -e efi.img \
    -no-emul-boot \
    --efi-boot-part --efi-boot-image \
    "$ISO_DIR" 2>/dev/null

echo ""
echo "ISO built: $ISO_OUT ($(du -h "$ISO_OUT" | cut -f1))"
echo ""
if [[ "$ARCH" == "arm64" ]]; then
    echo "Test with QEMU (UEFI, arm64):"
    echo "  OVMF=/usr/share/AAVMF/AAVMF_CODE.fd"
    echo "  qemu-system-aarch64 -m 4096 -cpu cortex-a57 -M virt \\"
    echo "    -drive if=pflash,format=raw,readonly=on,file=\$OVMF \\"
    echo "    -cdrom $ISO_OUT \\"
    echo "    -drive if=virtio,file=target.qcow2,format=qcow2 \\"
    echo "    -nographic"
else
    echo "Test with QEMU (UEFI, amd64):"
    echo "  OVMF=/usr/share/OVMF/OVMF_CODE.fd"
    echo "  qemu-system-x86_64 -m 4096 -enable-kvm \\"
    echo "    -drive if=pflash,format=raw,readonly=on,file=\$OVMF \\"
    echo "    -cdrom $ISO_OUT \\"
    echo "    -drive if=virtio,file=target.qcow2,format=qcow2 \\"
    echo "    -nographic"
fi
echo ""
echo "Write to USB:"
echo "  sudo dd if=$ISO_OUT of=/dev/sdX bs=4M status=progress"
