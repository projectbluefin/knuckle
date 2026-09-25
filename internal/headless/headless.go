// Package headless provides a non-interactive install mode that reads
// configuration from a JSON file and runs without the TUI.
package headless

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/projectbluefin/knuckle/internal/bakery"
	"github.com/projectbluefin/knuckle/internal/github"
	"github.com/projectbluefin/knuckle/internal/install"
	"github.com/projectbluefin/knuckle/internal/model"
	"github.com/projectbluefin/knuckle/internal/validate"
)

// Config is the JSON schema for headless install configuration.
// It maps closely to model.InstallConfig but uses simpler types for JSON.
type Config struct {
	OS                  string          `json:"os,omitempty"`   // "flatcar" or "fcos"; defaults to "flatcar"
	Arch                string          `json:"arch,omitempty"` // "amd64" or "arm64"; defaults to "amd64"
	Channel             string          `json:"channel"`
	Version             string          `json:"version,omitempty"`
	Hostname            string          `json:"hostname"`
	Timezone            string          `json:"timezone,omitempty"`
	Network             NetworkConfig   `json:"network"`
	Users               []UserConfig    `json:"users"`
	Disk                string          `json:"disk"`
	Sysexts             []string        `json:"sysexts,omitempty"`
	NvidiaDriverVersion string          `json:"nvidia_driver_version,omitempty"` // e.g. "570-open"; empty = no NVIDIA kernel driver
	Swap                *SwapConfig     `json:"swap,omitempty"`                  // nil = default-on (4 GiB); pass {"enabled":false} to disable
	Tailscale           TailscaleConfig `json:"tailscale,omitempty"`
	UpdateStrategy      string          `json:"update_strategy"`
	IgnitionURL         string          `json:"ignition_url,omitempty"`
	Reboot              bool            `json:"reboot"`
	DryRun              bool            `json:"dry_run,omitempty"`
}

// TailscaleConfig for JSON input. Empty AuthKey skips the integration.
type TailscaleConfig struct {
	AuthKey string `json:"auth_key,omitempty"`
	// Mode: "connect" (default), "exit-node", or "subnet-router".
	Mode string `json:"mode,omitempty"`
	// Routes: comma-separated CIDRs, only used when mode == "subnet-router".
	Routes string `json:"routes,omitempty"`
}

// SwapConfig for JSON input. Default (nil) ⇒ enabled with default size.
// Pass {"enabled": false} to disable, or {"enabled": true, "size_mb": 8192}
// for an explicit size.
type SwapConfig struct {
	Enabled bool `json:"enabled"`
	SizeMB  int  `json:"size_mb,omitempty"` // 0 = use model.DefaultSwapSizeMB
}

// NetworkConfig for JSON input.
type NetworkConfig struct {
	Mode      string   `json:"mode"` // "dhcp" or "static"
	Interface string   `json:"interface,omitempty"`
	Address   string   `json:"address,omitempty"`
	Gateway   string   `json:"gateway,omitempty"`
	DNS       []string `json:"dns,omitempty"`
}

// UserConfig for JSON input.
type UserConfig struct {
	Username   string   `json:"username"`
	Password   string   `json:"password,omitempty"` // expects a crypt hash ($6$, $y$, $2b$, $5$), not plaintext
	SSHKeys    []string `json:"ssh_keys,omitempty"`
	GithubUser string   `json:"github_user,omitempty"`
	Groups     []string `json:"groups,omitempty"`
}

// LoadConfig reads and parses a headless config JSON file.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config JSON: %w", err)
	}

	return &cfg, nil
}

