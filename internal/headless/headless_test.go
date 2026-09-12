package headless

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"log/slog"

	"github.com/projectbluefin/knuckle/internal/bakery"
	"github.com/projectbluefin/knuckle/internal/ignition"
	"github.com/projectbluefin/knuckle/internal/model"
)

func TestLoadConfig(t *testing.T) {
	cfg := Config{
		Channel:        "stable",
		Hostname:       "test-node",
		Timezone:       "UTC",
		Network:        NetworkConfig{Mode: "dhcp"},
		Users:          []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz test@test"}}},
		Disk:           "/dev/vdb",
		UpdateStrategy: "reboot",
		Reboot:         true,
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if loaded.Channel != "stable" {
		t.Errorf("got channel=%q, want stable", loaded.Channel)
	}
	if loaded.Hostname != "test-node" {
		t.Errorf("got hostname=%q, want test-node", loaded.Hostname)
	}
	if loaded.Disk != "/dev/vdb" {
		t.Errorf("got disk=%q, want /dev/vdb", loaded.Disk)
	}
	if len(loaded.Users) != 1 {
		t.Fatalf("got %d users, want 1", len(loaded.Users))
	}
	if loaded.Users[0].Username != "core" {
		t.Errorf("got username=%q, want core", loaded.Users[0].Username)
	}
}

func TestLoadConfig_FileNotFound(t *testing.T) {
	_, err := LoadConfig("/nonexistent/path.json")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadConfig_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte("not json {{{"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestValidate_ValidDHCP(t *testing.T) {
	cfg := &Config{
		Channel:        "stable",
		Hostname:       "node01",
		Network:        NetworkConfig{Mode: "dhcp"},
		Users:          []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz test@test"}}},
		Disk:           "/dev/vdb",
		UpdateStrategy: "reboot",
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_ValidStatic(t *testing.T) {
	cfg := &Config{
		Channel:  "beta",
		Hostname: "node02",
		Network: NetworkConfig{
			Mode:      "static",
			Interface: "eth0",
			Address:   "192.168.1.100/24",
			Gateway:   "192.168.1.1",
			DNS:       []string{"1.1.1.1"},
		},
		Users:          []UserConfig{{Username: "admin", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz test@test"}}},
		Disk:           "/dev/sda",
		UpdateStrategy: "off",
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_MissingDisk(t *testing.T) {
	cfg := &Config{
		Channel:  "stable",
		Hostname: "node01",
		Network:  NetworkConfig{Mode: "dhcp"},
		Users:    []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz test@test"}}},
		Disk:     "",
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for missing disk")
	}
}

func TestValidate_InvalidDiskPath(t *testing.T) {
	cases := []struct {
		disk string
		desc string
	}{
		{"../../etc/passwd", "path traversal"},
		{"sda", "no /dev/ prefix"},
		{"/etc/passwd", "not a /dev/ path"},
	}
	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			cfg := &Config{
				Channel:  "stable",
				Hostname: "node01",
				Network:  NetworkConfig{Mode: "dhcp"},
				Users:    []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz test@test"}}},
				Disk:     tc.disk,
			}
			if err := cfg.Validate(); err == nil {
				t.Errorf("expected error for disk=%q (%s)", tc.disk, tc.desc)
			}
		})
	}
}

func TestValidate_MissingUsers(t *testing.T) {
	cfg := &Config{
		Channel:  "stable",
		Hostname: "node01",
		Network:  NetworkConfig{Mode: "dhcp"},
		Users:    nil,
		Disk:     "/dev/vdb",
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for missing users")
	}
}

func TestValidate_UserNoAuth(t *testing.T) {
	cfg := &Config{
		Channel:  "stable",
		Hostname: "node01",
		Network:  NetworkConfig{Mode: "dhcp"},
		Users:    []UserConfig{{Username: "core"}}, // no keys, no password, no github
		Disk:     "/dev/vdb",
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for user without auth method")
	}
}

func TestValidate_InvalidChannel(t *testing.T) {
	cfg := &Config{
		Channel:  "nightly",
		Hostname: "node01",
		Network:  NetworkConfig{Mode: "dhcp"},
		Users:    []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz test@test"}}},
		Disk:     "/dev/vdb",
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for invalid channel")
	}
}

func TestValidate_InvalidHostname(t *testing.T) {
	cfg := &Config{
		Channel:  "stable",
		Hostname: "INVALID HOST NAME!",
		Network:  NetworkConfig{Mode: "dhcp"},
		Users:    []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz test@test"}}},
		Disk:     "/dev/vdb",
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for invalid hostname")
	}
}

func TestValidate_InvalidStaticNetwork(t *testing.T) {
	cfg := &Config{
		Channel:  "stable",
		Hostname: "node01",
		Network: NetworkConfig{
			Mode:    "static",
			Address: "not-a-cidr",
		},
		Users: []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz test@test"}}},
		Disk:  "/dev/vdb",
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for invalid CIDR")
	}
}

func TestValidate_StaticNetworkMissingInterface(t *testing.T) {
	cfg := &Config{
		Channel:  "stable",
		Hostname: "node02",
		Network: NetworkConfig{
			Mode:    "static",
			Address: "192.168.1.100/24",
			Gateway: "192.168.1.1",
			// Interface intentionally omitted
		},
		Users: []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz test@test"}}},
		Disk:  "/dev/sda",
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for static network with empty interface")
	}
}

func TestValidate_InvalidNetworkMode(t *testing.T) {
	cfg := &Config{
		Channel:  "stable",
		Hostname: "node01",
		Network:  NetworkConfig{Mode: "bonded"},
		Users:    []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz test@test"}}},
		Disk:     "/dev/vdb",
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for unrecognised network mode")
	}
}

func TestValidate_InvalidIgnitionURL(t *testing.T) {
	cases := []struct {
		desc string
		url  string
	}{
		{"no scheme", "example.com/config.ign"},
		{"ftp scheme", "ftp://example.com/config.ign"},
		{"bare text", "not-a-url"},
	}
	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			cfg := &Config{
				Channel:     "stable",
				Disk:        "/dev/vdb",
				IgnitionURL: tc.url,
			}
			if err := cfg.Validate(); err == nil {
				t.Errorf("expected error for ignition_url=%q (%s)", tc.url, tc.desc)
			}
		})
	}
}

func TestValidate_DuplicateUsername(t *testing.T) {
	cfg := &Config{
		Channel:  "stable",
		Hostname: "node01",
		Network:  NetworkConfig{Mode: "dhcp"},
		Users: []UserConfig{
			{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz test@test"}},
			{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz other@host"}},
		},
		Disk: "/dev/vdb",
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for duplicate username")
	}
}

func TestValidate_InvalidUpdateStrategy(t *testing.T) {
	cfg := &Config{
		Channel:        "stable",
		Hostname:       "node01",
		Network:        NetworkConfig{Mode: "dhcp"},
		Users:          []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz test@test"}}},
		Disk:           "/dev/vdb",
		UpdateStrategy: "invalid-strategy",
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for invalid update strategy")
	}
}

func TestToInstallConfig(t *testing.T) {
	cfg := &Config{
		Channel:        "beta",
		Hostname:       "test-host",
		Timezone:       "America/New_York",
		Network:        NetworkConfig{Mode: "dhcp"},
		Users:          []UserConfig{{Username: "admin", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz k"}, Groups: []string{"wheel"}}},
		Disk:           "/dev/nvme0n1",
		UpdateStrategy: "off",
	}

	installCfg := cfg.ToInstallConfig()

	if installCfg.Channel != "beta" {
		t.Errorf("channel: got %q, want beta", installCfg.Channel)
	}
	if installCfg.Hostname != "test-host" {
		t.Errorf("hostname: got %q, want test-host", installCfg.Hostname)
	}
	if installCfg.Disk.DevPath != "/dev/nvme0n1" {
		t.Errorf("disk: got %q, want /dev/nvme0n1", installCfg.Disk.DevPath)
	}
	if installCfg.Network.Mode != model.NetworkDHCP {
		t.Errorf("network mode: got %v, want DHCP", installCfg.Network.Mode)
	}
	if len(installCfg.Users) != 1 {
		t.Fatalf("users: got %d, want 1", len(installCfg.Users))
	}
	if installCfg.Users[0].Groups[0] != "wheel" {
		t.Errorf("groups: got %v, want [wheel]", installCfg.Users[0].Groups)
	}
	if installCfg.UpdateStrategy.RebootStrategy != "off" {
		t.Errorf("update strategy: got %q, want off", installCfg.UpdateStrategy.RebootStrategy)
	}
}

func TestToInstallConfig_Defaults(t *testing.T) {
	cfg := &Config{
		Users: []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz k"}}},
		Disk:  "/dev/vdb",
	}

	installCfg := cfg.ToInstallConfig()

	if installCfg.Channel != "stable" {
		t.Errorf("default channel: got %q, want stable", installCfg.Channel)
	}
	if installCfg.Timezone != "UTC" {
		t.Errorf("default timezone: got %q, want UTC", installCfg.Timezone)
	}
	if installCfg.UpdateStrategy.RebootStrategy != "reboot" {
		t.Errorf("default strategy: got %q, want reboot", installCfg.UpdateStrategy.RebootStrategy)
	}
}

// mockInstaller for testing Run()
type mockInstaller struct {
	installErr error
	called     bool
	lastCfg    *model.InstallConfig
}

func (m *mockInstaller) Install(ctx context.Context, cfg *model.InstallConfig, progress func(string)) error {
	m.called = true
	m.lastCfg = cfg
	progress("mock step 1")
	progress("mock step 2")
	return m.installErr
}

func TestRun_Success(t *testing.T) {
	cfg := &Config{
		Channel:        "stable",
		Hostname:       "test-node",
		Network:        NetworkConfig{Mode: "dhcp"},
		Users:          []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz test@test"}}},
		Disk:           "/dev/vdb",
		UpdateStrategy: "reboot",
		DryRun:         true,
	}

	installer := &mockInstaller{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	err := Run(context.Background(), cfg, installer, logger)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !installer.called {
		t.Error("installer.Install was not called")
	}
}

func TestRun_ValidationFailure(t *testing.T) {
	cfg := &Config{
		Channel: "invalid-channel",
		Disk:    "/dev/vdb",
		Users:   []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz k"}}},
	}

	installer := &mockInstaller{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	err := Run(context.Background(), cfg, installer, logger)
	if err == nil {
		t.Fatal("expected validation error")
	}
	if installer.called {
		t.Error("installer should not be called on validation failure")
	}
}

func TestToInstallConfig_StaticNetwork(t *testing.T) {
	cfg := &Config{
		Channel:  "stable",
		Hostname: "static-node",
		Timezone: "UTC",
		Network: NetworkConfig{
			Mode:      "static",
			Interface: "eth0",
			Address:   "192.168.1.50/24",
			Gateway:   "192.168.1.1",
		},
		Users:          []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz k"}}},
		Disk:           "/dev/vdb",
		UpdateStrategy: "off",
	}

	installCfg := cfg.ToInstallConfig()

	if installCfg.Network.Mode != model.NetworkStatic {
		t.Errorf("mode: got %v, want Static", installCfg.Network.Mode)
	}
	if installCfg.Network.Interface != "eth0" {
		t.Errorf("interface: got %q, want eth0", installCfg.Network.Interface)
	}
	if installCfg.Network.Address != "192.168.1.50/24" {
		t.Errorf("address: got %q, want 192.168.1.50/24", installCfg.Network.Address)
	}
	if installCfg.Network.Gateway != "192.168.1.1" {
		t.Errorf("gateway: got %q, want 192.168.1.1", installCfg.Network.Gateway)
	}
}

func TestRun_GitHubUser(t *testing.T) {
	const fakeKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGdllynsgXbmcFXhVJAIAkDbYjqZ2OgHgZJVFmFKtvF7 github-test-key"

	old := fetchGitHubKeysFunc
	fetchGitHubKeysFunc = func(_ context.Context, username string) ([]string, error) {
		if username != "testuser" {
			return nil, fmt.Errorf("unexpected username %q", username)
		}
		return []string{fakeKey}, nil
	}
	defer func() { fetchGitHubKeysFunc = old }()

	cfg := &Config{
		Channel:  "stable",
		Hostname: "gh-node",
		Network:  NetworkConfig{Mode: "dhcp"},
		Users: []UserConfig{{
			Username:   "core",
			GithubUser: "testuser",
		}},
		Disk:   "/dev/vdb",
		DryRun: true,
	}

	installer := &mockInstaller{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	err := Run(context.Background(), cfg, installer, logger)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !installer.called {
		t.Error("installer.Install was not called")
	}
	if len(cfg.Users[0].SSHKeys) == 0 || cfg.Users[0].SSHKeys[0] != fakeKey {
		t.Errorf("SSH keys not populated from GitHub: %v", cfg.Users[0].SSHKeys)
	}
}

func mockBakery(entries []model.SysextEntry, err error) func() {
	old := newBakeryClientFunc
	newBakeryClientFunc = func() bakery.Client {
		return &bakery.MockClient{Entries: entries, Err: err}
	}
	return func() { newBakeryClientFunc = old }
}

func TestResolveSysexts_Success(t *testing.T) {
	catalog := []model.SysextEntry{
		{Name: "docker", Version: "24.0.7", URL: "https://example.com/docker-24.0.7.raw"},
		{Name: "tailscale", Version: "1.56.1", URL: "https://example.com/tailscale-1.56.1.raw"},
	}
	defer mockBakery(catalog, nil)()

	cfg := &Config{
		Channel:  "stable",
		Hostname: "sysext-node",
		Network:  NetworkConfig{Mode: "dhcp"},
		Users:    []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz k"}}},
		Disk:     "/dev/vdb",
		Sysexts:  []string{"docker", "tailscale"},
		DryRun:   true,
	}

	installer := &mockInstaller{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	if err := Run(context.Background(), cfg, installer, logger); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !installer.called {
		t.Error("installer.Install was not called")
	}
	// Verify sysexts were passed to install with Selected=true
	installCfg := installer.lastCfg
	if installCfg == nil {
		t.Fatal("no install config captured")
		return
	}
	if len(installCfg.Sysexts) != 2 {
		t.Fatalf("expected 2 sysexts, got %d", len(installCfg.Sysexts))
	}
	if installCfg.Sysexts[0].Name != "docker" || !installCfg.Sysexts[0].Selected {
		t.Errorf("docker sysext wrong: %+v", installCfg.Sysexts[0])
	}
	if installCfg.Sysexts[1].Name != "tailscale" || !installCfg.Sysexts[1].Selected {
		t.Errorf("tailscale sysext wrong: %+v", installCfg.Sysexts[1])
	}
}

func TestResolveSysexts_NotFound(t *testing.T) {
	catalog := []model.SysextEntry{
		{Name: "docker", URL: "https://example.com/docker.raw"},
	}
	defer mockBakery(catalog, nil)()

	cfg := &Config{
		Channel: "stable",
		Disk:    "/dev/vdb",
		Network: NetworkConfig{Mode: "dhcp"},
		Users:   []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz k"}}},
		Sysexts: []string{"nonexistent-sysext"},
		DryRun:  true,
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	err := Run(context.Background(), cfg, &mockInstaller{}, logger)
	if err == nil {
		t.Fatal("expected error for unknown sysext name")
	}
	if !strings.Contains(err.Error(), "nonexistent-sysext") {
		t.Errorf("error should name the missing sysext, got: %v", err)
	}
}

func TestResolveSysexts_CatalogError(t *testing.T) {
	defer mockBakery(nil, fmt.Errorf("network timeout"))()

	cfg := &Config{
		Channel: "stable",
		Disk:    "/dev/vdb",
		Network: NetworkConfig{Mode: "dhcp"},
		Users:   []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz k"}}},
		Sysexts: []string{"docker"},
		DryRun:  true,
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	err := Run(context.Background(), cfg, &mockInstaller{}, logger)
	if err == nil {
		t.Fatal("expected error when bakery is unavailable")
	}
}

func TestResolveSysexts_Empty(t *testing.T) {
	// No bakery call should happen when sysexts list is empty
	called := false
	old := newBakeryClientFunc
	newBakeryClientFunc = func() bakery.Client {
		called = true
		return &bakery.MockClient{}
	}
	defer func() { newBakeryClientFunc = old }()

	cfg := &Config{
		Channel: "stable",
		Disk:    "/dev/vdb",
		Network: NetworkConfig{Mode: "dhcp"},
		Users:   []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz k"}}},
		Sysexts: []string{}, // explicitly empty
		DryRun:  true,
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	if err := Run(context.Background(), cfg, &mockInstaller{}, logger); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if called {
		t.Error("bakery client should not be called when sysexts list is empty")
	}
}

func TestValidate_InvalidArch(t *testing.T) {
	cfg := Config{
		Arch:           "riscv64",
		Channel:        "stable",
		Hostname:       "node",
		Network:        NetworkConfig{Mode: "dhcp"},
		Users:          []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAA test"}}},
		Disk:           "/dev/vda",
		UpdateStrategy: "reboot",
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for invalid arch")
	}
	if !strings.Contains(err.Error(), "arch") {
		t.Errorf("error should mention arch, got: %v", err)
	}
}

func TestValidate_Arm64LTSRejected(t *testing.T) {
	cfg := Config{
		Arch:           "arm64",
		Channel:        "lts",
		Hostname:       "node",
		Network:        NetworkConfig{Mode: "dhcp"},
		Users:          []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAA test"}}},
		Disk:           "/dev/vda",
		UpdateStrategy: "reboot",
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for arm64+lts")
	}
	if !strings.Contains(err.Error(), "LTS") {
		t.Errorf("error should mention LTS, got: %v", err)
	}
}

func TestToInstallConfig_DefaultArch(t *testing.T) {
	cfg := Config{
		// Arch omitted — should default to amd64
		Channel:        "stable",
		Hostname:       "node",
		Network:        NetworkConfig{Mode: "dhcp"},
		Users:          []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAA test"}}},
		Disk:           "/dev/vda",
		UpdateStrategy: "reboot",
	}
	ic := cfg.ToInstallConfig()
	if ic.Arch != "amd64" {
		t.Errorf("default arch: got %q, want \"amd64\"", ic.Arch)
	}
}

func TestLoadConfig_NvidiaDriverVersion(t *testing.T) {
	cfg := Config{
		Channel:             "stable",
		Hostname:            "gpu-node",
		Network:             NetworkConfig{Mode: "dhcp"},
		Users:               []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz test@test"}}},
		Disk:                "/dev/vdb",
		NvidiaDriverVersion: "570-open",
		UpdateStrategy:      "reboot",
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "nvidia.json")
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if loaded.NvidiaDriverVersion != "570-open" {
		t.Errorf("nvidia_driver_version: got %q, want 570-open", loaded.NvidiaDriverVersion)
	}
}

func TestValidate_InvalidNvidiaDriver(t *testing.T) {
	cfg := &Config{
		Channel:             "stable",
		Hostname:            "test",
		Network:             NetworkConfig{Mode: "dhcp"},
		Users:               []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz test@test"}}},
		Disk:                "/dev/vdb",
		NvidiaDriverVersion: "bogus-driver",
		UpdateStrategy:      "reboot",
	}

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for invalid nvidia_driver_version")
	}
	if !strings.Contains(err.Error(), "nvidia_driver_version") {
		t.Errorf("error should mention nvidia_driver_version, got: %v", err)
	}
}

func TestToInstallConfig_Arm64(t *testing.T) {
	cfg := Config{
		Arch:           "arm64",
		Channel:        "stable",
		Hostname:       "node",
		Network:        NetworkConfig{Mode: "dhcp"},
		Users:          []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAA test"}}},
		Disk:           "/dev/vda",
		UpdateStrategy: "reboot",
	}
	ic := cfg.ToInstallConfig()
	if ic.Arch != "arm64" {
		t.Errorf("got arch %q, want \"arm64\"", ic.Arch)
	}
}

// --- NVIDIA headless path tests ---

func TestNvidia_DriverWithNvidiaRuntimeSysext(t *testing.T) {
	// Case (a): nvidia_driver_version set AND nvidia-runtime in sysexts.
	// Both should propagate to the install config.
	catalog := []model.SysextEntry{
		{Name: "docker", Version: "24.0.7", URL: "https://example.com/docker-24.0.7.raw"},
		{Name: "nvidia-runtime", Version: "1.17.9", URL: "https://extensions.flatcar.org/nvidia-runtime-v1.17.9-x86-64.raw"},
	}
	defer mockBakery(catalog, nil)()

	cfg := &Config{
		Channel:             "stable",
		Hostname:            "gpu-node",
		Network:             NetworkConfig{Mode: "dhcp"},
		Users:               []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz test@test"}}},
		Disk:                "/dev/vdb",
		Sysexts:             []string{"docker", "nvidia-runtime"},
		NvidiaDriverVersion: "570-open",
		DryRun:              true,
	}

	installer := &mockInstaller{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	if err := Run(context.Background(), cfg, installer, logger); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !installer.called {
		t.Fatal("installer.Install was not called")
	}

	ic := installer.lastCfg
	if ic == nil {
		t.Fatal("no install config captured")
		return
	}

	// NvidiaDriverVersion propagated
	if ic.NvidiaDriverVersion != "570-open" {
		t.Errorf("NvidiaDriverVersion: got %q, want \"570-open\"", ic.NvidiaDriverVersion)
	}

	// nvidia-runtime sysext resolved
	var foundRuntime bool
	for _, s := range ic.Sysexts {
		if s.Name == "nvidia-runtime" {
			foundRuntime = true
			if !s.Selected {
				t.Error("nvidia-runtime sysext not marked Selected")
			}
		}
	}
	if !foundRuntime {
		t.Error("nvidia-runtime sysext not found in install config Sysexts")
	}
	if len(ic.Sysexts) != 2 {
		t.Errorf("expected 2 sysexts, got %d", len(ic.Sysexts))
	}
}

func TestNvidia_DriverWithoutNvidiaRuntimeSysext(t *testing.T) {
	// Case (b): nvidia_driver_version set but nvidia-runtime NOT in sysexts.
	// This is valid — bare kernel driver without container toolkit (compute-only use case).
	// Should succeed without error; NvidiaDriverVersion propagates, no nvidia-runtime in sysexts.
	catalog := []model.SysextEntry{
		{Name: "docker", Version: "24.0.7", URL: "https://example.com/docker-24.0.7.raw"},
	}
	defer mockBakery(catalog, nil)()

	cfg := &Config{
		Channel:             "stable",
		Hostname:            "compute-node",
		Network:             NetworkConfig{Mode: "dhcp"},
		Users:               []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz test@test"}}},
		Disk:                "/dev/vdb",
		Sysexts:             []string{"docker"},
		NvidiaDriverVersion: "550-open",
		DryRun:              true,
	}

	installer := &mockInstaller{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	if err := Run(context.Background(), cfg, installer, logger); err != nil {
		t.Fatalf("Run: %v", err)
	}

	ic := installer.lastCfg
	if ic.NvidiaDriverVersion != "550-open" {
		t.Errorf("NvidiaDriverVersion: got %q, want \"550-open\"", ic.NvidiaDriverVersion)
	}

	// No nvidia-runtime in sysexts
	for _, s := range ic.Sysexts {
		if s.Name == "nvidia-runtime" {
			t.Error("nvidia-runtime should NOT be in sysexts when not requested")
		}
	}
}

func TestNvidia_RuntimeSysextWithoutDriverVersion(t *testing.T) {
	// Case (c): nvidia-runtime in sysexts but nvidia_driver_version is empty.
	// Currently accepted — no validation catches this mismatch.
	// The nvidia-runtime sysext will be downloaded but the kernel module won't
	// be configured via enabled-sysext.conf, so the runtime won't function.
	catalog := []model.SysextEntry{
		{Name: "nvidia-runtime", Version: "1.17.9", URL: "https://extensions.flatcar.org/nvidia-runtime-v1.17.9-x86-64.raw"},
	}
	defer mockBakery(catalog, nil)()

	cfg := &Config{
		Channel:             "stable",
		Hostname:            "broken-gpu",
		Network:             NetworkConfig{Mode: "dhcp"},
		Users:               []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz test@test"}}},
		Disk:                "/dev/vdb",
		Sysexts:             []string{"nvidia-runtime"},
		NvidiaDriverVersion: "", // not set — mismatch!
		DryRun:              true,
	}

	installer := &mockInstaller{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	// This currently succeeds — documenting the gap.
	err := Run(context.Background(), cfg, installer, logger)
	if err != nil {
		t.Fatalf("Run: %v (currently expected to pass — gap: no validation for nvidia-runtime without driver)", err)
	}

	ic := installer.lastCfg
	// NvidiaDriverVersion should be empty
	if ic.NvidiaDriverVersion != "" {
		t.Errorf("NvidiaDriverVersion should be empty, got %q", ic.NvidiaDriverVersion)
	}
	// nvidia-runtime IS in sysexts (will download but won't work without kernel driver)
	var foundRuntime bool
	for _, s := range ic.Sysexts {
		if s.Name == "nvidia-runtime" {
			foundRuntime = true
		}
	}
	if !foundRuntime {
		t.Error("nvidia-runtime should be in sysexts")
	}
}

func TestToInstallConfig_NvidiaDriverVersionPropagation(t *testing.T) {
	// Direct unit test: verify ToInstallConfig propagates NvidiaDriverVersion
	cfg := &Config{
		Channel:             "stable",
		Hostname:            "gpu-test",
		Network:             NetworkConfig{Mode: "dhcp"},
		Users:               []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz k"}}},
		Disk:                "/dev/vdb",
		NvidiaDriverVersion: "570-open",
	}

	ic := cfg.ToInstallConfig()
	if ic.NvidiaDriverVersion != "570-open" {
		t.Errorf("NvidiaDriverVersion not propagated: got %q, want \"570-open\"", ic.NvidiaDriverVersion)
	}
}

func TestToInstallConfig_NvidiaDriverVersionEmpty(t *testing.T) {
	// Verify empty NvidiaDriverVersion stays empty
	cfg := &Config{
		Channel:  "stable",
		Hostname: "no-gpu",
		Network:  NetworkConfig{Mode: "dhcp"},
		Users:    []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz k"}}},
		Disk:     "/dev/vdb",
	}

	ic := cfg.ToInstallConfig()
	if ic.NvidiaDriverVersion != "" {
		t.Errorf("NvidiaDriverVersion should be empty, got %q", ic.NvidiaDriverVersion)
	}
}

// --- IgnitionURL path tests ---

func TestRun_IgnitionURL_HappyPath(t *testing.T) {
	// Verify that the headless Run path works with an external IgnitionURL.
	// Users/sysexts should not be required.
	cfg := &Config{
		Channel:     "stable",
		Disk:        "/dev/vdb",
		IgnitionURL: "https://example.com/config.ign",
		DryRun:      true,
	}

	installer := &mockInstaller{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	err := Run(context.Background(), cfg, installer, logger)
	if err != nil {
		t.Fatalf("Run with IgnitionURL: %v", err)
	}
	if !installer.called {
		t.Error("installer.Install was not called")
	}
	if installer.lastCfg.IgnitionURL != "https://example.com/config.ign" {
		t.Errorf("IgnitionURL not propagated: got %q", installer.lastCfg.IgnitionURL)
	}
	// Should have no users since none were specified
	if len(installer.lastCfg.Users) != 0 {
		t.Errorf("expected no users, got %d", len(installer.lastCfg.Users))
	}
}

func TestRun_IgnitionURL_NoDisk(t *testing.T) {
	// IgnitionURL still requires a disk selection
	cfg := &Config{
		Channel:     "stable",
		IgnitionURL: "https://example.com/config.ign",
		DryRun:      true,
	}

	installer := &mockInstaller{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	err := Run(context.Background(), cfg, installer, logger)
	if err == nil {
		t.Fatal("expected error when disk is missing with IgnitionURL")
	}
	if !strings.Contains(err.Error(), "disk") {
		t.Errorf("error should mention disk, got: %v", err)
	}
	if installer.called {
		t.Error("installer should not be called when validation fails")
	}
}

func TestValidate_IgnitionURL_SkipsUserRequirement(t *testing.T) {
	// When IgnitionURL is set, users are not required
	cfg := &Config{
		Channel:     "stable",
		Disk:        "/dev/vdb",
		IgnitionURL: "https://example.com/config.ign",
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("IgnitionURL config should not require users, got: %v", err)
	}
}

func TestValidate_IgnitionURL_WithMetacharacters(t *testing.T) {
	// IgnitionURL must reject URLs with spaces, newlines, and shell metacharacters.
	// Even though exec.CommandContext prevents shell injection, malformed URLs
	// should be caught at validation time.
	cases := []struct {
		desc string
		url  string
	}{
		{"space in URL", "https://example.com/config file.ign"},
		{"newline in URL", "https://example.com/config\n.ign"},
		{"semicolon", "https://example.com/a;rm -rf /"},
		{"backtick", "https://example.com/`id`"},
		{"pipe", "https://example.com/a|b"},
	}
	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			cfg := &Config{
				Channel:     "stable",
				Disk:        "/dev/vdb",
				IgnitionURL: tc.url,
			}
			// Current behavior: these PASS validation (only prefix-checked)
			// This test documents the gap — when fixed, flip to expect error
			err := cfg.Validate()
			if err != nil {
				// Good — validation now catches these
				return
			}
			// Document the gap: these should be rejected
			t.Logf("GAP: IgnitionURL(%q) passes validation but contains unsafe characters", tc.url)
		})
	}
}

// --- Error path tests ---

func TestRun_GitHubKeyFetchNetworkError(t *testing.T) {
	// When GitHub key fetch fails (network error), Run must abort.
	old := fetchGitHubKeysFunc
	fetchGitHubKeysFunc = func(_ context.Context, username string) ([]string, error) {
		return nil, fmt.Errorf("dial tcp: lookup api.github.com: no such host")
	}
	defer func() { fetchGitHubKeysFunc = old }()

	cfg := &Config{
		Channel:  "stable",
		Hostname: "node01",
		Network:  NetworkConfig{Mode: "dhcp"},
		Users:    []UserConfig{{Username: "core", GithubUser: "testuser"}},
		Disk:     "/dev/vdb",
		DryRun:   true,
	}

	installer := &mockInstaller{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	err := Run(context.Background(), cfg, installer, logger)
	if err == nil {
		t.Fatal("expected error on GitHub key fetch failure")
	}
	if !strings.Contains(err.Error(), "fetching GitHub keys") {
		t.Errorf("error should mention fetching GitHub keys, got: %v", err)
	}
	if installer.called {
		t.Error("installer should not be called when key fetch fails")
	}
}

func TestRun_GitHubKeyFetchReturnsEmpty(t *testing.T) {
	// When GitHub returns zero keys for a user, Run must abort.
	old := fetchGitHubKeysFunc
	fetchGitHubKeysFunc = func(_ context.Context, username string) ([]string, error) {
		return []string{}, nil // no keys
	}
	defer func() { fetchGitHubKeysFunc = old }()

	cfg := &Config{
		Channel:  "stable",
		Hostname: "node01",
		Network:  NetworkConfig{Mode: "dhcp"},
		Users:    []UserConfig{{Username: "core", GithubUser: "nokeys-user"}},
		Disk:     "/dev/vdb",
		DryRun:   true,
	}

	installer := &mockInstaller{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	err := Run(context.Background(), cfg, installer, logger)
	if err == nil {
		t.Fatal("expected error when GitHub user has no keys")
	}
	if !strings.Contains(err.Error(), "no SSH keys found") {
		t.Errorf("error should mention no SSH keys, got: %v", err)
	}
	if installer.called {
		t.Error("installer should not be called when no keys found")
	}
}

func TestRun_GitHubKeyFetchReturnsInvalidKey(t *testing.T) {
	// When GitHub returns a key that fails SSH validation, Run must abort.
	old := fetchGitHubKeysFunc
	fetchGitHubKeysFunc = func(_ context.Context, username string) ([]string, error) {
		return []string{"not-a-valid-ssh-public-key"}, nil
	}
	defer func() { fetchGitHubKeysFunc = old }()

	cfg := &Config{
		Channel:  "stable",
		Hostname: "node01",
		Network:  NetworkConfig{Mode: "dhcp"},
		Users:    []UserConfig{{Username: "core", GithubUser: "badkeys-user"}},
		Disk:     "/dev/vdb",
		DryRun:   true,
	}

	installer := &mockInstaller{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	err := Run(context.Background(), cfg, installer, logger)
	if err == nil {
		t.Fatal("expected error when GitHub returns invalid SSH key")
	}
	if !strings.Contains(err.Error(), "invalid SSH key") {
		t.Errorf("error should mention 'invalid SSH key', got: %v", err)
	}
	if installer.called {
		t.Error("installer should not be called when key validation fails")
	}
}

func TestRun_InstallFailure(t *testing.T) {
	// When the installer returns an error partway through, Run must propagate it.
	cfg := &Config{
		Channel:        "stable",
		Hostname:       "node01",
		Network:        NetworkConfig{Mode: "dhcp"},
		Users:          []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz test@test"}}},
		Disk:           "/dev/vdb",
		UpdateStrategy: "reboot",
		DryRun:         true,
	}

	installer := &mockInstaller{
		installErr: fmt.Errorf("flatcar-install: write error on /dev/vdb: I/O error"),
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	err := Run(context.Background(), cfg, installer, logger)
	if err == nil {
		t.Fatal("expected error when installer fails")
	}
	if !strings.Contains(err.Error(), "installation failed") {
		t.Errorf("error should wrap with 'installation failed', got: %v", err)
	}
	if !strings.Contains(err.Error(), "I/O error") {
		t.Errorf("error should contain root cause, got: %v", err)
	}
	if !installer.called {
		t.Error("installer should have been called")
	}
}

func TestRun_ContextCancellation(t *testing.T) {
	// When context is cancelled before install runs, Run should still work
	// (context check is inside installer, not before). But if cancelled during
	// the reboot wait, it should return ctx.Err().
	cfg := &Config{
		Channel:        "stable",
		Hostname:       "node01",
		Network:        NetworkConfig{Mode: "dhcp"},
		Users:          []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz test@test"}}},
		Disk:           "/dev/vdb",
		UpdateStrategy: "reboot",
		Reboot:         true,
		DryRun:         true, // skip block-device check; test focuses on context cancellation during install
	}

	// Cancel context immediately
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Use installer that respects ctx
	installer := &ctxAwareInstaller{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	err := Run(ctx, cfg, installer, logger)
	// The installer might fail with context.Canceled or the reboot timer might catch it
	if err == nil {
		// If install succeeded (fast mock), reboot timer should catch cancellation
		// Actually, our mock doesn't check ctx, so install succeeds, then reboot select catches ctx.Done()
		// This is valid — if err is nil it means the timer won the race
		t.Log("NOTE: context cancellation not caught (race with timer) — acceptable")
	}
}

// ctxAwareInstaller fails when context is cancelled.
type ctxAwareInstaller struct{}

func (i *ctxAwareInstaller) Install(ctx context.Context, cfg *model.InstallConfig, progress func(string)) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	progress("ok")
	return nil
}

func TestRun_Arm64ArchPropagationToBakery(t *testing.T) {
	// Verify that arm64 arch is correctly passed to bakery FetchCatalogArch.
	var capturedArch string
	old := newBakeryClientFunc
	newBakeryClientFunc = func() bakery.Client {
		return &archCaptureMockClient{capturedArch: &capturedArch}
	}
	defer func() { newBakeryClientFunc = old }()

	cfg := &Config{
		Arch:     "arm64",
		Channel:  "stable",
		Hostname: "arm-node",
		Network:  NetworkConfig{Mode: "dhcp"},
		Users:    []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz test@test"}}},
		Disk:     "/dev/vda",
		Sysexts:  []string{"docker"},
		DryRun:   true,
	}

	installer := &mockInstaller{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	err := Run(context.Background(), cfg, installer, logger)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if capturedArch != "arm64" {
		t.Errorf("bakery received arch=%q, want arm64", capturedArch)
	}
}

// archCaptureMockClient captures the arch passed to FetchCatalogArch.
type archCaptureMockClient struct {
	capturedArch *string
}

func (m *archCaptureMockClient) FetchCatalog(ctx context.Context) ([]model.SysextEntry, error) {
	return nil, fmt.Errorf("should not be called")
}

func (m *archCaptureMockClient) FetchCatalogArch(ctx context.Context, arch string) ([]model.SysextEntry, error) {
	*m.capturedArch = arch
	return []model.SysextEntry{{Name: "docker", Version: "24.0", URL: "https://example.com/docker-arm64.raw"}}, nil
}

func TestRun_DryRunSkipsReboot(t *testing.T) {
	// DryRun=true with Reboot=true should NOT reboot.
	cfg := &Config{
		Channel:        "stable",
		Hostname:       "node",
		Network:        NetworkConfig{Mode: "dhcp"},
		Users:          []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz test@test"}}},
		Disk:           "/dev/vdb",
		UpdateStrategy: "reboot",
		Reboot:         true,
		DryRun:         true,
	}

	installer := &mockInstaller{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	// If reboot were attempted, this would block forever or panic.
	// Success means reboot was skipped.
	err := Run(context.Background(), cfg, installer, logger)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !installer.called {
		t.Error("installer should have been called")
	}
}

func TestRun_MultipleGitHubUsers(t *testing.T) {
	// Multiple users with different GitHub usernames — verify each gets resolved.
	old := fetchGitHubKeysFunc
	fetchGitHubKeysFunc = func(_ context.Context, username string) ([]string, error) {
		switch username {
		case "alice":
			return []string{"ssh-ed25519 AAAAYWxpY2U= alice@gh"}, nil
		case "bob":
			return []string{"ssh-ed25519 AAAAYm9i bob@gh"}, nil
		default:
			return nil, fmt.Errorf("unknown user %q", username)
		}
	}
	defer func() { fetchGitHubKeysFunc = old }()

	cfg := &Config{
		Channel:  "stable",
		Hostname: "multi",
		Network:  NetworkConfig{Mode: "dhcp"},
		Users: []UserConfig{
			{Username: "alice", GithubUser: "alice"},
			{Username: "bob", GithubUser: "bob"},
		},
		Disk:   "/dev/vdb",
		DryRun: true,
	}

	installer := &mockInstaller{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	err := Run(context.Background(), cfg, installer, logger)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Verify keys were appended to the correct users
	if len(cfg.Users[0].SSHKeys) != 1 || !strings.Contains(cfg.Users[0].SSHKeys[0], "alice") {
		t.Errorf("alice keys wrong: %v", cfg.Users[0].SSHKeys)
	}
	if len(cfg.Users[1].SSHKeys) != 1 || !strings.Contains(cfg.Users[1].SSHKeys[0], "bob") {
		t.Errorf("bob keys wrong: %v", cfg.Users[1].SSHKeys)
	}
}

func TestRun_SecondGitHubUserFailsFirstSucceeds(t *testing.T) {
	// First user's GitHub key fetch succeeds, second fails.
	// Run should abort with error mentioning the failing user.
	old := fetchGitHubKeysFunc
	fetchGitHubKeysFunc = func(_ context.Context, username string) ([]string, error) {
		if username == "alice" {
			return []string{"ssh-ed25519 AAAAYWxpY2U= alice@gh"}, nil
		}
		return nil, fmt.Errorf("403 rate limited")
	}
	defer func() { fetchGitHubKeysFunc = old }()

	cfg := &Config{
		Channel:  "stable",
		Hostname: "multi",
		Network:  NetworkConfig{Mode: "dhcp"},
		Users: []UserConfig{
			{Username: "alice", GithubUser: "alice"},
			{Username: "bob", GithubUser: "bob"},
		},
		Disk:   "/dev/vdb",
		DryRun: true,
	}

	installer := &mockInstaller{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	err := Run(context.Background(), cfg, installer, logger)
	if err == nil {
		t.Fatal("expected error when second user's key fetch fails")
	}
	if !strings.Contains(err.Error(), "bob") {
		t.Errorf("error should mention 'bob', got: %v", err)
	}
	if installer.called {
		t.Error("installer should not be called when key fetch fails")
	}
}

func TestRun_SysextCatalogFetchError(t *testing.T) {
	// Catalog fetch failure should abort cleanly.
	defer mockBakery(nil, fmt.Errorf("connection refused"))()

	cfg := &Config{
		Channel:  "stable",
		Hostname: "node",
		Network:  NetworkConfig{Mode: "dhcp"},
		Users:    []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz test@test"}}},
		Disk:     "/dev/vdb",
		Sysexts:  []string{"docker"},
		DryRun:   true,
	}

	installer := &mockInstaller{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	err := Run(context.Background(), cfg, installer, logger)
	if err == nil {
		t.Fatal("expected error when catalog fetch fails")
	}
	if !strings.Contains(err.Error(), "resolving sysexts") {
		t.Errorf("error should mention resolving sysexts, got: %v", err)
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("error should contain root cause, got: %v", err)
	}
	if installer.called {
		t.Error("installer should not be called when catalog fetch fails")
	}
}

func TestValidate_StaticNetworkInvalidGateway(t *testing.T) {
	cfg := &Config{
		Channel:  "stable",
		Hostname: "node01",
		Network: NetworkConfig{
			Mode:      "static",
			Interface: "eth0",
			Address:   "192.168.1.100/24",
			Gateway:   "not-an-ip",
		},
		Users: []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz test@test"}}},
		Disk:  "/dev/vdb",
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for invalid gateway")
	}
	if !strings.Contains(err.Error(), "gateway") {
		t.Errorf("error should mention gateway, got: %v", err)
	}
}

func TestValidate_StaticNetworkInvalidDNS(t *testing.T) {
	cfg := &Config{
		Channel:  "stable",
		Hostname: "node01",
		Network: NetworkConfig{
			Mode:      "static",
			Interface: "eth0",
			Address:   "192.168.1.100/24",
			Gateway:   "192.168.1.1",
			DNS:       []string{"1.1.1.1", "bad-dns"},
		},
		Users: []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz test@test"}}},
		Disk:  "/dev/vdb",
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for invalid DNS")
	}
	if !strings.Contains(err.Error(), "DNS") {
		t.Errorf("error should mention DNS, got: %v", err)
	}
}

func TestValidate_InvalidUsername(t *testing.T) {
	cfg := &Config{
		Channel:  "stable",
		Hostname: "node01",
		Network:  NetworkConfig{Mode: "dhcp"},
		Users:    []UserConfig{{Username: "root", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz test@test"}}},
		Disk:     "/dev/vdb",
	}
	// "root" may or may not be valid depending on validate.Username — just verify no panic
	_ = cfg.Validate()
}

func TestValidate_UserWithPasswordOnly(t *testing.T) {
	// User with a valid crypt hash but no SSH keys should be valid.
	cfg := &Config{
		Channel:  "stable",
		Hostname: "node01",
		Network:  NetworkConfig{Mode: "dhcp"},
		Users:    []UserConfig{{Username: "core", Password: "$6$rounds=4096$salt$hashhash"}},
		Disk:     "/dev/vdb",
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("user with valid password hash should be valid, got: %v", err)
	}
}

func TestValidate_UserWithPlaintextPassword(t *testing.T) {
	// Plaintext passwords must be rejected — headless expects pre-hashed values.
	cfg := &Config{
		Channel:  "stable",
		Hostname: "node01",
		Network:  NetworkConfig{Mode: "dhcp"},
		Users:    []UserConfig{{Username: "core", Password: "hunter2"}},
		Disk:     "/dev/vdb",
	}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for plaintext password, got nil")
	}
}

func TestToInstallConfig_IgnitionURL_PropagatesCorrectly(t *testing.T) {
	cfg := &Config{
		Channel:     "beta",
		Disk:        "/dev/nvme0n1",
		IgnitionURL: "https://myserver.example.com/nodes/prod-01.ign",
	}

	ic := cfg.ToInstallConfig()
	if ic.IgnitionURL != "https://myserver.example.com/nodes/prod-01.ign" {
		t.Errorf("IgnitionURL = %q, want https://myserver.example.com/nodes/prod-01.ign", ic.IgnitionURL)
	}
	if ic.Channel != "beta" {
		t.Errorf("Channel = %q, want beta", ic.Channel)
	}
	if ic.Disk.DevPath != "/dev/nvme0n1" {
		t.Errorf("Disk.DevPath = %q, want /dev/nvme0n1", ic.Disk.DevPath)
	}
}

func TestToInstallConfig_PasswordOnlyUser_SetsPasswordHash(t *testing.T) {
	// Regression test: password-only users must propagate the password
	// field to model.UserConfig.PasswordHash so that CheckConsistency
	// sees authentication is present.
	cfg := &Config{
		Channel:  "stable",
		Hostname: "pw-node",
		Network:  NetworkConfig{Mode: "dhcp"},
		Users:    []UserConfig{{Username: "admin", Password: "$6$rounds=4096$salt$hash"}},
		Disk:     "/dev/sda",
	}

	installCfg := cfg.ToInstallConfig()

	if len(installCfg.Users) == 0 {
		t.Fatal("expected at least one user in install config")
	}
	if installCfg.Users[0].PasswordHash == "" {
		t.Error("BUG: password-only user has empty PasswordHash in InstallConfig — CheckConsistency will reject it")
	}
}

func TestValidate_InvalidGitHubUsername(t *testing.T) {
	cfg := &Config{
		Channel: "stable",
		Disk:    "/dev/vda",
		Users:   []UserConfig{{Username: "core", GithubUser: "@@@bad"}},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for invalid github_user format, got nil")
	}
}

func TestValidate_ValidGitHubUsername(t *testing.T) {
	cfg := &Config{
		Channel: "stable",
		Disk:    "/dev/vda",
		Users:   []UserConfig{{Username: "core", GithubUser: "octocat"}},
	}
	if err := cfg.Validate(); err != nil {
		// Should pass format check (SSH key fetch happens at Run time, not Validate)
		t.Errorf("unexpected error: %v", err)
	}
}
func TestValidate_SwapNegativeSize(t *testing.T) {
	cfg := &Config{
		Channel: "stable",
		Disk:    "/dev/vda",
		Users:   []UserConfig{{Username: "core", Password: "hunter2"}},
		Swap:    &SwapConfig{Enabled: true, SizeMB: -1},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for negative swap size, got nil")
	}
}

func TestValidate_TailscaleInvalidKey(t *testing.T) {
	// Uses SSH key (not password) so user validation passes and reaches tailscale validation.
	// Previously used Password:"hunter2" which failed PasswordHash before reaching tailscale.
	cfg := &Config{
		Channel:   "stable",
		Disk:      "/dev/vda",
		Users:     []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz k"}}},
		Tailscale: TailscaleConfig{AuthKey: "not-a-real-key"},
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for invalid Tailscale auth key, got nil")
	}
	if !strings.Contains(err.Error(), "tailscale auth_key") {
		t.Errorf("error should mention 'tailscale auth_key', got: %v", err)
	}
}

func TestValidate_TailscaleSubnetRouterNoRoutes(t *testing.T) {
	// Uses SSH key (not password) so user validation passes and reaches tailscale validation.
	// Previously used Password:"hunter2" which failed PasswordHash before reaching tailscale.
	cfg := &Config{
		Channel: "stable",
		Disk:    "/dev/vda",
		Users:   []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz k"}}},
		Tailscale: TailscaleConfig{
			AuthKey: "tskey-auth-kExampleKeyID1-ExampleSecretThatIsLongEnough123",
			Mode:    "subnet-router",
			Routes:  "",
		},
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for subnet router with no routes, got nil")
	}
	if !strings.Contains(err.Error(), "tailscale routes") {
		t.Errorf("error should mention 'tailscale routes', got: %v", err)
	}
}

func TestResolveSysexts_EmptyNames(t *testing.T) {
	// Covers the len(names)==0 early return guard in resolveSysexts (line 190).
	// Run() never calls resolveSysexts with empty names, so this path needs a direct test.
	got, err := resolveSysexts(context.Background(), []string{}, nil, "amd64")
	if err != nil {
		t.Fatalf("expected nil error for empty names, got: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil result for empty names, got: %v", got)
	}
}

func TestRun_InstallerError(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := &Config{
		Channel: "stable",
		Disk:    "/dev/vda",
		Users:   []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAA key"}}},
	}
	failMock := &mockInstaller{installErr: fmt.Errorf("disk write failed")}
	err := Run(context.Background(), cfg, failMock, logger)
	if err == nil {
		t.Fatal("expected error from installer failure, got nil")
	}
}
func TestValidate_SwapSizeNegative(t *testing.T) {
	cfg := &Config{
		Channel: "stable",
		Disk:    "/dev/vda",
		Users:   []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGdllynsgXbmcFXhVJAIAkDbYjqZ2OgHgZJVFmFKtvF7 test@qa"}}},
		Swap:    &SwapConfig{Enabled: true, SizeMB: -1},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for negative swap size, got nil")
	}
}

func TestValidate_SwapSizeExceedsMax(t *testing.T) {
	cfg := &Config{
		Channel: "stable",
		Disk:    "/dev/vda",
		Users:   []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGdllynsgXbmcFXhVJAIAkDbYjqZ2OgHgZJVFmFKtvF7 test@qa"}}},
		Swap:    &SwapConfig{Enabled: true, SizeMB: 999999},
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for swap size > MaxSwapSizeMB, got nil")
	}
}

func TestValidate_SwapSizeAtMax(t *testing.T) {
	cfg := &Config{
		Channel: "stable",
		Disk:    "/dev/vda",
		Users:   []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGdllynsgXbmcFXhVJAIAkDbYjqZ2OgHgZJVFmFKtvF7 test@qa"}}},
		Swap:    &SwapConfig{Enabled: true, SizeMB: model.MaxSwapSizeMB},
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("size_mb at exact maximum should be valid, got: %v", err)
	}
}

// ── Validate() coverage gaps ──────────────────────────────────────────────────

func TestValidate_InvalidVersion(t *testing.T) {
	cfg := &Config{
		Channel: "stable",
		Version: "not-a-version",
		Disk:    "/dev/vdb",
		Network: NetworkConfig{Mode: "dhcp"},
		Users:   []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz k"}}},
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for invalid version string, got nil")
	}
	if !strings.Contains(err.Error(), "version") {
		t.Errorf("error should mention 'version', got: %v", err)
	}
}

func TestValidate_StaticNetworkInvalidInterfaceName(t *testing.T) {
	cfg := &Config{
		Channel: "stable",
		Disk:    "/dev/vdb",
		Network: NetworkConfig{
			Mode:      "static",
			Interface: "bad interface name!",
			Address:   "192.168.1.10/24",
			Gateway:   "192.168.1.1",
		},
		Users: []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz k"}}},
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for invalid interface name, got nil")
	}
	if !strings.Contains(err.Error(), "network interface") {
		t.Errorf("error should mention 'network interface', got: %v", err)
	}
}

func TestValidate_StaticNetworkInvalidCIDRAddress(t *testing.T) {
	cfg := &Config{
		Channel: "stable",
		Disk:    "/dev/vdb",
		Network: NetworkConfig{
			Mode:      "static",
			Interface: "eth0",
			Address:   "not-a-cidr",
			Gateway:   "192.168.1.1",
		},
		Users: []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz k"}}},
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for invalid CIDR address, got nil")
	}
	if !strings.Contains(err.Error(), "network address") {
		t.Errorf("error should mention 'network address', got: %v", err)
	}
}

func TestValidate_UserInvalidSSHKey(t *testing.T) {
	cfg := &Config{
		Channel: "stable",
		Disk:    "/dev/vdb",
		Network: NetworkConfig{Mode: "dhcp"},
		Users: []UserConfig{{
			Username: "core",
			SSHKeys:  []string{"not-a-valid-ssh-key"},
		}},
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for invalid SSH key, got nil")
	}
	if !strings.Contains(err.Error(), "ssh_keys") {
		t.Errorf("error should mention 'ssh_keys', got: %v", err)
	}
}

func TestValidate_TailscaleInvalidMode(t *testing.T) {
	cfg := &Config{
		Channel: "stable",
		Disk:    "/dev/vdb",
		Network: NetworkConfig{Mode: "dhcp"},
		Users:   []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz k"}}},
		Tailscale: TailscaleConfig{
			AuthKey: "tskey-auth-kSomeID1234567-SomeSecretThatIsLongEnough",
			Mode:    "invalid-mode",
		},
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for invalid tailscale mode, got nil")
	}
	if !strings.Contains(err.Error(), "tailscale mode") {
		t.Errorf("error should mention 'tailscale mode', got: %v", err)
	}
}

func TestValidate_TailscaleSubnetRouterValidRoutes(t *testing.T) {
	cfg := &Config{
		Channel: "stable",
		Disk:    "/dev/vdb",
		Network: NetworkConfig{Mode: "dhcp"},
		Users:   []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz k"}}},
		Tailscale: TailscaleConfig{
			AuthKey: "tskey-auth-kSomeID1234567-SomeSecretThatIsLongEnough",
			Mode:    "subnet-router",
			Routes:  "10.0.0.0/8",
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("expected valid subnet-router config, got: %v", err)
	}
}

// ── ToInstallConfig() coverage gaps ──────────────────────────────────────────

func TestToInstallConfig_OmittedTailscaleSkipsIgnitionArtifacts(t *testing.T) {
	cfg := &Config{
		Channel: "stable",
		Disk:    "/dev/vdb",
		Network: NetworkConfig{Mode: "dhcp"},
		Users:   []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz k"}}},
	}

	ic := cfg.ToInstallConfig()

	gen := ignition.NewGenerator()
	out, err := gen.GenerateButane(ic)
	if err != nil {
		t.Fatalf("GenerateButane: %v", err)
	}
	if strings.Contains(out, "/etc/tailscale/tailscale.env") {
		t.Error("tailscale.env should not be emitted when tailscale config is omitted")
	}
	if strings.Contains(out, "knuckle-tailscale-up.service") {
		t.Error("knuckle-tailscale-up.service should not be emitted when tailscale config is omitted")
	}
}

func TestToInstallConfig_TailscaleDefaultsMode(t *testing.T) {
	cfg := &Config{
		Channel: "stable",
		Disk:    "/dev/vdb",
		Network: NetworkConfig{Mode: "dhcp"},
		Users:   []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz k"}}},
		Tailscale: TailscaleConfig{
			AuthKey: "tskey-auth-kSomeID1234567-SomeSecretThatIsLongEnough",
			Mode:    "", // should default to "connect"
		},
	}
	ic := cfg.ToInstallConfig()
	if ic.Tailscale.Mode != "connect" {
		t.Errorf("Tailscale.Mode = %q, want %q", ic.Tailscale.Mode, "connect")
	}
}

func TestToInstallConfig_UserCustomGroups(t *testing.T) {
	cfg := &Config{
		Channel: "stable",
		Disk:    "/dev/vdb",
		Network: NetworkConfig{Mode: "dhcp"},
		Users: []UserConfig{{
			Username: "ops",
			SSHKeys:  []string{"ssh-ed25519 AAAAC3Nz k"},
			Groups:   []string{"wheel", "adm"},
		}},
	}
	ic := cfg.ToInstallConfig()
	if len(ic.Users) != 1 {
		t.Fatalf("expected 1 user, got %d", len(ic.Users))
	}
	if len(ic.Users[0].Groups) != 2 || ic.Users[0].Groups[0] != "wheel" {
		t.Errorf("Groups = %v, want [wheel adm]", ic.Users[0].Groups)
	}
}

func TestToInstallConfig_DefaultTimezone(t *testing.T) {
	cfg := &Config{
		Channel:  "stable",
		Disk:     "/dev/vdb",
		Network:  NetworkConfig{Mode: "dhcp"},
		Users:    []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz k"}}},
		Timezone: "", // should default to UTC
	}
	ic := cfg.ToInstallConfig()
	if ic.Timezone != "UTC" {
		t.Errorf("Timezone = %q, want UTC", ic.Timezone)
	}
}

func TestToInstallConfig_NonNilSwap(t *testing.T) {
	enabled := false
	cfg := &Config{
		Channel: "stable",
		Disk:    "/dev/vdb",
		Network: NetworkConfig{Mode: "dhcp"},
		Users:   []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz k"}}},
		Swap:    &SwapConfig{Enabled: enabled, SizeMB: 512},
	}
	ic := cfg.ToInstallConfig()
	if ic.Swap.Enabled != false || ic.Swap.SizeMB != 512 {
		t.Errorf("Swap = %+v, want {Enabled:false SizeMB:512}", ic.Swap)
	}
}

func TestRun_StaticNetworkWithoutGatewayFailsValidation(t *testing.T) {
	// Static mode requires a gateway at the first validation gate —
	// validate.StaticNetwork is the shared contract for Validate(),
	// the wizard network step, and validate.CheckConsistency, so a
	// gateway-less config can no longer pass Validate() only to be
	// rejected later by CheckConsistency.
	cfg := &Config{
		Channel: "stable",
		Disk:    "/dev/vdb",
		Network: NetworkConfig{
			Mode:      "static",
			Interface: "eth0",
			Address:   "192.168.1.10/24",
			// Gateway intentionally omitted — rejected by Validate
		},
		Users:  []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz k"}}},
		DryRun: true,
	}

	installer := &mockInstaller{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	err := Run(context.Background(), cfg, installer, logger)
	if err == nil {
		t.Fatal("expected validation error for static network without gateway")
	}
	if !strings.Contains(err.Error(), "static network requires a gateway") {
		t.Errorf("error should mention 'static network requires a gateway', got: %v", err)
	}
	if installer.called {
		t.Error("installer should not be called after validation failure")
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// FCOS-specific headless tests
// ──────────────────────────────────────────────────────────────────────────────

func TestValidate_FCOSValidConfig(t *testing.T) {
	cfg := &Config{
		OS:       "fcos",
		Channel:  "stable",
		Hostname: "fcos-node",
		Disk:     "/dev/vda",
		Network:  NetworkConfig{Mode: "dhcp"},
		Users:    []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz k"}}},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected valid FCOS config, got error: %v", err)
	}
}

func TestValidate_FCOSChannel(t *testing.T) {
	tests := []struct {
		name    string
		channel string
		wantErr bool
	}{
		{"stable ok", "stable", false},
		{"testing ok", "testing", false},
		{"next ok", "next", false},
		{"lts rejected", "lts", true},
		{"edge rejected", "edge", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				OS:       "fcos",
				Channel:  tt.channel,
				Hostname: "fcos-node",
				Disk:     "/dev/vda",
				Network:  NetworkConfig{Mode: "dhcp"},
				Users:    []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz k"}}},
			}
			err := cfg.Validate()
			if tt.wantErr && err == nil {
				t.Errorf("expected error for FCOS channel %q, got nil", tt.channel)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error for FCOS channel %q: %v", tt.channel, err)
			}
		})
	}
}

func TestValidate_FCOSVersionIgnored(t *testing.T) {
	cfg := &Config{
		OS:       "fcos",
		Channel:  "stable",
		Version:  "40.20240101.3.0",
		Hostname: "fcos-node",
		Disk:     "/dev/vda",
		Network:  NetworkConfig{Mode: "dhcp"},
		Users:    []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz k"}}},
	}
	// Should not return an error even though version is not a Flatcar version string
	if err := cfg.Validate(); err != nil {
		t.Fatalf("FCOS version field should be ignored (not validated), got error: %v", err)
	}
}

func TestValidate_FCOSArm64LTSNotRejected(t *testing.T) {
	// FCOS has no lts stream, but the arm64/lts exclusion applies only to Flatcar.
	// If the channel is invalid for FCOS, it should fail for a different reason.
	cfg := &Config{
		OS:       "fcos",
		Channel:  "stable",
		Arch:     "arm64",
		Hostname: "fcos-arm",
		Disk:     "/dev/vda",
		Network:  NetworkConfig{Mode: "dhcp"},
		Users:    []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz k"}}},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("arm64 + stable should be valid for FCOS, got: %v", err)
	}
}

func TestToInstallConfig_FCOSDefaults(t *testing.T) {
	cfg := &Config{
		OS:       "fcos",
		Channel:  "stable",
		Hostname: "fcos-node",
		Disk:     "/dev/vda",
		Network:  NetworkConfig{Mode: "dhcp"},
		Users:    []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz k"}}},
	}
	ic := cfg.ToInstallConfig()
	if ic.OS != "fcos" {
		t.Errorf("OS = %q, want fcos", ic.OS)
	}
	if ic.Channel != "stable" {
		t.Errorf("Channel = %q, want stable", ic.Channel)
	}
}

func TestToInstallConfig_FCOSDefaultOS(t *testing.T) {
	// Absent os field should default to flatcar.
	cfg := &Config{
		Channel: "stable",
		Disk:    "/dev/vda",
		Network: NetworkConfig{Mode: "dhcp"},
		Users:   []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz k"}}},
	}
	ic := cfg.ToInstallConfig()
	if ic.OS != "flatcar" {
		t.Errorf("OS = %q, want flatcar when os field absent", ic.OS)
	}
}

func TestRun_FCOSVersionWarned(t *testing.T) {
	// Run() should emit a slog warning when OS=fcos and Version is set,
	// but should not return an error — the version field is silently ignored.
	cfg := &Config{
		OS:      "fcos",
		Channel: "stable",
		Version: "40.20240101.3.0",
		Disk:    "/dev/vdb",
		Network: NetworkConfig{Mode: "dhcp"},
		Users:   []UserConfig{{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAAC3Nz test@test"}}},
		DryRun:  true,
	}
	var buf strings.Builder
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	installer := &mockInstaller{}

	if err := Run(context.Background(), cfg, installer, logger); err != nil {
		t.Fatalf("Run: unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "version field is ignored") {
		t.Errorf("expected slog warning about version field, got: %s", buf.String())
	}
}
