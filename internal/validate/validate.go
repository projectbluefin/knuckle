// Package validate provides input validation functions for the knuckle TUI installer.
package validate

import (
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"regexp"
	"strings"
	"syscall"

	"github.com/projectbluefin/knuckle/internal/model"
)

// Compiled regex patterns — evaluated once at init to catch malformed patterns early.
var (
	reHostname       = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?$`)
	reUsername       = regexp.MustCompile(`^[a-z_][a-z0-9_-]*$`)
	reTimezone       = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_/+-]*$`)
	reGroupName      = regexp.MustCompile(`^[a-z_][a-z0-9_-]*$`)
	reInterfaceName  = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)
	reGitHubUsername = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]{0,37}[a-zA-Z0-9])?$`)
	reFlatcarVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	reSysextName     = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)
	// Tailscale auth keys: tskey-auth-<id12>-<secret32+>; tskey-client- variant also accepted.
	// See https://tailscale.com/kb/1085/auth-keys for the format.
	reTailscaleAuthKey = regexp.MustCompile(`^tskey-(auth|client)-[A-Za-z0-9]{10,}-[A-Za-z0-9]{20,}$`)
)

// Hostname validates a Linux hostname (RFC 1123).
// Must be 1-63 characters, alphanumeric plus hyphens, no leading/trailing hyphen, no dots.
func Hostname(s string) error {
	if s == "" {
		return fmt.Errorf("hostname cannot be empty")
	}
	if len(s) > 63 {
		return fmt.Errorf("hostname too long (max 63 characters)")
	}
	if !reHostname.MatchString(s) {
		return fmt.Errorf("invalid hostname %q: must be alphanumeric with optional hyphens, no leading/trailing hyphen", s)
	}
	return nil
}

// IPAddress validates an IPv4 address (without CIDR).
func IPAddress(s string) error {
	ip := net.ParseIP(s)
	if ip == nil || ip.To4() == nil {
		return fmt.Errorf("invalid IPv4 address: %s", s)
	}
	return nil
}

// CIDR validates an IPv4 CIDR notation (e.g., 192.168.1.10/24).
func CIDR(s string) error {
	ip, _, err := net.ParseCIDR(s)
	if err != nil {
		return fmt.Errorf("invalid CIDR: %s", s)
	}
	if ip.To4() == nil {
		return fmt.Errorf("invalid CIDR: only IPv4 is supported")
	}
	return nil
}

// Gateway validates a gateway IPv4 address.
func Gateway(s string) error {
	return IPAddress(s)
}

// DNSServer validates a DNS server address.
func DNSServer(s string) error {
	return IPAddress(s)
}

// SSHPublicKey validates an SSH public key format.
// Must have at least "type base64data"; an optional comment is allowed.
func SSHPublicKey(s string) error {
	parts := strings.Fields(s)
	if len(parts) < 2 {
		return fmt.Errorf("invalid SSH key format")
	}
	validTypes := []string{
		"ssh-rsa",
		"ssh-ed25519",
		"ssh-dss",
		"ecdsa-sha2-nistp256",
		"ecdsa-sha2-nistp384",
		"ecdsa-sha2-nistp521",
		"sk-ssh-ed25519@openssh.com",
		"sk-ecdsa-sha2-nistp256@openssh.com",
	}
	validType := false
	for _, t := range validTypes {
		if parts[0] == t {
			validType = true
			break
		}
	}
	if !validType {
		return fmt.Errorf("unsupported SSH key type: %s", parts[0])
	}
	if _, err := base64.StdEncoding.DecodeString(parts[1]); err != nil {
		return fmt.Errorf("invalid SSH key: base64 payload is malformed")
	}
	return nil
}

// Username validates a Linux username.
// Must be 1-32 characters, start with a letter or underscore, contain only
// lowercase alphanumeric, underscore, or hyphen.
func Username(s string) error {
	if s == "" {
		return fmt.Errorf("username cannot be empty")
	}
	if len(s) > 32 {
		return fmt.Errorf("username too long (max 32 characters)")
	}
	if !reUsername.MatchString(s) {
		return fmt.Errorf("invalid username: must start with letter or underscore, contain only lowercase alphanumeric, underscore, or hyphen")
	}
	return nil
}

// DiskPath validates a disk device path.
// Must start with /dev/ and contain no path traversal.
func DiskPath(s string) error {
	if !strings.HasPrefix(s, "/dev/") {
		return fmt.Errorf("disk path must start with /dev/")
	}
	if len(s) <= 5 {
		return fmt.Errorf("disk path too short")
	}
	if strings.Contains(s, "..") {
		return fmt.Errorf("disk path must not contain path traversal")
	}
	return nil
}

// BlockDevice checks that path exists on the host and is a block device.
// Call this after DiskPath() in contexts where the local filesystem is available
// (headless mode, pre-install checks). Do not call from pure format validators.
func BlockDevice(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("disk %s not found — check available disks with `lsblk`", path)
		}
		return fmt.Errorf("disk %s: %w", path, err)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Mode&syscall.S_IFMT != syscall.S_IFBLK {
		return fmt.Errorf("disk %s is not a block device", path)
	}
	return nil
}

// Channel validates a Flatcar release channel.
func Channel(s string) error {
	valid := []string{"stable", "beta", "alpha", "lts", "edge"}
	for _, v := range valid {
		if s == v {
			return nil
		}
	}
	return fmt.Errorf("invalid channel %q: must be one of stable, beta, alpha, lts, edge", s)
}

// FCOSStream validates a Fedora CoreOS stream name.
func FCOSStream(s string) error {
	valid := []string{"stable", "testing", "next"}
	for _, v := range valid {
		if s == v {
			return nil
		}
	}
	return fmt.Errorf("invalid FCOS stream %q: must be one of stable, testing, next", s)
}

// URL validates a basic URL format (must start with http:// or https://).
func URL(s string) error {
	if !strings.HasPrefix(s, "http://") && !strings.HasPrefix(s, "https://") {
		return fmt.Errorf("URL must start with http:// or https://")
	}
	return nil
}

// IgnitionURL validates a remote Ignition config URL.
// Requires https:// for remote URLs (Ignition configs may contain secrets).
// Also allows file:// for local configs.
// Rejects URLs with whitespace or shell metacharacters.
func IgnitionURL(s string) error {
	if s == "" {
		return fmt.Errorf("ignition URL must not be empty")
	}
	if strings.HasPrefix(s, "http://") {
		return fmt.Errorf("ignition URL must use https:// (config may contain secrets); got http://")
	}
	if !strings.HasPrefix(s, "https://") && !strings.HasPrefix(s, "file://") {
		return fmt.Errorf("ignition URL must start with https:// or file://")
	}
	// Reject URLs with whitespace or shell metacharacters.
	// While exec.CommandContext prevents shell injection, malformed URLs
	// would fail at fetch time — reject early with a clear error.
	for _, c := range s {
		if c <= ' ' || c == '`' || c == '|' || c == ';' || c == '&' || c == '$' || c == '(' || c == ')' || c == '\'' || c == '"' || c == '\\' || c == '{' || c == '}' || c == '<' || c == '>' {
			return fmt.Errorf("ignition URL contains invalid character %q", c)
		}
	}
	return nil
}

