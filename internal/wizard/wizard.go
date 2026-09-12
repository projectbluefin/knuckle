// Package wizard provides the wizard subsystem for knuckle.
// It manages installation state transitions and orchestrates the install flow.
package wizard

import (
	"context"
	"fmt"
	"runtime"

	"github.com/projectbluefin/knuckle/internal/bakery"
	"github.com/projectbluefin/knuckle/internal/ignition"
	"github.com/projectbluefin/knuckle/internal/install"
	"github.com/projectbluefin/knuckle/internal/model"
	"github.com/projectbluefin/knuckle/internal/probe"
	"github.com/projectbluefin/knuckle/internal/validate"
)

// fetchAllChannelsFn is a package-level hook so tests can inject a mock.
var fetchAllChannelsFn = bakery.FetchAllChannels

// fetchFCOSStreamVersionFn fetches the Fedora version label for an FCOS stream.
// Tests can replace this to avoid network calls.
var fetchFCOSStreamVersionFn = fetchFCOSStreamVersion

// fetchFCOSStreamVersion fetches the current release version for an FCOS stream.
// Returns the stream name itself as fallback; empty string on any hard error.
func fetchFCOSStreamVersion(ctx context.Context, stream string) (string, error) {
	return stream, nil // version data requires #641; stream name is shown until then
}

// FCOSStreamInfo holds minimal display data for an FCOS stream.
type FCOSStreamInfo struct {
	Stream  string // "stable", "testing", "next"
	Version string // version label, e.g. stream name or future Fedora release version
}

// State holds the complete wizard state
type State struct {
	CurrentStep model.WizardStep
	Config      model.InstallConfig

	// Discovered hardware
	Disks      []model.DiskInfo
	Interfaces []model.NetworkInterface
	Sysexts    []model.SysextEntry

	// NvidiaGPUDetected is true when an NVIDIA GPU was found on the installer host.
	// Since knuckle installs to the machine it runs on, detected GPUs will be present
	// on the installed system too — used to auto-select the nvidia-runtime sysext.
	NvidiaGPUDetected bool
	NvidiaGPUs        []probe.NvidiaGPUInfo // detected GPU details for display in GPU Setup screen

	// Channel version info (fetched at startup)
	Channels []bakery.ChannelInfo

	// FCOS stream info (fetched at startup when OS == FCOS)
	FCOSStreams []FCOSStreamInfo

	// System checks (populated at startup)
	SystemChecks []SystemCheck

	// User confirmed destructive operation
	Confirmed bool

	// Error from the last operation
	Err error

	// Installation progress messages
	ProgressMessages []string
}

// SystemCheck represents a pre-flight system check result
type SystemCheck struct {
	Name   string
	Status string // "ok", "warn", "fail"
	Detail string
}

// Wizard manages the installer workflow
type Wizard struct {
	State     *State
	Prober    probe.Prober
	Bakery    bakery.Client
	Installer install.Installer
}

// New creates a new Wizard with the given dependencies
func New(prober probe.Prober, bakeryClient bakery.Client, installer install.Installer) *Wizard {
	return &Wizard{
		State: &State{
			CurrentStep: model.StepWelcome,
			Config: model.InstallConfig{
				OS: model.OSFlatcar,
				// Arch is set at compile time — the ISO is built for a specific architecture.
				// runtime.GOARCH reflects the arch the binary was compiled for (amd64 or arm64).
				Arch:           runtime.GOARCH,
				Channel:        "stable",
				UpdateStrategy: model.UpdateStrategy{RebootStrategy: "reboot"},
				// Default to enabling swap (issue #95). Sized at render time —
				// 0 SizeMB means "use DefaultSwapSizeMB".
				Swap: model.SwapConfig{Enabled: true, SizeMB: 0},
			},
		},
		Prober:    prober,
		Bakery:    bakeryClient,
		Installer: installer,
	}
}

// Next advances to the next step if validation passes
func (w *Wizard) Next() error {
	if err := w.ValidateCurrentStep(); err != nil {
		return err
	}

	if w.State.CurrentStep < model.StepDone {
		w.State.CurrentStep++
		// BluefinDDI skips Sysext, Nvidia, Tailscale, and Update steps —
		// those are all Flatcar/FCOS-only concerns.
		if w.isBluefinDDI() {
			for w.State.CurrentStep == model.StepSysext ||
				w.State.CurrentStep == model.StepNvidia ||
				w.State.CurrentStep == model.StepTailscale ||
				w.State.CurrentStep == model.StepUpdate {
				w.State.CurrentStep++
			}
		}
		// StepNvidia is conditional — only visit it when nvidia-runtime is selected.
		if w.State.CurrentStep == model.StepNvidia && !w.isNvidiaSelected() {
			w.State.CurrentStep++
		}
		// StepTailscale is conditional — only visit it when tailscale is selected.
		if w.State.CurrentStep == model.StepTailscale && !w.isTailscaleSelected() {
			w.State.CurrentStep++
		}
	}
	return nil
}

