#!/usr/bin/env bats
# Tests for scripts/drive-demo.sh: binary discovery, invocation, FIFO
# lifecycle, and the keystroke stream fed to the knuckle TUI.
# Requires: bats-core >= 1.5.0 (https://github.com/bats-core/bats-core)
#
# Run: bats scripts/tests/drive-demo.bats

bats_require_minimum_version 1.5.0

SCRIPT="$BATS_TEST_DIRNAME/../drive-demo.sh"
WORK_DIR=""
MOCK_BIN_DIR=""
ORIGINAL_PATH="$PATH"

setup() {
  local workroot
  workroot="$BATS_TEST_DIRNAME/.bats-work"
  mkdir -p "$workroot"

  WORK_DIR="$workroot/drive-demo-${BATS_TEST_NUMBER:-0}.$$.$RANDOM"
  mkdir -p "$WORK_DIR"

  # Scratch TMPDIR so the FIFO created by `mktemp -u` lands somewhere we
  # can inspect for leftovers, and so parallel test runs don't collide.
  export TMPDIR="$WORK_DIR/tmp"
  mkdir -p "$TMPDIR"

  # Stub `sleep` through PATH so the script's ~15s of `pause` calls
  # (used only to pace a live demo recording) run instantly in tests.
  MOCK_BIN_DIR="$WORK_DIR/mockbin"
  mkdir -p "$MOCK_BIN_DIR"
  cat > "$MOCK_BIN_DIR/sleep" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
  chmod +x "$MOCK_BIN_DIR/sleep"
  export PATH="$MOCK_BIN_DIR:$ORIGINAL_PATH"
}

teardown() {
  export PATH="$ORIGINAL_PATH"
  /bin/rm -rf "$WORK_DIR"
}

# Installs a mock `knuckle` binary at $1 (relative to WORK_DIR) that reads
# stdin (the FIFO) to completion, tees it to CAPTURE_FILE, and exits with
# code $2 (default 0). Reading the FIFO to EOF is required for `send` to
# succeed instead of dying on SIGPIPE.
install_mock_knuckle() {
  local rel_path="$1"
  local exit_code="${2:-0}"
  local full_path="$WORK_DIR/$rel_path"
  mkdir -p "$(dirname "$full_path")"
  cat > "$full_path" <<EOF
#!/usr/bin/env bash
echo "\$@" > "\$ARGS_FILE"
cat > "\$CAPTURE_FILE"
exit $exit_code
EOF
  chmod +x "$full_path"
}

run_in_workdir() {
  cd "$WORK_DIR"
  run bash "$SCRIPT"
  cd "$BATS_TEST_DIRNAME"
}

# ── Binary discovery ─────────────────────────────────────────────────────────

@test "uses bin/knuckle when present" {
  install_mock_knuckle "bin/knuckle"
  export ARGS_FILE="$WORK_DIR/args.txt"
  export CAPTURE_FILE="$WORK_DIR/capture.bin"

  run_in_workdir
  [ "$status" -eq 0 ]
  [ -f "$ARGS_FILE" ]
}

@test "uses ./knuckle when bin/knuckle is absent" {
  install_mock_knuckle "knuckle"
  export ARGS_FILE="$WORK_DIR/args.txt"
  export CAPTURE_FILE="$WORK_DIR/capture.bin"

  run_in_workdir
  [ "$status" -eq 0 ]
  [ -f "$ARGS_FILE" ]
}

@test "bin/knuckle wins when both bin/knuckle and ./knuckle exist" {
  export ARGS_FILE="$WORK_DIR/args.txt"
  export CAPTURE_FILE="$WORK_DIR/capture.bin"
  install_mock_knuckle "bin/knuckle"
  # A second, distinguishable mock at ./knuckle: if this one runs instead,
  # ARGS_FILE_ALT below is created and the test fails.
  export ARGS_FILE_ALT="$WORK_DIR/args-alt.txt"
  cat > "$WORK_DIR/knuckle" <<EOF
#!/usr/bin/env bash
echo "\$@" > "\$ARGS_FILE_ALT"
cat > /dev/null
exit 0
EOF
  chmod +x "$WORK_DIR/knuckle"

  run_in_workdir
  [ "$status" -eq 0 ]
  [ -f "$ARGS_FILE" ]
  [ ! -f "$ARGS_FILE_ALT" ]
}

