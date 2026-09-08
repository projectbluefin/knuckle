package headless

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/projectbluefin/knuckle/internal/model"
)

// Tests covering previously-uncovered validation error branches in Config.Validate()
// and error paths in Run().

func TestValidate_StaticNetworkMissingAddress(t *testing.T) {
	// Covers the shared static-network contract (validate.StaticNetwork):
	cfg := &Config{
		Channel:  "stable",
		Hostname: "node01",
		Network: NetworkConfig{
			Mode:      "static",
			Interface: "eth0",
			// Address intentionally omitted
			Gateway: "192.168.1.1",
		},
		Users:          []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz test@test"}}},
		Disk:           "/dev/sda",
		UpdateStrategy: "reboot",
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for missing address in static mode")
	}
	if !strings.Contains(err.Error(), "static network requires an IP address") {
		t.Errorf("expected 'static network requires an IP address' in error, got: %v", err)
	}
}

func TestValidate_UserEmptyUsername(t *testing.T) {
	// Covers L297: users[i] username is required
	cfg := &Config{
		Channel:  "stable",
		Hostname: "node01",
		Network:  NetworkConfig{Mode: "dhcp"},
		Users: []UserConfig{
			{Username: "", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz test@test"}},
		},
		Disk:           "/dev/vdb",
		UpdateStrategy: "reboot",
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for empty username")
	}
	if !strings.Contains(err.Error(), "username is required") {
		t.Errorf("expected 'username is required' in error, got: %v", err)
	}
}

func TestValidate_UserInvalidUsernameFormat(t *testing.T) {
	// Covers L300: validate.Username returns error for invalid format
	cfg := &Config{
		Channel:  "stable",
		Hostname: "node01",
		Network:  NetworkConfig{Mode: "dhcp"},
		Users: []UserConfig{
			{Username: "123-INVALID", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz test@test"}},
		},
		Disk:           "/dev/vdb",
		UpdateStrategy: "reboot",
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for invalid username format")
	}
	if !strings.Contains(err.Error(), "users[0]") {
		t.Errorf("expected 'users[0]' in error, got: %v", err)
	}
}

func TestRun_GitHubKeyInvalidSSHKeyReturned(t *testing.T) {
	// Covers L421: validate.SSHPublicKey fails on a key from GitHub
	origFetch := fetchGitHubKeysFunc
	defer func() { fetchGitHubKeysFunc = origFetch }()

	fetchGitHubKeysFunc = func(ctx context.Context, username string) ([]string, error) {
		return []string{"not-a-valid-ssh-key"}, nil
	}

	cfg := &Config{
		Channel:        "stable",
		Hostname:       "node01",
		Network:        NetworkConfig{Mode: "dhcp"},
		Users:          []UserConfig{{Username: "core", GithubUser: "testuser"}},
		Disk:           "/dev/vdb",
		DryRun:         true,
		UpdateStrategy: "reboot",
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	err := Run(context.Background(), cfg, &mockInstaller{}, logger)
	if err == nil {
		t.Fatal("expected error for invalid SSH key from GitHub")
	}
	if !strings.Contains(err.Error(), "invalid SSH key from GitHub user") {
		t.Errorf("expected 'invalid SSH key from GitHub user' in error, got: %v", err)
	}
}

func TestRun_RebootWithContextCancellation(t *testing.T) {
	// Covers L477-485: reboot path when context is cancelled (Reboot=true, DryRun=false)
	origFetch := fetchGitHubKeysFunc
	defer func() { fetchGitHubKeysFunc = origFetch }()
	fetchGitHubKeysFunc = func(ctx context.Context, username string) ([]string, error) {
		return nil, nil
	}

	cfg := &Config{
		Channel:     "stable",
		Hostname:    "node01",
		Network:     NetworkConfig{Mode: "dhcp"},
		IgnitionURL: "https://example.com/config.ign", // bypass disk and user requirements
		Reboot:      true,
		DryRun:      false, // must be false to enter reboot branch
	}

	// Cancel context immediately so the select in reboot catches ctx.Done()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Use a fast installer that skips block device checks
	installer := &skipBlockDeviceInstaller{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	err := Run(ctx, cfg, installer, logger)
	// Should get context.Canceled either from installer or reboot timer
	if err == nil {
		t.Fatal("expected error from cancelled context during reboot wait")
	}
}

func TestRun_RebootDryRunMessage(t *testing.T) {
	// Covers L496: "reboot skipped — dry-run mode" message path
	origFetch := fetchGitHubKeysFunc
	defer func() { fetchGitHubKeysFunc = origFetch }()
	fetchGitHubKeysFunc = func(ctx context.Context, username string) ([]string, error) {
		return nil, nil
	}

	cfg := &Config{
		Channel:        "stable",
		Hostname:       "node01",
		Network:        NetworkConfig{Mode: "dhcp"},
		Users:          []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz test@test"}}},
		Disk:           "/dev/vdb",
		Reboot:         true,
		DryRun:         true,
		UpdateStrategy: "reboot",
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	err := Run(context.Background(), cfg, &mockInstaller{}, logger)
	if err != nil {
		t.Fatalf("expected no error for dry-run reboot path, got: %v", err)
	}
}

// skipBlockDeviceInstaller always succeeds regardless of context state,
// allowing subsequent reboot logic to run.
type skipBlockDeviceInstaller struct{}

func (s *skipBlockDeviceInstaller) Install(_ context.Context, _ *model.InstallConfig, progress func(string)) error {
	progress("mock install")
	return nil
}

func TestValidate_NewOSAndArchValidation(t *testing.T) {
	// 1. Arm64 + LTS on Flatcar should fail
	cfgArmLTS := &Config{
		OS:             "flatcar",
		Arch:           "arm64",
		Channel:        "lts",
		Hostname:       "node01",
		Network:        NetworkConfig{Mode: "dhcp"},
		Users:          []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz test@test"}}},
		Disk:           "/dev/vdb",
		UpdateStrategy: "reboot",
	}
	err := cfgArmLTS.Validate()
	if err == nil {
		t.Fatal("expected error for arm64 + LTS channel on Flatcar")
	}
	if !strings.Contains(err.Error(), "LTS channel is not available for arm64") {
		t.Errorf("expected LTS warning for arm64, got: %v", err)
	}

	// 2. FCOS stream success
	cfgFCOSSuccess := &Config{
		OS:             "fcos",
		Arch:           "amd64",
		Channel:        "stable",
		Hostname:       "node01",
		Network:        NetworkConfig{Mode: "dhcp"},
		Users:          []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz test@test"}}},
		Disk:           "/dev/vdb",
		UpdateStrategy: "reboot",
	}
	if err := cfgFCOSSuccess.Validate(); err != nil {
		t.Errorf("expected no error for FCOS stable channel, got: %v", err)
	}

	// 3. FCOS stream failure (LTS does not exist in FCOS)
	cfgFCOSFail := &Config{
		OS:             "fcos",
		Arch:           "amd64",
		Channel:        "lts",
		Hostname:       "node01",
		Network:        NetworkConfig{Mode: "dhcp"},
		Users:          []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz test@test"}}},
		Disk:           "/dev/vdb",
		UpdateStrategy: "reboot",
	}
	err = cfgFCOSFail.Validate()
	if err == nil {
		t.Fatal("expected error for FCOS + LTS channel")
	}
	if !strings.Contains(err.Error(), "channel: invalid FCOS stream") {
		t.Errorf("expected invalid FCOS stream error, got: %v", err)
	}

	// 4. Invalid OS
	cfgBadOS := &Config{
		OS:             "nixos",
		Arch:           "amd64",
		Channel:        "stable",
		Hostname:       "node01",
		Network:        NetworkConfig{Mode: "dhcp"},
		Users:          []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz test@test"}}},
		Disk:           "/dev/vdb",
		UpdateStrategy: "reboot",
	}
	err = cfgBadOS.Validate()
	if err == nil {
		t.Fatal("expected error for invalid OS")
	}
	if !strings.Contains(err.Error(), "os: must be") {
		t.Errorf("expected OS validation error, got: %v", err)
	}

	// 5. Flatcar channel failure
	cfgFlatcarFail := &Config{
		OS:             "flatcar",
		Arch:           "amd64",
		Channel:        "invalid-channel",
		Hostname:       "node01",
		Network:        NetworkConfig{Mode: "dhcp"},
		Users:          []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz test@test"}}},
		Disk:           "/dev/vdb",
		UpdateStrategy: "reboot",
	}
	err = cfgFlatcarFail.Validate()
	if err == nil {
		t.Fatal("expected error for invalid Flatcar channel")
	}
	if !strings.Contains(err.Error(), "channel: invalid channel") {
		t.Errorf("expected invalid channel error, got: %v", err)
	}
}

func TestToInstallConfig_OSPropagation(t *testing.T) {
	tests := []struct {
		name    string
		inputOS string
		wantOS  string
	}{
		{"empty defaults to flatcar", "", model.OSFlatcar},
		{"flatcar passes through", model.OSFlatcar, model.OSFlatcar},
		{"fcos passes through", model.OSFCOS, model.OSFCOS},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				OS:       tt.inputOS,
				Channel:  "stable",
				Hostname: "node01",
				Network:  NetworkConfig{Mode: "dhcp"},
				Users:    []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz test@test"}}},
				Disk:     "/dev/vdb",
			}
			ic := cfg.ToInstallConfig()
			if ic.OS != tt.wantOS {
				t.Errorf("ToInstallConfig().OS = %q, want %q", ic.OS, tt.wantOS)
			}
		})
	}
}

// fcosBase returns a minimal valid FCOS headless Config for use in test helpers.
func fcosBase() *Config {
	return &Config{
		OS:       "fcos",
		Channel:  "stable",
		Hostname: "fcos-node",
		Network:  NetworkConfig{Mode: "dhcp"},
		Users:    []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz test@test"}}},
		Disk:     "/dev/vdb",
	}
}

func TestValidate_FCOSVersionPassthrough(t *testing.T) {
	// FCOS version strings do not match the Flatcar semver regex (MAJOR.MINOR.PATCH).
	// Validate() must not call FlatcarVersion for FCOS — it should accept any non-empty
	// version string without error (the FCOSInstaller will warn and ignore it).
	cfg := fcosBase()
	cfg.Version = "38.20230514.3.0"
	if err := cfg.Validate(); err != nil {
		t.Errorf("FCOS with version string should pass Validate(), got: %v", err)
	}
}

func TestValidate_FCOSNvidiaRejected(t *testing.T) {
	cfg := fcosBase()
	cfg.NvidiaDriverVersion = "570-open"
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for nvidia_driver_version on FCOS, got nil")
	}
	if !strings.Contains(err.Error(), "nvidia_driver_version") {
		t.Errorf("expected nvidia_driver_version error, got: %v", err)
	}
}

func TestValidate_FCOSEtcdLockRejected(t *testing.T) {
	cfg := fcosBase()
	cfg.UpdateStrategy = "etcd-lock"
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for etcd-lock on FCOS, got nil")
	}
	if !strings.Contains(err.Error(), "etcd-lock") {
		t.Errorf("expected etcd-lock error, got: %v", err)
	}
}