// Previous goes back to the previous step
func (w *Wizard) Previous() {
	if w.State.CurrentStep > model.StepWelcome {
		w.State.CurrentStep--
		// BluefinDDI: skip back over Sysext/Nvidia/Tailscale/Update.
		if w.isBluefinDDI() {
			for w.State.CurrentStep == model.StepUpdate ||
				w.State.CurrentStep == model.StepTailscale ||
				w.State.CurrentStep == model.StepNvidia ||
				w.State.CurrentStep == model.StepSysext {
				w.State.CurrentStep--
			}
		}
		// StepTailscale is conditional — skip back over it when tailscale is not selected.
		if w.State.CurrentStep == model.StepTailscale && !w.isTailscaleSelected() {
			w.State.CurrentStep--
		}
		// StepNvidia is conditional — skip back over it when nvidia-runtime is not selected.
		if w.State.CurrentStep == model.StepNvidia && !w.isNvidiaSelected() {
			w.State.CurrentStep--
		}
	}
}

// isBluefinDDI returns true when the target OS is a Bluefin Server DDI image.
// All Flatcar-specific steps (Channel, Sysext, Nvidia, Tailscale, Update) are
// skipped for DDI installs — systemd-repart handles partitioning, no Ignition.
func (w *Wizard) isBluefinDDI() bool {
	return w.State.Config.OS == model.OSBluefinDDI
}

// isNvidiaSelected returns true when the nvidia-runtime sysext is toggled on.
// FCOS and BluefinDDI do not use /etc/flatcar/enabled-sysext.conf, so the
// NVIDIA step is unconditionally skipped for those OS targets.
func (w *Wizard) isNvidiaSelected() bool {
	if w.State.Config.OS == model.OSFCOS || w.isBluefinDDI() {
		return false
	}
	for _, s := range w.State.Sysexts {
		if s.Name == "nvidia-runtime" && s.Selected {
			return true
		}
	}
	return false
}

// isTailscaleSelected returns true when the tailscale sysext is toggled on.
func (w *Wizard) isTailscaleSelected() bool {
	for _, s := range w.State.Sysexts {
		if s.Name == "tailscale" && s.Selected {
			return true
		}
	}
	return false
}

// GoToStep jumps to a specific step (for review screen navigation)
func (w *Wizard) GoToStep(step model.WizardStep) {
	if step >= model.StepWelcome && step <= model.StepDone {
		// StepNvidia is conditional — refuse jump when nvidia-runtime not selected.
		if step == model.StepNvidia && !w.isNvidiaSelected() {
			return
		}
		// StepTailscale is conditional — refuse jump when tailscale not selected.
		if step == model.StepTailscale && !w.isTailscaleSelected() {
			return
		}
		w.State.CurrentStep = step
	}
}

// ValidateCurrentStep validates the data for the current step
func (w *Wizard) ValidateCurrentStep() error {
	switch w.State.CurrentStep {
	case model.StepWelcome:
		return w.validateWelcome()
	case model.StepNetwork:
		return w.validateNetwork()
	case model.StepStorage:
		return w.validateStorage()
	case model.StepUser:
		return w.validateUser()
	case model.StepSysext:
		return nil // sysext selection is optional
	case model.StepNvidia:
		return nil // driver series has a safe default; no validation needed
	case model.StepTailscale:
		return w.validateTailscale()
	case model.StepUpdate:
		return nil // update strategy selection is optional (defaults to "reboot")
	case model.StepReview:
		return validate.CheckConsistency(&w.State.Config)
	case model.StepInstall:
		return nil // install step validates on execute
	default:
		return nil
	}
}

func (w *Wizard) validateWelcome() error {
	// BluefinDDI has no channel/stream — skip channel validation entirely.
	if w.isBluefinDDI() {
		return nil
	}
	if w.State.Config.OS == model.OSFCOS {
		if err := validate.FCOSStream(w.State.Config.Channel); err != nil {
			return err
		}
	} else {
		if err := validate.Channel(w.State.Config.Channel); err != nil {
			return err
		}
	}
	if w.State.Config.IgnitionURL != "" {
		if err := validate.IgnitionURL(w.State.Config.IgnitionURL); err != nil {
			return fmt.Errorf("ignition URL: %w", err)
		}
	}
	return nil
}