// NonEmpty validates that a string is not empty after trimming whitespace.
func NonEmpty(field, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s cannot be empty", field)
	}
	return nil
}

// Timezone validates a timezone string (e.g. "America/New_York").
// Empty is OK (defaults to UTC).
func Timezone(tz string) error {
	if tz == "" {
		return nil // empty is OK, defaults to UTC
	}
	if !reTimezone.MatchString(tz) {
		return fmt.Errorf("invalid timezone %q: must match [A-Za-z_][A-Za-z0-9_/+-]*", tz)
	}
	return nil
}

// GroupName validates a Linux group name.
func GroupName(name string) error {
	if name == "" {
		return fmt.Errorf("group name cannot be empty")
	}
	if !reGroupName.MatchString(name) {
		return fmt.Errorf("invalid group name %q: must match [a-z_][a-z0-9_-]*", name)
	}
	return nil
}

// CheckConsistency validates the overall config for conflicting settings.
func CheckConsistency(cfg *model.InstallConfig) error {
	// NVIDIA driver version is Flatcar-only regardless of ignition_url mode
	if cfg.OS == model.OSFCOS && cfg.NvidiaDriverVersion != "" {
		return fmt.Errorf("nvidia_driver_version: not supported on FCOS")
	}

	// External Ignition URL mode: only disk is required, skip auth/network checks
	if cfg.IgnitionURL != "" {
		if cfg.Disk.DevPath == "" {
			return fmt.Errorf("no disk selected")
		}
		return nil
	}
	// Static network requires gateway and interface
	if cfg.Network.Mode == model.NetworkStatic {
		if err := StaticNetwork(cfg.Network); err != nil {
			return err
		}
	}
	// Must have at least one auth method; reject duplicate usernames
	hasSSH := len(cfg.SSHKeys) > 0
	hasPassword := false
	seenUsers := make(map[string]bool)
	for _, u := range cfg.Users {
		if len(u.SSHKeys) > 0 {
			hasSSH = true
		}
		if u.PasswordHash != "" {
			hasPassword = true
		}
		if seenUsers[u.Username] {
			return fmt.Errorf("duplicate username %q", u.Username)
		}
		seenUsers[u.Username] = true
	}
	if !hasSSH && !hasPassword {
		return fmt.Errorf("at least one authentication method required (SSH key or password)")
	}
	// Disk must be selected
	if cfg.Disk.DevPath == "" {
		return fmt.Errorf("no disk selected")
	}
	// Channel must be valid
	if cfg.Channel == "" {
		return fmt.Errorf("no channel selected")
	}

	// NVIDIA driver version must be a known series if set.
	if cfg.NvidiaDriverVersion != "" {
		valid := false
		for _, opt := range model.NvidiaDriverOptions {
			if opt.ID == cfg.NvidiaDriverVersion {
				valid = true
				break
			}
		}
		if !valid {
			return fmt.Errorf("unknown NVIDIA driver series %q", cfg.NvidiaDriverVersion)
		}
	}

	return nil
}