@test "neither binary present: exit 1 with not-found message and build hint" {
  run_in_workdir
  [ "$status" -eq 1 ]
  [[ "$output" == *"knuckle binary not found"* ]]
  [[ "$output" == *"go build -o bin/knuckle ./cmd/knuckle"* ]]
}

# ── Invocation ────────────────────────────────────────────────────────────────

@test "invokes knuckle with exactly --demo and no other flags" {
  install_mock_knuckle "bin/knuckle"
  export ARGS_FILE="$WORK_DIR/args.txt"
  export CAPTURE_FILE="$WORK_DIR/capture.bin"

  run_in_workdir
  [ "$status" -eq 0 ]
  run cat "$ARGS_FILE"
  [ "$output" = "--demo" ]
}

@test "a non-zero knuckle exit still leaves the script exiting 0" {
  install_mock_knuckle "bin/knuckle" 1
  export ARGS_FILE="$WORK_DIR/args.txt"
  export CAPTURE_FILE="$WORK_DIR/capture.bin"

  run_in_workdir
  [ "$status" -eq 0 ]
}

# ── FIFO lifecycle ────────────────────────────────────────────────────────────

@test "no FIFO is left in TMPDIR after a successful run" {
  install_mock_knuckle "bin/knuckle"
  export ARGS_FILE="$WORK_DIR/args.txt"
  export CAPTURE_FILE="$WORK_DIR/capture.bin"

  run_in_workdir
  [ "$status" -eq 0 ]

  run find "$TMPDIR" -maxdepth 1 -type p
  [ "$status" -eq 0 ]
  [ -z "$output" ]
}

# ── Keystroke stream ──────────────────────────────────────────────────────────

@test "keystroke stream contains 7 down-arrow and 3 up-arrow presses" {
  install_mock_knuckle "bin/knuckle"
  export ARGS_FILE="$WORK_DIR/args.txt"
  export CAPTURE_FILE="$WORK_DIR/capture.bin"

  run_in_workdir
  [ "$status" -eq 0 ]

  local down_count up_count
  down_count=$(grep -o $'\033\[B' "$CAPTURE_FILE" | wc -l)
  up_count=$(grep -o $'\033\[A' "$CAPTURE_FILE" | wc -l)
  [ "$down_count" -eq 7 ]
  [ "$up_count" -eq 3 ]
}

@test "keystroke stream contains 3 space toggles and 2 q presses" {
  install_mock_knuckle "bin/knuckle"
  export ARGS_FILE="$WORK_DIR/args.txt"
  export CAPTURE_FILE="$WORK_DIR/capture.bin"

  run_in_workdir
  [ "$status" -eq 0 ]

  local space_count q_count
  space_count=$(grep -o ' ' "$CAPTURE_FILE" | wc -l)
  q_count=$(grep -o 'q' "$CAPTURE_FILE" | wc -l)
  [ "$space_count" -eq 3 ]
  [ "$q_count" -eq 2 ]
}

@test "escape sequences arrive as real control bytes, not the literal backslash-033" {
  install_mock_knuckle "bin/knuckle"
  export ARGS_FILE="$WORK_DIR/args.txt"
  export CAPTURE_FILE="$WORK_DIR/capture.bin"

  run_in_workdir
  [ "$status" -eq 0 ]

  run grep -o $'\033' "$CAPTURE_FILE"
  [ "$status" -eq 0 ]
  [ "${#lines[@]}" -gt 0 ]

  run grep -c -F '\033' "$CAPTURE_FILE"
  # grep -c prints 0 and exits 1 when there are no matches — either is fine,
  # what matters is that no *literal* backslash-zero-three-three occurs.
  [ "$status" -ne 0 ] || [ "$output" -eq 0 ]
}

@test "keystroke stream terminates in qq" {
  install_mock_knuckle "bin/knuckle"
  export ARGS_FILE="$WORK_DIR/args.txt"
  export CAPTURE_FILE="$WORK_DIR/capture.bin"

  run_in_workdir
  [ "$status" -eq 0 ]

  run tail -c 2 "$CAPTURE_FILE"
  [ "$output" = "qq" ]
}