func (w *Wizard) validateNetwork() error {
	cfg := w.State.Config.Network
	if cfg.Mode == model.NetworkStatic {
		return validate.StaticNetwork(cfg)
	}
	return nil
}

func (w *Wizard) validateStorage() error {
	if w.State.Config.Disk.DevPath == "" {
		return fmt.Errorf("no disk selected")
	}
	for _, p := range w.State.Config.Disk.Partitions {
		if p.MountPoint != "" {
			return fmt.Errorf("disk %s has mounted partition %s at %s — unmount before installing", w.State.Config.Disk.DevPath, p.Path, p.MountPoint)
		}
	}
	return validate.DiskPath(w.State.Config.Disk.DevPath)
}

func (w *Wizard) validateTailscale() error {
	ts := w.State.Config.Tailscale
	if ts.AuthKey == "" {
		// Empty auth key is allowed: user skips the step, no provisioning.
		return nil
	}
	if err := validate.TailscaleAuthKey(ts.AuthKey); err != nil {
		return fmt.Errorf("tailscale auth key: %w", err)
	}
	if ts.Mode == model.TailscaleModeSubnetRouter {
		if err := validate.TailscaleRoutes(ts.Routes); err != nil {
			return fmt.Errorf("tailscale routes: %w", err)
		}
	}
	return nil
}

func (w *Wizard) validateUser() error {
	// Validate hostname
	if w.State.Config.Hostname != "" {
		if err := validate.Hostname(w.State.Config.Hostname); err != nil {
			return err
		}
	}
	// Must have at least one user or SSH key
	if len(w.State.Config.Users) == 0 && len(w.State.Config.SSHKeys) == 0 {
		return fmt.Errorf("at least one user or SSH key is required")
	}
	for _, user := range w.State.Config.Users {
		if err := validate.Username(user.Username); err != nil {
			return err
		}
		for _, key := range user.SSHKeys {
			if err := validate.SSHPublicKey(key); err != nil {
				return err
			}
		}
	}
	for _, key := range w.State.Config.SSHKeys {
		if err := validate.SSHPublicKey(key); err != nil {
			return err
		}
	}
	return nil
}

// ProbeHardware discovers disks, network interfaces, and GPU hardware.
func (w *Wizard) ProbeHardware(ctx context.Context) error {
	disks, err := w.Prober.ListDisks(ctx)
	if err != nil {
		return fmt.Errorf("probing disks: %w", err)
	}
	w.State.Disks = disks

	ifaces, err := w.Prober.ListNetworkInterfaces(ctx)
	if err != nil {
		return fmt.Errorf("probing network: %w", err)
	}
	w.State.Interfaces = ifaces

	// Detect NVIDIA GPUs via PCI sysfs — no driver required.
	gpus := probe.DetectNvidiaGPUs()
	w.State.NvidiaGPUs = gpus
	w.State.NvidiaGPUDetected = len(gpus) > 0

	// Run system checks after hardware probe
	w.runSystemChecks()

	return nil
}

// runSystemChecks performs pre-flight verification
func (w *Wizard) runSystemChecks() {
	w.State.SystemChecks = nil

	// Check: at least one eligible disk
	if len(w.State.Disks) > 0 {
		w.State.SystemChecks = append(w.State.SystemChecks, SystemCheck{
			Name: "Disk", Status: "ok",
			Detail: fmt.Sprintf("%d eligible disk(s) found", len(w.State.Disks)),
		})
	} else {
		w.State.SystemChecks = append(w.State.SystemChecks, SystemCheck{
			Name: "Disk", Status: "fail",
			Detail: "no eligible disks found (need ≥8GB, non-removable, non-boot)",
		})
	}

	// Check: at least one network interface
	activeIfaces := 0
	for _, iface := range w.State.Interfaces {
		if len(iface.IPv4Addrs) > 0 {
			activeIfaces++
		}
	}
	if activeIfaces > 0 {
		w.State.SystemChecks = append(w.State.SystemChecks, SystemCheck{
			Name: "Network", Status: "ok",
			Detail: fmt.Sprintf("%d interface(s) with IPv4", activeIfaces),
		})
	} else if len(w.State.Interfaces) > 0 {
		w.State.SystemChecks = append(w.State.SystemChecks, SystemCheck{
			Name: "Network", Status: "warn",
			Detail: "interfaces found but none have IPv4 addresses",
		})
	} else {
		w.State.SystemChecks = append(w.State.SystemChecks, SystemCheck{
			Name: "Network", Status: "fail",
			Detail: "no network interfaces detected",
		})
	}

	// Check: sysext catalog availability
	if len(w.State.Sysexts) > 0 {
		w.State.SystemChecks = append(w.State.SystemChecks, SystemCheck{
			Name: "Sysext Catalog", Status: "ok",
			Detail: fmt.Sprintf("%d extensions available", len(w.State.Sysexts)),
		})
	} else {
		w.State.SystemChecks = append(w.State.SystemChecks, SystemCheck{
			Name: "Sysext Catalog", Status: "warn",
			Detail: "catalog unavailable — sysext selection will be skipped",
		})
	}
}