// ToInstallConfig converts a headless Config to a model.InstallConfig.
func (c *Config) ToInstallConfig() *model.InstallConfig {
	os := c.OS
	if os == "" {
		os = model.OSFlatcar
	}

	cfg := &model.InstallConfig{
		OS:       os,
		Arch:     c.Arch,
		Channel:  c.Channel,
		Version:  c.Version,
		Hostname: c.Hostname,
		Timezone: c.Timezone,
		DryRun:   c.DryRun,
	}

	// Set defaults
	if cfg.Arch == "" {
		cfg.Arch = "amd64"
	}
	if cfg.Channel == "" {
		cfg.Channel = "stable"
	}
	if cfg.Timezone == "" {
		cfg.Timezone = "UTC"
	}

	// Network
	switch c.Network.Mode {
	case "static":
		cfg.Network.Mode = model.NetworkStatic
		cfg.Network.Interface = c.Network.Interface
		cfg.Network.Address = c.Network.Address
		cfg.Network.Gateway = c.Network.Gateway
		cfg.Network.DNS = c.Network.DNS
	default:
		cfg.Network.Mode = model.NetworkDHCP
	}

	// Disk
	if c.Disk != "" {
		cfg.Disk = model.DiskInfo{
			DevPath: c.Disk,
			Path:    c.Disk,
		}
	}

	// IgnitionURL (mutually exclusive with local gen)
	cfg.IgnitionURL = c.IgnitionURL

	// NVIDIA kernel driver series (empty = no NVIDIA setup)
	cfg.NvidiaDriverVersion = c.NvidiaDriverVersion

	// Swap: nil = default-on (matches wizard New() default).
	if c.Swap == nil {
		cfg.Swap = model.SwapConfig{Enabled: true, SizeMB: 0}
	} else {
		cfg.Swap = model.SwapConfig{Enabled: c.Swap.Enabled, SizeMB: c.Swap.SizeMB}
	}

	// Tailscale (empty AuthKey skips provisioning)
	if c.Tailscale.AuthKey != "" {
		mode := c.Tailscale.Mode
		if mode == "" {
			mode = model.TailscaleModeConnect
		}
		cfg.Tailscale = model.TailscaleConfig{
			AuthKey: c.Tailscale.AuthKey,
			Mode:    mode,
			Routes:  c.Tailscale.Routes,
		}
	}

	// Users
	for _, u := range c.Users {
		groups := u.Groups
		if len(groups) == 0 {
			groups = []string{"sudo", "docker"}
		}
		user := model.UserConfig{
			Username:     u.Username,
			SSHKeys:      u.SSHKeys,
			PasswordHash: u.Password,
			Groups:       groups,
		}
		cfg.Users = append(cfg.Users, user)
		// Collect SSH keys at config level too
		cfg.SSHKeys = append(cfg.SSHKeys, u.SSHKeys...)
	}

	// Update strategy
	if c.OS == model.OSFCOS {
		// Map headless update_strategy to FCOS zincati strategy
		switch c.UpdateStrategy {
		case "off":
			cfg.UpdateStrategy.FCOSUpdateStrategy = model.FCOSStrategyDisabled
		default: // "reboot", "" → immediate (coreos-installer default)
			cfg.UpdateStrategy.FCOSUpdateStrategy = model.FCOSStrategyImmediate
		}
	} else {
		if c.UpdateStrategy != "" {
			cfg.UpdateStrategy.RebootStrategy = c.UpdateStrategy
		} else {
			cfg.UpdateStrategy.RebootStrategy = "reboot"
		}
	}

	return cfg
}

// resolveSysexts fetches the bakery catalog for the given arch and matches each
// requested sysext name to its catalog entry. Returns error if any name is not found.
func resolveSysexts(ctx context.Context, names []string, client bakery.Client, arch string) ([]model.SysextEntry, error) {
	if len(names) == 0 {
		return nil, nil
	}
	catalog, err := client.FetchCatalogArch(ctx, arch)
	if err != nil {
		return nil, fmt.Errorf("fetching sysext catalog: %w", err)
	}
	index := make(map[string]model.SysextEntry, len(catalog))
	for _, e := range catalog {
		index[e.Name] = e
	}
	var resolved []model.SysextEntry
	for _, name := range names {
		e, ok := index[name]
		if !ok {
			return nil, fmt.Errorf("sysext %q not found in catalog", name)
		}
		e.Selected = true
		resolved = append(resolved, e)
	}
	return resolved, nil
}