// StaticNetwork validates a static-mode network configuration: interface,
// address and gateway are all required and must be well-formed, and every
// DNS server must be a valid IP address. It is the single source of truth
// for the static-network contract — consumed by the wizard network step,
// headless config validation, and CheckConsistency.
func StaticNetwork(n model.NetworkConfig) error {
	// Presence checks keep CheckConsistency's original gateway/interface/address
	// order so existing error expectations are preserved.
	if n.Gateway == "" {
		return fmt.Errorf("static network requires a gateway")
	}
	if n.Interface == "" {
		return fmt.Errorf("static network requires an interface name")
	}
	if n.Address == "" {
		return fmt.Errorf("static network requires an IP address")
	}
	if err := InterfaceName(n.Interface); err != nil {
		return fmt.Errorf("network interface: %w", err)
	}
	if err := CIDR(n.Address); err != nil {
		return fmt.Errorf("network address: %w", err)
	}
	if err := Gateway(n.Gateway); err != nil {
		return fmt.Errorf("gateway: %w", err)
	}
	for _, dns := range n.DNS {
		if err := DNSServer(dns); err != nil {
			return fmt.Errorf("DNS server %q: %w", dns, err)
		}
	}
	return nil
}

// GitHubUsername validates a GitHub username.
// GitHub rules: 1-39 chars, alphanumeric and hyphens, no consecutive hyphens,
// cannot start or end with a hyphen.
func GitHubUsername(s string) error {
	if s == "" {
		return fmt.Errorf("GitHub username cannot be empty")
	}
	if len(s) > 39 {
		return fmt.Errorf("GitHub username too long (max 39 characters)")
	}
	if !reGitHubUsername.MatchString(s) {
		return fmt.Errorf("invalid GitHub username %q: must be alphanumeric with hyphens, cannot start or end with a hyphen", s)
	}
	if strings.Contains(s, "--") {
		return fmt.Errorf("invalid GitHub username %q: consecutive hyphens not allowed", s)
	}
	return nil
}