func TestValidate_FCOSUpdateStrategyOffOK(t *testing.T) {
	cfg := fcosBase()
	cfg.UpdateStrategy = "off"
	if err := cfg.Validate(); err != nil {
		t.Errorf("FCOS update_strategy=off should pass Validate(), got: %v", err)
	}
}

func TestValidate_FCOSUpdateStrategyRebootOK(t *testing.T) {
	cfg := fcosBase()
	cfg.UpdateStrategy = "reboot"
	if err := cfg.Validate(); err != nil {
		t.Errorf("FCOS update_strategy=reboot should pass Validate(), got: %v", err)
	}
}

func TestToInstallConfig_FCOSUpdateStrategyMapping(t *testing.T) {
	tests := []struct {
		name           string
		updateStrategy string
		wantFCOS       string
		wantFlatcar    string
	}{
		{"fcos reboot → immediate", "reboot", model.FCOSStrategyImmediate, ""},
		{"fcos empty → immediate", "", model.FCOSStrategyImmediate, ""},
		{"fcos off → disabled", "off", model.FCOSStrategyDisabled, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := fcosBase()
			cfg.UpdateStrategy = tt.updateStrategy
			ic := cfg.ToInstallConfig()
			if ic.UpdateStrategy.FCOSUpdateStrategy != tt.wantFCOS {
				t.Errorf("FCOSUpdateStrategy = %q, want %q", ic.UpdateStrategy.FCOSUpdateStrategy, tt.wantFCOS)
			}
		})
	}
}

func TestToInstallConfig_FlatcarUpdateStrategyUnchanged(t *testing.T) {
	cfg := &Config{
		OS:             "flatcar",
		Channel:        "stable",
		Hostname:       "node",
		Network:        NetworkConfig{Mode: "dhcp"},
		Users:          []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz test@test"}}},
		Disk:           "/dev/vdb",
		UpdateStrategy: "etcd-lock",
	}
	ic := cfg.ToInstallConfig()
	if ic.UpdateStrategy.RebootStrategy != "etcd-lock" {
		t.Errorf("Flatcar RebootStrategy = %q, want etcd-lock", ic.UpdateStrategy.RebootStrategy)
	}
	if ic.UpdateStrategy.FCOSUpdateStrategy != "" {
		t.Errorf("Flatcar FCOSUpdateStrategy should be empty, got %q", ic.UpdateStrategy.FCOSUpdateStrategy)
	}
}
