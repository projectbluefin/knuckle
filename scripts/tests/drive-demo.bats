#!/usr/bin/env bats
# Tests for scripts/drive-demo.sh — binary discovery, FIFO plumbing, and the
# keystroke sequence it feeds to `knuckle --demo`.
# Requires: bats-core (https://github.com/bats-core/bats-core)
#
# Run: bats scripts/tests/drive-demo.bats

SCRIPT="$BATS_TEST_DIRNAME/../drive-demo.sh"

setup() {
  WORKDIR="$(mktemp -d)"
  STUBDIR="$WORKDIR/stub"
  mkdir -p "$STUBDIR" "$WORKDIR/bin"

  # `pause` calls sleep for every step (~20s of real waits). Stub it out so the
  # suite stays fast; the script resolves sleep through PATH.
  cat >"$STUBDIR/sleep" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
  chmod +x "$STUBDIR/sleep"
}

teardown() {
  rm -rf "$WORKDIR"
}

# Write a fake knuckle at $1 that records its argv and stdin under $WORKDIR.
make_fake_knuckle() {
  local path="$1" tag="$2"
  cat >"$path" <<EOF
#!/usr/bin/env bash
echo "$tag" > "$WORKDIR/which-binary"
printf '%s\n' "\$@" > "$WORKDIR/argv"
cat > "$WORKDIR/stdin.bin"
exit 0
EOF
  chmod +x "$path"
}

run_drive_demo() {
  run env PATH="$STUBDIR:$PATH" bash -c "cd '$WORKDIR' && bash '$SCRIPT'"
}

# ── Binary discovery ─────────────────────────────────────────────────────────

@test "missing knuckle binary exits 1 with a build hint on stderr" {
  run env PATH="$STUBDIR:$PATH" bash -c "cd '$WORKDIR' && bash '$SCRIPT' 2>&1 1>/dev/null"
  [ "$status" -eq 1 ]
  [[ "$output" == *"knuckle binary not found"* ]]
  [[ "$output" == *"go build -o bin/knuckle ./cmd/knuckle"* ]]
}

@test "bin/knuckle is used when present" {
  make_fake_knuckle "$WORKDIR/bin/knuckle" "bin"
  run_drive_demo
  [ "$status" -eq 0 ]
  [ "$(cat "$WORKDIR/which-binary")" = "bin" ]
}

@test "./knuckle is used when bin/knuckle is absent" {
  make_fake_knuckle "$WORKDIR/knuckle" "cwd"
  run_drive_demo
  [ "$status" -eq 0 ]
  [ "$(cat "$WORKDIR/which-binary")" = "cwd" ]
}

@test "bin/knuckle wins over ./knuckle when both exist" {
  make_fake_knuckle "$WORKDIR/bin/knuckle" "bin"
  make_fake_knuckle "$WORKDIR/knuckle" "cwd"
  run_drive_demo
  [ "$status" -eq 0 ]
  [ "$(cat "$WORKDIR/which-binary")" = "bin" ]
}

@test "an unexecutable bin/knuckle is still selected, and the run dies on SIGPIPE" {
  # The discovery loop tests -f, not -x, so an unexecutable file is selected.
  # The exec then fails, nothing reads the FIFO, and the first send is killed by
  # SIGPIPE (128+13=141). This test documents current behavior; see the
  # accompanying issue for the -x check and trap-based FIFO cleanup.
  : >"$WORKDIR/bin/knuckle"
  run_drive_demo
  [ "$status" -eq 141 ]
  [[ "$output" != *"knuckle binary not found"* ]]
}

# ── Invocation ───────────────────────────────────────────────────────────────

@test "knuckle is invoked with --demo and no other flags" {
  make_fake_knuckle "$WORKDIR/bin/knuckle" "bin"
  run_drive_demo
  [ "$status" -eq 0 ]
  [ "$(cat "$WORKDIR/argv")" = "--demo" ]
}

@test "script exits 0 even when knuckle exits non-zero" {
  cat >"$WORKDIR/bin/knuckle" <<'EOF'
#!/usr/bin/env bash
cat >/dev/null
exit 7
EOF
  chmod +x "$WORKDIR/bin/knuckle"
  run_drive_demo
  [ "$status" -eq 0 ]
}

# ── FIFO lifecycle ───────────────────────────────────────────────────────────

@test "no FIFO is left behind after a successful run" {
  make_fake_knuckle "$WORKDIR/bin/knuckle" "bin"
  before="$(find "${TMPDIR:-/tmp}" -maxdepth 1 -type p 2>/dev/null | wc -l)"
  run_drive_demo
  [ "$status" -eq 0 ]
  after="$(find "${TMPDIR:-/tmp}" -maxdepth 1 -type p 2>/dev/null | wc -l)"
  [ "$before" -eq "$after" ]
}

# ── Keystroke sequence ───────────────────────────────────────────────────────

@test "the driven keystrokes match the documented demo walkthrough" {
  make_fake_knuckle "$WORKDIR/bin/knuckle" "bin"
  run_drive_demo
  [ "$status" -eq 0 ]

  # 3 downs browsing the welcome cards + 4 walking the sysext list.
  [ "$(grep -ao $'\033\[B' "$WORKDIR/stdin.bin" | wc -l)" -eq 7 ]
  [ "$(grep -ao $'\033\[A' "$WORKDIR/stdin.bin" | wc -l)" -eq 3 ]
  # Three sysexts toggled with space.
  [ "$(grep -ao ' ' "$WORKDIR/stdin.bin" | wc -l)" -eq 3 ]
  # Two 'q' presses quit without confirming the install.
  [ "$(grep -ao 'q' "$WORKDIR/stdin.bin" | wc -l)" -eq 2 ]
}

@test "escape sequences are emitted as real control bytes, not literal backslash-033" {
  make_fake_knuckle "$WORKDIR/bin/knuckle" "bin"
  run_drive_demo
  [ "$status" -eq 0 ]
  ! grep -q '\\033' "$WORKDIR/stdin.bin"
  grep -q $'\033' "$WORKDIR/stdin.bin"
}

@test "the input stream ends with the two quit presses" {
  make_fake_knuckle "$WORKDIR/bin/knuckle" "bin"
  run_drive_demo
  [ "$status" -eq 0 ]
  [ "$(tail -c 2 "$WORKDIR/stdin.bin")" = "qq" ]
}