// Validate checks the headless config for errors using the same validation
// as the TUI wizard path.
func (c *Config) Validate() error {
	// OS
	if c.OS != "" && c.OS != model.OSFlatcar && c.OS != model.OSFCOS {
		return fmt.Errorf("os: must be %q or %q (got %q)", model.OSFlatcar, model.OSFCOS, c.OS)
	}

	// Arch
	if c.Arch != "" && c.Arch != "amd64" && c.Arch != "arm64" {
		return fmt.Errorf("arch: must be \"amd64\" or \"arm64\" (got %q)", c.Arch)
	}
	// LTS is not available for arm64 on Flatcar (FCOS has no lts stream)
	if c.Arch == "arm64" && c.Channel == "lts" && c.OS != model.OSFCOS {
		return fmt.Errorf("arch: LTS channel is not available for arm64")
	}

	// Channel / Stream
	if c.Channel != "" {
		if c.OS == model.OSFCOS {
			if err := validate.FCOSStream(c.Channel); err != nil {
				return fmt.Errorf("channel: %w", err)
			}
		} else {
			if err := validate.Channel(c.Channel); err != nil {
				return fmt.Errorf("channel: %w", err)
			}
		}
	}

	// Version — FCOS uses a different version scheme and coreos-installer does
	// not support stream-based version pinning in v1; ignore with a warning.
	if c.OS != model.OSFCOS {
		if err := validate.FlatcarVersion(c.Version); err != nil {
			return fmt.Errorf("version: %w", err)
		}
	}

	// Hostname
	if c.Hostname != "" {
		if err := validate.Hostname(c.Hostname); err != nil {
			return fmt.Errorf("hostname: %w", err)
		}
	}

	// Network mode must be recognised
	if c.Network.Mode != "" && c.Network.Mode != "dhcp" && c.Network.Mode != "static" {
		return fmt.Errorf("network mode: must be \"dhcp\" or \"static\" (got %q)", c.Network.Mode)
	}

	// Network (static mode validation) — same contract as the wizard and
	// CheckConsistency via validate.StaticNetwork. In particular the gateway
	// is required, so a config that Validate() accepts cannot later be
	// rejected by the consistency gate in Run.
	if c.Network.Mode == "static" {
		if err := validate.StaticNetwork(model.NetworkConfig{
			Interface: c.Network.Interface,
			Address:   c.Network.Address,
			Gateway:   c.Network.Gateway,
			DNS:       c.Network.DNS,
		}); err != nil {
			return err
		}
	}

	// IgnitionURL format
	if c.IgnitionURL != "" {
		if err := validate.IgnitionURL(c.IgnitionURL); err != nil {
			return fmt.Errorf("ignition_url: %w", err)
		}
	}

	// Disk
	if c.Disk == "" && c.IgnitionURL == "" {
		return fmt.Errorf("disk: required (specify target disk path)")
	}
	if c.Disk != "" {
		if err := validate.DiskPath(c.Disk); err != nil {
			return fmt.Errorf("disk: %w", err)
		}
	}

	// Users (required unless using external ignition URL)
	if c.IgnitionURL == "" {
		if len(c.Users) == 0 {
			return fmt.Errorf("users: at least one user is required")
		}
		seen := make(map[string]bool)
		for i, u := range c.Users {
			if u.Username == "" {
				return fmt.Errorf("users[%d]: username is required", i)
			}
			if err := validate.Username(u.Username); err != nil {
				return fmt.Errorf("users[%d]: %w", i, err)
			}
			if seen[u.Username] {
				return fmt.Errorf("users[%d]: duplicate username %q", i, u.Username)
			}
			seen[u.Username] = true
			if len(u.SSHKeys) == 0 && u.Password == "" && u.GithubUser == "" {
				return fmt.Errorf("users[%d] (%s): must have ssh_keys, password, or github_user", i, u.Username)
			}
			if u.GithubUser != "" {
				if err := validate.GitHubUsername(u.GithubUser); err != nil {
					return fmt.Errorf("users[%d] (%s): github_user: %w", i, u.Username, err)
				}
			}
			if u.Password != "" {
				if err := validate.PasswordHash(u.Password); err != nil {
					return fmt.Errorf("users[%d] (%s): %w", i, u.Username, err)
				}
			}
			for j, key := range u.SSHKeys {
				if err := validate.SSHPublicKey(key); err != nil {
					return fmt.Errorf("users[%d] (%s) ssh_keys[%d]: %w", i, u.Username, j, err)
				}
			}
		}
	}

	// Update strategy
	validStrategies := map[string]bool{"reboot": true, "off": true, "etcd-lock": true, "": true}
	if !validStrategies[c.UpdateStrategy] {
		return fmt.Errorf("update_strategy: must be reboot, off, or etcd-lock (got %q)", c.UpdateStrategy)
	}
	// etcd-lock is Flatcar-only (zincati, used by FCOS, has no equivalent)
	if c.OS == model.OSFCOS && c.UpdateStrategy == "etcd-lock" {
		return fmt.Errorf("update_strategy: etcd-lock is not supported for FCOS (use reboot or off)")
	}

	// Swap size must be within [0, MaxSwapSizeMB]
	if c.Swap != nil {
		if c.Swap.SizeMB < 0 {
			return fmt.Errorf("swap.size_mb: must be ≥ 0 (got %d)", c.Swap.SizeMB)
		}
		if c.Swap.SizeMB > model.MaxSwapSizeMB {
			return fmt.Errorf("swap.size_mb: %d MiB exceeds maximum %d MiB (%.0f GiB)",
				c.Swap.SizeMB, model.MaxSwapSizeMB, float64(model.MaxSwapSizeMB)/1024)
		}
	}

	// Tailscale
	if c.Tailscale.AuthKey != "" {
		if err := validate.TailscaleAuthKey(c.Tailscale.AuthKey); err != nil {
			return fmt.Errorf("tailscale auth_key: %w", err)
		}
		switch c.Tailscale.Mode {
		case "", model.TailscaleModeConnect, model.TailscaleModeExitNode, model.TailscaleModeSubnetRouter:
			// ok
		default:
			return fmt.Errorf("tailscale mode: must be %q, %q, or %q (got %q)",
				model.TailscaleModeConnect, model.TailscaleModeExitNode, model.TailscaleModeSubnetRouter, c.Tailscale.Mode)
		}
		if c.Tailscale.Mode == model.TailscaleModeSubnetRouter {
			if err := validate.TailscaleRoutes(c.Tailscale.Routes); err != nil {
				return fmt.Errorf("tailscale routes: %w", err)
			}
		}
	}

	// NVIDIA driver version must be a known series; not supported on FCOS
	if c.NvidiaDriverVersion != "" {
		if c.OS == model.OSFCOS {
			return fmt.Errorf("nvidia_driver_version: not supported on FCOS")
		}
		valid := false
		for _, opt := range model.NvidiaDriverOptions {
			if opt.ID == c.NvidiaDriverVersion {
				valid = true
				break
			}
		}
		if !valid {
			return fmt.Errorf("nvidia_driver_version: unknown series %q", c.NvidiaDriverVersion)
		}
	}

	return nil
}