// TailscaleAuthKey validates a Tailscale auth key (tskey-auth-… or tskey-client-…).
// Rejects obviously malformed input early — the real check is Tailscale's API at
// `tailscale up` time, but failing here gives the user immediate feedback in the TUI.
func TailscaleAuthKey(s string) error {
	if s == "" {
		return fmt.Errorf("auth key cannot be empty")
	}
	if !reTailscaleAuthKey.MatchString(s) {
		return fmt.Errorf("auth key must start with %q or %q and contain an id and secret", "tskey-auth-", "tskey-client-")
	}
	return nil
}

// FlatcarVersion validates a pinned Flatcar version string.
// Must be empty (latest) or in MAJOR.MINOR.PATCH format.
func FlatcarVersion(s string) error {
	if s == "" {
		return nil // empty = use latest
	}
	if !reFlatcarVersion.MatchString(s) {
		return fmt.Errorf("invalid Flatcar version %q: must be MAJOR.MINOR.PATCH (e.g. 3510.2.8)", s)
	}
	return nil
}

// PasswordHash validates a Linux shadow-format password hash.
// Must start with a known crypt prefix ($6$, $y$, $2b$, $5$) or be empty.
func PasswordHash(s string) error {
	if s == "" {
		return nil
	}
	valid := []string{"$6$", "$y$", "$2b$", "$5$"}
	for _, prefix := range valid {
		if strings.HasPrefix(s, prefix) {
			return nil
		}
	}
	return fmt.Errorf("password_hash must be a valid crypt hash (starting with $6$, $y$, $2b$, or $5$); got plain text or unsupported format")
}

// SysextName validates a sysext extension name used in Butane/Ignition paths.
// Allows alphanumeric, hyphens, and underscores — no dots, slashes, or path traversal.
func SysextName(s string) error {
	if s == "" {
		return fmt.Errorf("sysext name cannot be empty")
	}
	if !reSysextName.MatchString(s) {
		return fmt.Errorf("invalid sysext name %q: must contain only alphanumeric, hyphens, or underscores", s)
	}
	return nil
}

// TailscaleRoutes validates a comma-separated list of CIDRs for --advertise-routes.
// At least one non-empty valid CIDR must be present; all-comma inputs like "," are rejected.
func TailscaleRoutes(s string) error {
	if strings.TrimSpace(s) == "" {
		return fmt.Errorf("at least one route is required for subnet router mode")
	}
	found := 0
	for _, raw := range strings.Split(s, ",") {
		r := strings.TrimSpace(raw)
		if r == "" {
			continue
		}
		if err := CIDR(r); err != nil {
			return fmt.Errorf("route %q: %w", r, err)
		}
		found++
	}
	if found == 0 {
		return fmt.Errorf("at least one route is required for subnet router mode")
	}
	return nil
}

// InterfaceName validates a Linux network interface name.
// Must be 1-15 characters, alphanumeric plus dots, hyphens, underscores.
// No path traversal or special characters.
func InterfaceName(s string) error {
	if s == "" {
		return fmt.Errorf("interface name cannot be empty")
	}
	if len(s) > 15 {
		return fmt.Errorf("interface name too long (max 15 characters)")
	}
	if !reInterfaceName.MatchString(s) {
		return fmt.Errorf("invalid interface name %q: must contain only alphanumeric, dots, hyphens, or underscores", s)
	}
	return nil
}