// FetchSysexts loads the sysext catalog for the configured architecture.
// For FCOS and BluefinDDI, sysexts are not applicable — the step is skipped.
func (w *Wizard) FetchSysexts(ctx context.Context) error {
	if w.State.Config.OS == model.OSFCOS || w.isBluefinDDI() {
		// FCOS sysext catalog support requires #641 (FetchCatalogFCOS).
		// For now, leave Sysexts empty so the step is a no-op for FCOS.
		w.State.Sysexts = nil
		return nil
	}
	sysexts, err := w.Bakery.FetchCatalogArch(ctx, w.State.Config.Arch)
	if err != nil {
		return fmt.Errorf("fetching sysext catalog: %w", err)
	}
	w.State.Sysexts = sysexts

	// Auto-select nvidia-runtime when a GPU is detected on the installer host.
	if w.State.NvidiaGPUDetected {
		for i, s := range w.State.Sysexts {
			if s.Name == "nvidia-runtime" {
				w.State.Sysexts[i].Selected = true
				w.State.Config.Sysexts = w.State.Sysexts
				if w.State.Config.NvidiaDriverVersion == "" {
					w.State.Config.NvidiaDriverVersion = model.DefaultNvidiaDriverSeries
				}
				break
			}
		}
	}
	return nil
}

// FetchChannels loads version info for all release channels
func (w *Wizard) FetchChannels(ctx context.Context) error {
	channels, err := fetchAllChannelsFn(ctx)
	if len(channels) > 0 {
		// Use whatever channels succeeded; partial failures (e.g. one channel
		// temporarily unreachable) are not fatal — surface only a total outage.
		w.State.Channels = channels
		return nil
	}
	return err
}

// FetchFCOSStreams loads version info for the three FCOS streams (stable, testing, next).
// Version info is best-effort — failures are non-fatal; the picker will show streams
// without version numbers rather than blocking the TUI.
func (w *Wizard) FetchFCOSStreams(ctx context.Context) error {
	streams := []string{"stable", "testing", "next"}
	infos := make([]FCOSStreamInfo, 0, len(streams))
	for _, s := range streams {
		version, err := fetchFCOSStreamVersionFn(ctx, s)
		info := FCOSStreamInfo{Stream: s}
		if err == nil {
			info.Version = version
		}
		infos = append(infos, info)
	}
	w.State.FCOSStreams = infos
	return nil
}

// Execute runs the installation
func (w *Wizard) Execute(ctx context.Context) error {
	w.State.ProgressMessages = nil
	progress := func(msg string) {
		w.State.ProgressMessages = append(w.State.ProgressMessages, msg)
	}
	return w.Installer.Install(ctx, &w.State.Config, progress)
}

// ExecuteWithProgress runs the install with an external progress callback.
// Used by the TUI to send progress messages via tea.Cmd channel.
func (w *Wizard) ExecuteWithProgress(ctx context.Context, progress func(string)) error {
	return w.Installer.Install(ctx, &w.State.Config, progress)
}

// IsFirstStep returns true if on the first step
func (w *Wizard) IsFirstStep() bool {
	return w.State.CurrentStep == model.StepWelcome
}

// IsLastStep returns true if on the final step
func (w *Wizard) IsLastStep() bool {
	return w.State.CurrentStep == model.StepDone
}

// StepCount returns the total number of steps
func StepCount() int {
	return int(model.StepDone) + 1
}

// GenerateButane renders the current config as Butane YAML for preview.
func (w *Wizard) GenerateButane() (string, error) {
	gen := ignition.NewGenerator()
	return gen.GenerateButane(&w.State.Config)
}