// Run executes the headless install flow:
// 1. Validate config
// 2. Resolve GitHub SSH keys (if any)
// 2b. Resolve sysext names → catalog entries (if any)
// 3. Convert to InstallConfig
// 4. Run full validation (consistency check)
// 5. Execute install
// 6. Optionally reboot
func Run(ctx context.Context, cfg *Config, installer install.Installer, logger *slog.Logger) error {
	logger.Info("headless install starting",
		"channel", cfg.Channel,
		"disk", cfg.Disk,
		"hostname", cfg.Hostname,
		"dry_run", cfg.DryRun,
	)

	// Step 1: Validate input config
	fmt.Println("→ Validating configuration...")
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}
	if cfg.OS == model.OSFCOS && cfg.Version != "" {
		logger.Warn("version field is ignored for FCOS (not supported in v1)")
	}
	if cfg.Disk != "" && !cfg.DryRun {
		if err := validateBlockDeviceFunc(cfg.Disk); err != nil {
			return fmt.Errorf("validation failed: disk: %w", err)
		}
	}
	fmt.Println("  ✓ Configuration valid")

	// Step 2: Resolve GitHub SSH keys
	for i, u := range cfg.Users {
		if u.GithubUser != "" {
			fmt.Printf("→ Fetching SSH keys for GitHub user %q...\n", u.GithubUser)
			keys, err := fetchGitHubKeysFunc(ctx, u.GithubUser)
			if err != nil {
				return fmt.Errorf("fetching GitHub keys for %q: %w", u.GithubUser, err)
			}
			if len(keys) == 0 {
				return fmt.Errorf("no SSH keys found for GitHub user %q", u.GithubUser)
			}
			for _, k := range keys {
				if err := validate.SSHPublicKey(k); err != nil {
					return fmt.Errorf("invalid SSH key from GitHub user %q: %w", u.GithubUser, err)
				}
			}
			cfg.Users[i].SSHKeys = append(cfg.Users[i].SSHKeys, keys...)
			fmt.Printf("  ✓ %d key(s) fetched\n", len(keys))
		}
	}

	// Step 2b: Resolve sysext names to catalog entries
	var resolvedSysexts []model.SysextEntry
	if len(cfg.Sysexts) > 0 {
		fmt.Printf("→ Resolving %d sysext(s) from catalog...\n", len(cfg.Sysexts))
		sysextArch := cfg.Arch
		if sysextArch == "" {
			sysextArch = "amd64"
		}
		var serr error
		resolvedSysexts, serr = resolveSysexts(ctx, cfg.Sysexts, newBakeryClientFunc(), sysextArch)
		if serr != nil {
			return fmt.Errorf("resolving sysexts: %w", serr)
		}
		fmt.Printf("  ✓ %d sysext(s) resolved\n", len(resolvedSysexts))
	}

	// Step 3: Convert to InstallConfig
	installCfg := cfg.ToInstallConfig()
	installCfg.Sysexts = resolvedSysexts

	// Step 4: Full consistency check
	fmt.Println("→ Running consistency checks...")
	if err := validate.CheckConsistency(installCfg); err != nil {
		return fmt.Errorf("consistency check failed: %w", err)
	}
	fmt.Println("  ✓ Consistency checks passed")

	// Step 5: Execute install
	fmt.Println("→ Starting installation...")
	startTime := time.Now()

	progress := func(msg string) {
		fmt.Printf("  • %s\n", msg)
		logger.Info("install progress", "step", msg)
	}

	if err := installer.Install(ctx, installCfg, progress); err != nil {
		return fmt.Errorf("installation failed: %w", err)
	}

	elapsed := time.Since(startTime).Round(time.Second)
	fmt.Printf("  ✓ Installation complete (%s)\n", elapsed)

	// Step 6: Reboot
	if cfg.Reboot && !cfg.DryRun {
		fmt.Println("→ Rebooting in 3 seconds...")
		select {
		case <-time.After(rebootDelay):
		case <-ctx.Done():
			return ctx.Err()
		}
		// Reboot is handled by the caller (main.go) via the runner abstraction.
		return nil
	}

	fmt.Println("\n✅ Headless install finished successfully.")
	if cfg.Reboot && cfg.DryRun {
		fmt.Println("   (reboot skipped — dry-run mode)")
	}
	return nil
}

// rebootDelay is the duration Run waits before returning in non-dry-run reboot mode.
// Tests can replace this with a shorter duration to avoid 3-second waits.
var rebootDelay = 3 * time.Second

// validateBlockDeviceFunc checks that a disk path is a real block device.
// Tests can replace this to avoid requiring physical block devices.
var validateBlockDeviceFunc = validate.BlockDevice

// fetchGitHubKeysFunc is the actual implementation used by Run; tests can replace it.
var fetchGitHubKeysFunc = func(ctx context.Context, username string) ([]string, error) {
	return github.NewClient().FetchKeys(ctx, username)
}

// newBakeryClientFunc returns the bakery client used by Run; tests can replace it.
var newBakeryClientFunc = func() bakery.Client {
	return bakery.NewHTTPClient()
}
