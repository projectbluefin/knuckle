package validate

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/projectbluefin/knuckle/internal/model"
)

func TestHostname(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid simple", "myhost", false},
		{"valid with hyphen", "my-host", false},
		{"valid with numbers", "host123", false},
		{"valid single char", "a", false},
		{"valid mixed case", "MyHost", false},
		{"valid max length", strings.Repeat("a", 63), false},
		{"empty", "", true},
		{"too long", strings.Repeat("a", 64), true},
		{"leading hyphen", "-host", true},
		{"trailing hyphen", "host-", true},
		{"contains dot", "my.host", true},
		{"contains space", "my host", true},
		{"contains underscore", "my_host", true},
		{"only hyphen", "-", true},
		{"special chars", "host!", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Hostname(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("Hostname(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestIPAddress(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid", "192.168.1.1", false},
		{"valid loopback", "127.0.0.1", false},
		{"valid zeros", "0.0.0.0", false},
		{"valid broadcast", "255.255.255.255", false},
		{"invalid empty", "", true},
		{"invalid text", "notanip", true},
		{"invalid octet", "192.168.1.256", true},
		{"ipv6 rejected", "::1", true},
		{"ipv6 full rejected", "2001:db8::1", true},
		{"cidr not allowed", "192.168.1.1/24", true},
		{"trailing dot", "192.168.1.1.", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := IPAddress(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("IPAddress(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestCIDR(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid /24", "192.168.1.0/24", false},
		{"valid /32", "10.0.0.1/32", false},
		{"valid /8", "10.0.0.0/8", false},
		{"valid host in subnet", "192.168.1.100/24", false},
		{"invalid no mask", "192.168.1.0", true},
		{"invalid empty", "", true},
		{"invalid text", "notacidr", true},
		{"invalid mask", "192.168.1.0/33", true},
		{"ipv6 rejected", "2001:db8::/32", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CIDR(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("CIDR(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestSSHPublicKey(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid ed25519", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGdllynsgXbmcFXhVJAIAkDbYjqZ2OgHgZJVFmFKtvF7 user@host", false},
		{"valid rsa", "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQDjTid/Xxpik4yiFwhLdPiIkG28XBLeIqvb0/nAwjyYJU8KU7qy91tGCFf/09D3VTnJbp3jfrOwboxb4iL+BiowC5bhbdJtHkQ89tx/xDw8ljrOx025UWp6EvOrD+rk7Aw4kYnLJ0CA5MvzdgVOal0brgHIpw34hbrP/yPNdv/H8VMsZBT+pXDQP0JcGe0K8HRM54cn/xIrSYnUvEZBb+kpscPXJtUGFNDSFxFp7fPhlViYLxDuNQtRgc7u3mAMuLMbxI6JxkIsvZ14PxxFTQ4Vq+BnJEazHgFn3wz86dHqanwx/sE9bBWsk7fhV2rfWpI1WI4KaTVfgeFaJ404VRkP user@host", false},
		{"valid ecdsa", "ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTY= comment", false},
		{"valid no comment", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGdllynsgXbmcFXhVJAIAkDbYjqZ2OgHgZJVFmFKtvF7", false},
		{"valid sk key", "sk-ssh-ed25519@openssh.com AAAAG3NrLXNzaC1lZDI1NTE5 user", false},
		{"invalid empty", "", true},
		{"invalid no data", "ssh-ed25519", true},
		{"invalid type", "ssh-invalid AAAAC3NzaC1lZDI1NTE5AAAAIGdllynsgXbmcFXhVJAIAkDbYjqZ2OgHgZJVFmFKtvF7", true},
		{"invalid just text", "notakey", true},
		{"invalid base64 payload", "ssh-ed25519 not!valid@base64 user@host", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := SSHPublicKey(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("SSHPublicKey(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestUsername(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid simple", "jorge", false},
		{"valid underscore start", "_admin", false},
		{"valid with hyphen", "my-user", false},
		{"valid with numbers", "user01", false},
		{"valid underscore", "my_user", false},
		{"valid single char", "a", false},
		{"empty", "", true},
		{"too long", strings.Repeat("a", 33), true},
		{"starts with number", "1user", true},
		{"starts with hyphen", "-user", true},
		{"uppercase", "Admin", true},
		{"contains dot", "my.user", true},
		{"contains space", "my user", true},
		{"special chars", "user!", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Username(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("Username(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestDiskPath(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid sda", "/dev/sda", false},
		{"valid nvme", "/dev/nvme0n1", false},
		{"valid by-id", "/dev/disk/by-id/scsi-0QEMU_QEMU_HARDDISK", false},
		{"valid vda", "/dev/vda", false},
		{"invalid empty", "", true},
		{"invalid no prefix", "sda", true},
		{"invalid just prefix", "/dev/", true},
		{"invalid relative", "dev/sda", true},
		{"invalid other path", "/sys/block/sda", true},
		{"invalid path traversal", "/dev/../etc/passwd", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := DiskPath(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("DiskPath(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestBlockDevice_NotExist(t *testing.T) {
	err := BlockDevice("/dev/knuckle-nonexistent-device-xyz")
	if err == nil {
		t.Fatal("expected error for non-existent device, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should mention 'not found', got: %v", err)
	}
}

func TestBlockDevice_NotABlockDevice(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "not-a-block-device-*.txt")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	err = BlockDevice(f.Name())
	if err == nil {
		t.Fatal("expected error for regular file, got nil")
	}
	if !strings.Contains(err.Error(), "not a block device") {
		t.Errorf("error should mention 'not a block device', got: %v", err)
	}
}

func TestBlockDevice_StatError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "no-access", "dev")
	err := BlockDevice(path)
	if err == nil {
		t.Fatal("expected error for inaccessible path, got nil")
	}
}

func TestBlockDevice_PermissionDenied(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("permission checks are bypassed as root")
	}
	dir := t.TempDir()
	subdir := filepath.Join(dir, "locked")
	if err := os.Mkdir(subdir, 0o700); err != nil {
		t.Fatal(err)
	}
	inner := filepath.Join(subdir, "device")
	if err := os.WriteFile(inner, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// Remove all permissions so os.Stat(inner) returns EACCES, not ENOENT.
	if err := os.Chmod(subdir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(subdir, 0o700) })

	err := BlockDevice(inner)
	if err == nil {
		t.Fatal("expected error for permission-denied stat, got nil")
	}
	// Must NOT say "not found" — that would mean IsNotExist hit instead.
	if strings.Contains(err.Error(), "not found") {
		t.Errorf("expected generic stat error, got not-found message: %v", err)
	}
}

func TestBlockDevice_Success(t *testing.T) {
	// Find any block device that stat(2) can read (no read permission needed).
	candidates := []string{"/dev/sda", "/dev/vda", "/dev/nvme0n1", "/dev/loop0", "/dev/zram0"}
	var blkdev string
	for _, dev := range candidates {
		info, err := os.Stat(dev)
		if err != nil {
			continue
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if ok && st.Mode&syscall.S_IFMT == syscall.S_IFBLK {
			blkdev = dev
			break
		}
	}
	if blkdev == "" {
		t.Skip("no accessible block device found for test (CI may have none)")
	}
	if err := BlockDevice(blkdev); err != nil {
		t.Errorf("BlockDevice(%q) = %v, want nil", blkdev, err)
	}
}

func TestChannel(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"stable", "stable", false},
		{"beta", "beta", false},
		{"alpha", "alpha", false},
		{"edge", "edge", false},
		{"invalid empty", "", true},
		{"invalid uppercase", "Stable", true},
		{"invalid unknown", "nightly", true},
		{"valid lts", "lts", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Channel(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("Channel(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestFCOSStream(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"stable", "stable", false},
		{"testing", "testing", false},
		{"next", "next", false},
		{"invalid empty", "", true},
		{"invalid beta", "beta", true},
		{"invalid edge", "edge", true},
		{"invalid lts", "lts", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := FCOSStream(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("FCOSStream(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestURL(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid http", "http://example.com", false},
		{"valid https", "https://example.com/path", false},
		{"valid https with port", "https://example.com:8080/api", false},
		{"invalid empty", "", true},
		{"invalid no scheme", "example.com", true},
		{"invalid ftp", "ftp://example.com", true},
		{"invalid just text", "notaurl", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := URL(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("URL(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestIgnitionURL(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid https", "https://example.com/config.ign", false},
		{"valid https with path", "https://myserver.example.com/nodes/prod-01.ign", false},
		{"valid file", "file:///etc/ignition/config.ign", false},
		{"rejects http", "http://example.com/config.ign", true},
		{"rejects empty", "", true},
		{"rejects bare path", "/etc/config.ign", true},
		{"rejects ftp", "ftp://example.com/config.ign", true},
		// Metacharacter tests — these document current permissive behavior
		// flatcar-install receives them via argv (no shell injection possible)
		// but they are still malformed URLs that will fail at fetch time
		{"space in URL", "https://example.com/config file.ign", true},
		{"newline in URL", "https://example.com/config\n.ign", true},
		{"backtick in URL", "https://example.com/`id`", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := IgnitionURL(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("IgnitionURL(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestNonEmpty(t *testing.T) {
	tests := []struct {
		name    string
		field   string
		value   string
		wantErr bool
	}{
		{"valid", "name", "hello", false},
		{"valid with spaces", "name", "  hello  ", false},
		{"empty string", "name", "", true},
		{"only spaces", "name", "   ", true},
		{"only tabs", "name", "\t\t", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := NonEmpty(tt.field, tt.value)
			if (err != nil) != tt.wantErr {
				t.Errorf("NonEmpty(%q, %q) error = %v, wantErr %v", tt.field, tt.value, err, tt.wantErr)
			}
		})
	}
}

func TestTimezone(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"empty is OK", "", false},
		{"valid US", "America/New_York", false},
		{"valid Europe", "Europe/Berlin", false},
		{"valid UTC", "UTC", false},
		{"valid offset style", "Etc/GMT+5", false},
		{"valid underscore", "America/North_Dakota/Center", false},
		{"invalid space", "America/New York", true},
		{"invalid newline", "America\n/New_York", true},
		{"invalid semicolon", "UTC;rm -rf /", true},
		{"starts with number", "1UTC", true},
		{"starts with slash", "/etc/localtime", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Timezone(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("Timezone(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestGroupName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid simple", "docker", false},
		{"valid with underscore", "wheel_users", false},
		{"valid with hyphen", "libvirt-users", false},
		{"valid starts underscore", "_shadow", false},
		{"empty", "", true},
		{"starts with number", "1docker", true},
		{"starts with hyphen", "-docker", true},
		{"uppercase", "Docker", true},
		{"has space", "my group", true},
		{"has newline", "group\nname", true},
		{"special chars", "group;rm", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := GroupName(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("GroupName(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestInterfaceName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid eth0", "eth0", false},
		{"valid ens3", "ens3", false},
		{"valid with dot", "eth0.100", false},
		{"valid with hyphen", "veth-abc", false},
		{"valid with underscore", "bond_0", false},
		{"valid max length", strings.Repeat("a", 15), false},
		{"too long", strings.Repeat("a", 16), true},
		{"empty", "", true},
		{"starts with dot", ".eth0", true},
		{"starts with hyphen", "-eth0", true},
		{"contains space", "eth 0", true},
		{"contains slash", "eth/0", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := InterfaceName(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("InterfaceName(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestCheckConsistency(t *testing.T) {
	validConfig := func() *model.InstallConfig {
		return &model.InstallConfig{
			Channel: "stable",
			SSHKeys: []string{"ssh-ed25519 AAAA test@host"},
			Disk: model.DiskInfo{
				DevPath: "/dev/sda",
			},
			Network: model.NetworkConfig{
				Mode: model.NetworkDHCP,
			},
		}
	}

	tests := []struct {
		name    string
		modify  func(cfg *model.InstallConfig)
		wantErr string
	}{
		{
			name:   "valid config passes",
			modify: func(cfg *model.InstallConfig) {},
		},
		{
			name: "no disk selected",
			modify: func(cfg *model.InstallConfig) {
				cfg.Disk.DevPath = ""
			},
			wantErr: "no disk selected",
		},
		{
			name: "no channel selected",
			modify: func(cfg *model.InstallConfig) {
				cfg.Channel = ""
			},
			wantErr: "no channel selected",
		},
		{
			name: "no auth method",
			modify: func(cfg *model.InstallConfig) {
				cfg.SSHKeys = nil
			},
			wantErr: "at least one authentication method required",
		},
		{
			name: "user SSH key counts as auth",
			modify: func(cfg *model.InstallConfig) {
				cfg.SSHKeys = nil
				cfg.Users = []model.UserConfig{
					{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAA"}},
				}
			},
		},
		{
			name: "user password counts as auth",
			modify: func(cfg *model.InstallConfig) {
				cfg.SSHKeys = nil
				cfg.Users = []model.UserConfig{
					{Username: "core", PasswordHash: "$6$rounds=..."},
				}
			},
		},
		{
			name: "static network missing gateway",
			modify: func(cfg *model.InstallConfig) {
				cfg.Network.Mode = model.NetworkStatic
				cfg.Network.Interface = "eth0"
				cfg.Network.Address = "10.0.0.5/24"
			},
			wantErr: "static network requires a gateway",
		},
		{
			name: "static network missing interface",
			modify: func(cfg *model.InstallConfig) {
				cfg.Network.Mode = model.NetworkStatic
				cfg.Network.Gateway = "10.0.0.1"
				cfg.Network.Address = "10.0.0.5/24"
			},
			wantErr: "static network requires an interface name",
		},
		{
			name: "static network missing address",
			modify: func(cfg *model.InstallConfig) {
				cfg.Network.Mode = model.NetworkStatic
				cfg.Network.Gateway = "10.0.0.1"
				cfg.Network.Interface = "eth0"
			},
			wantErr: "static network requires an IP address",
		},
		{
			name: "valid static network",
			modify: func(cfg *model.InstallConfig) {
				cfg.Network.Mode = model.NetworkStatic
				cfg.Network.Gateway = "10.0.0.1"
				cfg.Network.Interface = "eth0"
				cfg.Network.Address = "10.0.0.5/24"
			},
		},
		{
			name: "duplicate username rejected",
			modify: func(cfg *model.InstallConfig) {
				cfg.SSHKeys = nil
				cfg.Users = []model.UserConfig{
					{Username: "core", SSHKeys: []string{"ssh-ed25519 AAAA"}},
					{Username: "core", SSHKeys: []string{"ssh-ed25519 BBBB"}},
				}
			},
			wantErr: "duplicate username",
		},
		{
			name: "external ignition URL skips auth check",
			modify: func(cfg *model.InstallConfig) {
				cfg.IgnitionURL = "https://example.com/config.ign"
				cfg.SSHKeys = nil
				cfg.Users = nil
				cfg.Channel = ""
			},
		},
		{
			name: "external ignition URL still requires disk",
			modify: func(cfg *model.InstallConfig) {
				cfg.IgnitionURL = "https://example.com/config.ign"
				cfg.SSHKeys = nil
				cfg.Users = nil
				cfg.Disk.DevPath = ""
			},
			wantErr: "no disk selected",
		},
		{
			name: "invalid nvidia driver version",
			modify: func(cfg *model.InstallConfig) {
				cfg.NvidiaDriverVersion = "bogus"
			},
			wantErr: "unknown NVIDIA driver series",
		},
		{
			name: "valid nvidia driver version",
			modify: func(cfg *model.InstallConfig) {
				cfg.NvidiaDriverVersion = "570-open"
			},
			wantErr: "",
		},
		{
			name: "FCOS nvidia driver version rejected",
			modify: func(cfg *model.InstallConfig) {
				cfg.OS = model.OSFCOS
				cfg.NvidiaDriverVersion = "570-open"
			},
			wantErr: "nvidia_driver_version: not supported on FCOS",
		},
		{
			name: "FCOS nvidia driver version empty is OK",
			modify: func(cfg *model.InstallConfig) {
				cfg.OS = model.OSFCOS
			},
			wantErr: "",
		},
		{
			name: "FCOS nvidia driver version rejected even with ignition_url",
			modify: func(cfg *model.InstallConfig) {
				cfg.OS = model.OSFCOS
				cfg.IgnitionURL = "https://example.com/fcos.ign"
				cfg.NvidiaDriverVersion = "570-open"
				cfg.SSHKeys = nil
				cfg.Users = nil
				cfg.Channel = ""
			},
			wantErr: "nvidia_driver_version: not supported on FCOS",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.modify(cfg)
			err := CheckConsistency(cfg)
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("CheckConsistency() unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("CheckConsistency() expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("CheckConsistency() error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestGitHubUsername(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid simple", "torvalds", false},
		{"valid with hyphen", "john-doe", false},
		{"valid with numbers", "user123", false},
		{"valid single char", "a", false},
		{"valid max length 39", strings.Repeat("a", 39), false},
		{"empty", "", true},
		{"too long 40", strings.Repeat("a", 40), true},
		{"leading hyphen", "-user", true},
		{"trailing hyphen", "user-", true},
		{"consecutive hyphens", "john--doe", true},
		{"contains underscore", "user_name", true},
		{"contains dot", "user.name", true},
		{"contains space", "user name", true},
		{"contains slash", "user/name", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := GitHubUsername(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("GitHubUsername(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestFlatcarVersion(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"empty is latest", "", false},
		{"valid semver", "3510.2.8", false},
		{"valid zeros", "0.0.0", false},
		{"valid large", "9999.99.99", false},
		{"missing patch", "3510.2", true},
		{"with v prefix", "v3510.2.8", true},
		{"alpha string", "latest", true},
		{"extra segment", "3510.2.8.1", true},
		{"leading dot", ".3510.2.8", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := FlatcarVersion(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("FlatcarVersion(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestPasswordHash(t *testing.T) {
	tests := []struct {
		in      string
		wantErr bool
	}{
		{"", false},
		{"$6$rounds=4096$salt$hash", false},
		{"$y$hash", false},
		{"$2b$12$hash", false},
		{"$5$hash", false},
		{"hunter2", true},
		{"plaintext", true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			err := PasswordHash(tt.in)
			if (err != nil) != tt.wantErr {
				t.Errorf("PasswordHash(%q) error=%v wantErr=%v", tt.in, err, tt.wantErr)
			}
		})
	}
}

func TestSysextName(t *testing.T) {
	tests := []struct {
		in      string
		wantErr bool
	}{
		{"docker", false},
		{"nvidia-runtime", false},
		{"my_ext", false},
		{"", true},
		{"bad/name", true},
		{"bad.name", true},
		{"bad name", true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			err := SysextName(tt.in)
			if (err != nil) != tt.wantErr {
				t.Errorf("SysextName(%q) error=%v wantErr=%v", tt.in, err, tt.wantErr)
			}
		})
	}
}

func TestGateway(t *testing.T) {
	tests := []struct {
		input   string
		wantErr bool
	}{
		{"192.168.1.1", false},
		{"10.0.0.1", false},
		{"8.8.8.8", false},
		{"not-an-ip", true},
		{"", true},
		{"256.0.0.1", true},
		{"192.168.1.1/24", true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			err := Gateway(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("Gateway(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestDNSServer(t *testing.T) {
	tests := []struct {
		input   string
		wantErr bool
	}{
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"192.168.1.1", false},
		{"not-dns", true},
		{"", true},
		{"300.0.0.1", true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			err := DNSServer(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("DNSServer(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestStaticNetwork(t *testing.T) {
	good := model.NetworkConfig{
		Mode:      model.NetworkStatic,
		Interface: "eth0",
		Address:   "192.168.1.10/24",
		Gateway:   "192.168.1.1",
		DNS:       []string{"1.1.1.1", "8.8.8.8"},
	}
	tests := []struct {
		name    string
		mutate  func(n *model.NetworkConfig)
		wantErr string
	}{
		{"valid", func(n *model.NetworkConfig) {}, ""},
		{"valid without DNS", func(n *model.NetworkConfig) { n.DNS = nil }, ""},
		{"missing gateway", func(n *model.NetworkConfig) { n.Gateway = "" }, "static network requires a gateway"},
		{"missing interface", func(n *model.NetworkConfig) { n.Interface = "" }, "static network requires an interface name"},
		{"missing address", func(n *model.NetworkConfig) { n.Address = "" }, "static network requires an IP address"},
		{"bad interface name", func(n *model.NetworkConfig) { n.Interface = "eth 0" }, "network interface:"},
		{"bad address", func(n *model.NetworkConfig) { n.Address = "192.168.1.10" }, "network address:"},
		{"bad gateway", func(n *model.NetworkConfig) { n.Gateway = "not-an-ip" }, "gateway:"},
		{"bad DNS server", func(n *model.NetworkConfig) { n.DNS = []string{"1.1.1.1", "not-dns"} }, `DNS server "not-dns":`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := good
			n.DNS = append([]string(nil), good.DNS...)
			tt.mutate(&n)
			err := StaticNetwork(n)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("StaticNetwork(%+v) unexpected error: %v", n, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("StaticNetwork(%+v) expected error containing %q, got nil", n, tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("StaticNetwork(%+v) error = %q, want it to contain %q", n, err, tt.wantErr)
			}
		})
	}
}

func TestTailscaleAuthKey(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid auth key", "tskey-auth-kExampleKeyID1-ExampleSecretThatIsLongEnough123", false},
		{"valid client key", "tskey-client-kExampleKeyID1-ExampleSecretThatIsLongEnough12", false},
		{"empty", "", true},
		{"wrong prefix", "sk-auth-foo-bar", true},
		{"missing secret", "tskey-auth-kExampleKeyID1", true},
		{"too short id", "tskey-auth-k-ExampleSecretThatIsLongEnough123", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := TailscaleAuthKey(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("TailscaleAuthKey(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestTailscaleRoutes(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"single CIDR", "10.0.0.0/8", false},
		{"multiple CIDRs", "10.0.0.0/8,192.168.1.0/24", false},
		{"with spaces", "10.0.0.0/8, 192.168.1.0/24", false},
		{"empty", "", true},
		{"invalid CIDR", "not-a-cidr", true},
		{"mixed valid invalid", "10.0.0.0/8,bad", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := TailscaleRoutes(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("TailscaleRoutes(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestTailscaleRoutes_AllCommas(t *testing.T) {
	// Inputs that consist only of commas/spaces must be rejected.
	for _, bad := range []string{",", " , ", ",,", " "} {
		if err := TailscaleRoutes(bad); err == nil {
			t.Errorf("TailscaleRoutes(%q) should fail: no valid CIDRs present", bad)
		}
	}
}
