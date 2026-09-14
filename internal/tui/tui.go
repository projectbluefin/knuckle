package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
	"github.com/projectbluefin/knuckle/internal/bakery"
	"github.com/projectbluefin/knuckle/internal/model"
	"github.com/projectbluefin/knuckle/internal/validate"
	"github.com/projectbluefin/knuckle/internal/wizard"
)

var (
	selectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("170")).Bold(true)
	errorStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	helpStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("241")).MarginTop(1)
)

// installProgressMsg carries a progress line from the install goroutine.
type installProgressMsg string

// installDoneMsg signals install completion (success or failure).
type installDoneMsg struct{ err error }

// fetchKeysMsg carries the result of an async GitHub key fetch.
type fetchKeysMsg struct {
	keys []string
	err  error
}

// Model is the top-level Bubble Tea model
type Model struct {
	Wizard        *wizard.Wizard
	rebootFn      func(context.Context) error // nil ⇒ dry-run / test mode
	width         int
	height        int
	err           error
	quitting      bool
	confirmQuit   bool
	confirmReboot bool
	showButane    bool
	installing    bool
	fetching      bool
	cursor        int
	fields        []field
	fieldIdx      int

	// huh form state
	activeForm         *huh.Form
	dnsInput           string
	networkModeInput   string
	usernameInput      string
	passwordInput      string
	githubUserInput    string
	sshKeyInput        string
	tailscaleAuthKeyIn string
	tailscaleModeIn    string
	tailscaleRoutesIn  string
	showAdvanced       bool

	// osSubView tracks whether the StepWelcome OS picker is shown (true)
	// or the channel/stream card picker is shown (false).
	osSubView bool

	// Sysext list (bubbles/list)
	sysextList      list.Model
	sysextListReady bool

	// Install progress
	spinner       spinner.Model
	progress      progress.Model
	progressCh    chan string
	installCancel context.CancelFunc
}

type field struct {
	label  string
	value  string
	key    string
	masked bool
}

// New creates a new TUI model
func New(w *wizard.Wizard) *Model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))

	p := progress.New(
		progress.WithColors(lipgloss.Color("#50fa7b"), lipgloss.Color("#ff79c6")),
		progress.WithWidth(40),
	)

	m := &Model{
		Wizard:   w,
		spinner:  s,
		progress: p,
	}
	if len(w.State.Config.Users) > 0 {
		m.usernameInput = w.State.Config.Users[0].Username
	}
	m.initStepFields()
	m.initForm()
	return m
}

func (m *Model) Init() tea.Cmd {
	// Test mode: quit immediately after Init for coverage testing
	if os.Getenv("KNUCKLE_TEST_TUI_AUTO_QUIT") == "1" {
		m.quitting = true
		return tea.Quit
	}
	var cmds []tea.Cmd
	if m.activeForm != nil {
		cmds = append(cmds, m.activeForm.Init())
	}
	cmds = append(cmds, m.spinner.Tick)
	return tea.Batch(cmds...)
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		// Global keys override form
		switch msg.String() {
		case "ctrl+c":
			if m.confirmQuit {
				m.quitting = true
				if m.installCancel != nil {
					m.installCancel()
				}
				return m, tea.Quit
			}
			m.confirmQuit = true
			m.err = fmt.Errorf("press Ctrl+C again to quit, or any other key to continue")
			return m, nil
		case "ctrl+a":
			// Toggle advanced mode on Welcome step
			if m.Wizard.State.CurrentStep == model.StepWelcome {
				m.showAdvanced = !m.showAdvanced
				return m, nil
			}
		}
		m.confirmQuit = false
		// Only reset confirmReboot when not on Done step or not pressing 'r'
		if m.Wizard.State.CurrentStep != model.StepDone || msg.String() != "r" {
			m.confirmReboot = false
		}
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		// Forward to active form so it knows its rendering width
		if m.activeForm != nil {
			form, cmd := m.activeForm.Update(msg)
			if f, ok := form.(*huh.Form); ok {
				m.activeForm = f
			}
			return m, cmd
		}
		return m, nil
	case installProgressMsg:
		m.Wizard.State.ProgressMessages = append(m.Wizard.State.ProgressMessages, string(msg))
		// Update progress bar + continue listening
		total := 5.0
		done := float64(len(m.Wizard.State.ProgressMessages))
		pCmd := m.progress.SetPercent(done / total)
		return m, tea.Batch(pCmd, m.waitForProgress())
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case progress.FrameMsg:
		var cmd tea.Cmd
		m.progress, cmd = m.progress.Update(msg)
		return m, cmd
	case installDoneMsg:
		m.installing = false
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.Wizard.State.CurrentStep = model.StepDone
		// Don't quit immediately — show Done screen, let user press q to exit
		return m, nil
	case fetchKeysMsg:
		m.fetching = false
		if msg.err != nil {
			m.err = fmt.Errorf("%w — edit the username and press Enter to retry, or clear it and add an SSH key or password instead", msg.err)
			return m, nil
		}
		for _, k := range msg.keys {
			if err := validate.SSHPublicKey(k); err != nil {
				m.err = fmt.Errorf("invalid SSH key from GitHub: %w", err)
				return m, nil
			}
		}
		m.Wizard.ApplyGitHubKeys(msg.keys, detectLocalSSHKeys(), m.sshKeyInput)
		if !m.Wizard.HasAnyAuthentication() {
			m.err = fmt.Errorf("no SSH keys found — add a key manually, set a password, or use a GitHub user with public keys")
			return m, nil
		}
		if err := m.Wizard.Next(); err != nil {
			m.err = err
			return m, nil
		}
		m.err = nil
		m.cursor = 0
		m.initStepFields()
		m.initForm()
		if m.activeForm != nil {
			return m, m.activeForm.Init()
		}
		return m, nil
	}

	// Intercept shift+tab on non-form steps — go back a wizard step.
	// On form steps, let huh handle shift+tab natively so the user can
	// navigate backwards through form fields (previous field / previous group).
	if keyMsg, ok := msg.(tea.KeyPressMsg); ok && keyMsg.String() == "shift+tab" && m.activeForm == nil {
		if m.Wizard.State.CurrentStep > model.StepWelcome {
			m.Wizard.Previous()
			m.err = nil
			m.cursor = 0
			m.initStepFields()
			m.initForm()
			if m.activeForm != nil {
				return m, m.activeForm.Init()
			}
		}
		return m, nil
	}

	// Intercept esc on form steps — go back a wizard step.
	// On non-form steps esc is handled later in handleKey (with the sysext
	// filter-clear special case preserved there).
	if keyMsg, ok := msg.(tea.KeyPressMsg); ok && keyMsg.String() == "esc" && m.activeForm != nil {
		if m.Wizard.State.CurrentStep > model.StepWelcome {
			m.Wizard.Previous()
			m.err = nil
			m.cursor = 0
			m.activeForm = nil
			m.initStepFields()
			m.initForm()
			if m.activeForm != nil {
				return m, m.activeForm.Init()
			}
		}
		return m, nil
	}

	// Delegate to huh form if active
	if m.activeForm != nil {
		form, cmd := m.activeForm.Update(msg)
		if f, ok := form.(*huh.Form); ok {
			m.activeForm = f
		}
		if m.activeForm.State == huh.StateCompleted {
			return m, m.onFormComplete()
		}
		if m.activeForm.State == huh.StateAborted {
			m.Wizard.Previous()
			m.err = nil
			m.cursor = 0
			m.initStepFields()
			m.initForm()
			return m, nil
		}
		return m, cmd
	}

	// Non-form steps: handle keys
	if msg, ok := msg.(tea.KeyPressMsg); ok {
		return m.handleKey(msg)
	}
	return m, nil
}

func (m *Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// Only allow 'q' to quit when NOT editing text fields
	switch msg.String() {
	case "ctrl+c":
		if m.confirmQuit {
			m.quitting = true
			if m.installCancel != nil {
				m.installCancel()
			}
			return m, tea.Quit
		}
		m.confirmQuit = true
		m.err = fmt.Errorf("press Ctrl+C again to quit, or any other key to continue")
		return m, nil
	case "r":
		// Reboot on Done step — requires double-press confirmation
		if m.Wizard.State.CurrentStep == model.StepDone {
			if m.Wizard.State.Config.DryRun {
				m.err = fmt.Errorf("dry-run mode: would reboot (systemctl reboot)")
				return m, nil
			}
			if m.confirmReboot {
				m.quitting = true
				reboot := m.rebootFn
				return m, func() tea.Msg {
					if reboot != nil {
						_ = reboot(context.Background())
					}
					return tea.QuitMsg{}
				}
			}
			m.confirmReboot = true
			m.err = fmt.Errorf("press r again to confirm reboot")
			return m, nil
		}
		// On field steps, type the character
		if len(m.fields) > 0 {
			m.fields[m.fieldIdx].value += "r"
			return m, nil
		}
		return m, nil
	case "q":
		if len(m.fields) > 0 {
			// In field-editing mode, treat as regular character
			m.fields[m.fieldIdx].value += "q"
			return m, nil
		}
		// On non-field steps, require confirmation (same as Ctrl+C)
		if m.confirmQuit {
			m.quitting = true
			if m.installCancel != nil {
				m.installCancel()
			}
			return m, tea.Quit
		}
		m.confirmQuit = true
		m.err = fmt.Errorf("press q again to quit, or any other key to continue")
		return m, nil
	case "enter":
		m.confirmQuit = false
		return m.handleEnter()
	case "tab", "down", "j":
		m.confirmQuit = false
		if m.Wizard.State.CurrentStep == model.StepSysext && m.sysextListReady {
			// Delegate to bubbles/list.
			var cmd tea.Cmd
			m.sysextList, cmd = m.sysextList.Update(msg)
			m.cursor = m.sysextListCursorIdx()
			return m, cmd
		}
		if len(m.fields) > 0 {
			m.fieldIdx = (m.fieldIdx + 1) % len(m.fields)
		} else {
			m.cursor++
			// Clamp cursor to list bounds
			maxCursor := m.maxCursor()
			if m.cursor >= maxCursor {
				m.cursor = maxCursor - 1
			}
			if m.cursor < 0 {
				m.cursor = 0
			}
		}
		return m, nil
	case "up", "k":
		if m.Wizard.State.CurrentStep == model.StepSysext && m.sysextListReady {
			var cmd tea.Cmd
			m.sysextList, cmd = m.sysextList.Update(msg)
			m.cursor = m.sysextListCursorIdx()
			return m, cmd
		}
		if len(m.fields) > 0 {
			m.fieldIdx--
			if m.fieldIdx < 0 {
				m.fieldIdx = len(m.fields) - 1
			}
		} else if m.cursor > 0 {
			m.cursor--
		}
		return m, nil
	case "backspace":
		if len(m.fields) > 0 && len(m.fields[m.fieldIdx].value) > 0 {
			m.fields[m.fieldIdx].value = m.fields[m.fieldIdx].value[:len(m.fields[m.fieldIdx].value)-1]
		}
		return m, nil
	case "esc":
		// If sysext list is filtering, let esc clear the filter instead of going back.
		if m.Wizard.State.CurrentStep == model.StepSysext && m.sysextListReady && m.sysextList.FilterState() == list.Filtering {
			var cmd tea.Cmd
			m.sysextList, cmd = m.sysextList.Update(msg)
			m.cursor = m.sysextListCursorIdx()
			return m, cmd
		}
		m.Wizard.Previous()
		m.err = nil
		m.initStepFields()
		return m, nil
	case "space":
		if m.Wizard.State.CurrentStep == model.StepSysext {
			idx := m.cursor
			if idx < len(m.Wizard.State.Sysexts) {
				m.Wizard.State.Sysexts[idx].Selected = !m.Wizard.State.Sysexts[idx].Selected
				m.Wizard.State.Config.Sysexts = m.Wizard.State.Sysexts
				// When toggling nvidia-runtime on/off, sync the driver version.
				if m.Wizard.State.Sysexts[idx].Name == "nvidia-runtime" {
					if m.Wizard.State.Sysexts[idx].Selected {
						if m.Wizard.State.Config.NvidiaDriverVersion == "" {
							m.Wizard.State.Config.NvidiaDriverVersion = model.DefaultNvidiaDriverSeries
						}
					} else {
						m.Wizard.State.Config.NvidiaDriverVersion = ""
					}
				}
				m.refreshSysextListTitle()
			}
		} else if len(m.fields) > 0 {
			m.fields[m.fieldIdx].value += " "
		}
		return m, nil
	case "ctrl+b":
		if m.Wizard.State.CurrentStep == model.StepReview {
			m.showButane = !m.showButane
		}
		return m, nil
	default:
		// Delegate to sysext list for filter input and other keys.
		if m.Wizard.State.CurrentStep == model.StepSysext && m.sysextListReady {
			var cmd tea.Cmd
			m.sysextList, cmd = m.sysextList.Update(msg)
			m.cursor = m.sysextListCursorIdx()
			return m, cmd
		}
		if len(m.fields) > 0 && msg.Text != "" {
			m.fields[m.fieldIdx].value += msg.Text
		}
		return m, nil
	}
}

// maxCursor returns the number of selectable items in list-based steps
func (m *Model) maxCursor() int {
	switch m.Wizard.State.CurrentStep {
	case model.StepWelcome:
		if m.osSubView {
			return len(model.OSTargetIDs())
		}
		return m.channelCardCount()
	case model.StepStorage:
		return len(m.Wizard.State.Disks)
	case model.StepSysext:
		return len(m.Wizard.State.Sysexts)
	case model.StepNvidia:
		return len(model.NvidiaDriverOptions)
	case model.StepUpdate:
		if m.Wizard.State.Config.OS == model.OSFCOS {
			return 2 // immediate | disabled
		}
		return 3
	default:
		return 1
	}
}

func (m *Model) handleEnter() (tea.Model, tea.Cmd) {
	step := m.Wizard.State.CurrentStep
	m.applyFields()

	switch step {
	case model.StepWelcome:
		if m.osSubView {
			// Cursor indexes the same roster the picker renders — see
			// model.OSTargets().
			osList := model.OSTargetIDs()
			if m.cursor >= 0 && m.cursor < len(osList) {
				m.Wizard.State.Config.OS = osList[m.cursor]
			}
			// BluefinDDI has no channel — skip straight to Storage.
			if m.Wizard.State.Config.OS == model.OSBluefinDDI {
				m.Wizard.GoToStep(model.StepStorage)
				m.err = nil
				m.cursor = 0
				m.initStepFields()
				m.initForm()
				return m, nil
			}
			m.osSubView = false
			m.cursor = 0
			return m, nil
		}
		// Apply channel/stream selection from card cursor
		channels := m.channelList()
		if m.cursor >= 0 && m.cursor < len(channels) {
			m.Wizard.State.Config.Channel = channels[m.cursor]
		}
		// If IgnitionURL is set, skip directly to Storage
		if m.Wizard.State.Config.IgnitionURL != "" {
			m.Wizard.GoToStep(model.StepStorage)
			m.err = nil
			m.cursor = 0
			m.initStepFields()
			m.initForm()
			return m, nil
		}
	case model.StepStorage:
		if m.cursor < len(m.Wizard.State.Disks) {
			m.Wizard.State.Config.Disk = m.Wizard.State.Disks[m.cursor]
		}
		// If IgnitionURL is set, skip to Review after Storage
		if m.Wizard.State.Config.IgnitionURL != "" {
			if err := m.Wizard.ValidateCurrentStep(); err != nil {
				m.err = err
				return m, nil
			}
			m.Wizard.GoToStep(model.StepReview)
			m.err = nil
			m.cursor = 0
			m.initStepFields()
			m.initForm()
			return m, m.activeForm.Init()
		}
	case model.StepNvidia:
		// Confirm the cursor-selected driver series.
		if m.cursor >= 0 && m.cursor < len(model.NvidiaDriverOptions) {
			m.Wizard.State.Config.NvidiaDriverVersion = model.NvidiaDriverOptions[m.cursor].ID
		}
	case model.StepUpdate:
		if m.Wizard.State.Config.OS == model.OSFCOS {
			strategies := []string{model.FCOSStrategyImmediate, model.FCOSStrategyDisabled}
			if m.cursor >= 0 && m.cursor < len(strategies) {
				m.Wizard.State.Config.UpdateStrategy.FCOSUpdateStrategy = strategies[m.cursor]
			}
		} else {
			strategies := []string{"reboot", "off", "etcd-lock"}
			if m.cursor >= 0 && m.cursor < len(strategies) {
				m.Wizard.State.Config.UpdateStrategy.RebootStrategy = strategies[m.cursor]
			}
		}
	case model.StepUser:
		// Collect local keys first so they're always included even without GitHub.
		localKeys := detectLocalSSHKeys()
		cfg := &m.Wizard.State.Config
		if len(localKeys) > 0 {
			cfg.SSHKeys = mergeKeys(cfg.SSHKeys, localKeys)
			if len(cfg.Users) > 0 {
				cfg.Users[0].SSHKeys = mergeKeys(cfg.Users[0].SSHKeys, localKeys)
			}
		}
		// Trigger async GitHub key fetch if username is provided.
		// If the fetch is triggered we return immediately, so reaching the
		// auth-check below implies no GitHub fetch was started.
		for _, f := range m.fields {
			if f.key == "github_user" && f.value != "" && !m.fetching {
				m.fetching = true
				username := strings.TrimPrefix(f.value, "@")
				return m, func() tea.Msg {
					keys, err := fetchGitHubKeysFn(username)
					return fetchKeysMsg{keys: keys, err: err}
				}
			}
		}
		// No GitHub fetch was triggered — check we have SOME auth before advancing.
		hasAuth := len(cfg.SSHKeys) > 0
		for _, u := range cfg.Users {
			if u.PasswordHash != "" || len(u.SSHKeys) > 0 {
				hasAuth = true
				break
			}
		}
		if !hasAuth {
			m.err = fmt.Errorf("no authentication configured \u2014 add an SSH key, set a password, or provide a GitHub username with public keys")
			return m, nil
		}
	case model.StepInstall:
		if !m.installing {
			m.installing = true
			return m, m.startInstall()
		}
		return m, nil
	}

	if err := m.Wizard.Next(); err != nil {
		m.err = err
		return m, nil
	}

	m.err = nil
	m.cursor = 0
	m.initStepFields()
	m.initForm()

	if m.activeForm != nil {
		return m, m.activeForm.Init()
	}
	return m, nil
}

func (m *Model) startInstall() tea.Cmd {
	progressCh := make(chan string, 10)
	m.progressCh = progressCh

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	m.installCancel = cancel

	go func() {
		defer cancel()
		defer close(progressCh)
		defer func() {
			if r := recover(); r != nil {
				progressCh <- fmt.Sprintf("PANIC: %v", r)
			}
		}()

		progress := func(msg string) {
			progressCh <- msg
		}
		if err := m.Wizard.ExecuteWithProgress(ctx, progress); err != nil {
			progressCh <- "ERROR:" + err.Error()
		}
	}()

	// Return a Cmd that polls the channel
	return m.waitForProgress()
}

func (m *Model) waitForProgress() tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-m.progressCh
		if !ok {
			// Channel closed — install finished
			return installDoneMsg{err: nil}
		}
		if strings.HasPrefix(msg, "ERROR:") {
			return installDoneMsg{err: fmt.Errorf("%s", strings.TrimPrefix(msg, "ERROR:"))}
		}
		if strings.HasPrefix(msg, "PANIC:") {
			return installDoneMsg{err: fmt.Errorf("%s", msg)}
		}
		return installProgressMsg(msg)
	}
}

func (m *Model) applyFields() {
	cfg := &m.Wizard.State.Config
	switch m.Wizard.State.CurrentStep {
	case model.StepWelcome:
		for _, f := range m.fields {
			switch f.key {
			case "channel":
				if f.value != "" {
					var chanErr error
					if cfg.OS == model.OSFCOS {
						chanErr = validate.FCOSStream(f.value)
					} else {
						chanErr = validate.Channel(f.value)
					}
					if chanErr != nil {
						m.err = chanErr
						return
					}
					cfg.Channel = f.value
				}
			case "version":
				cfg.Version = f.value
			case "ignition_url":
				if f.value != "" {
					if err := validate.IgnitionURL(f.value); err != nil {
						m.err = err
						return
					}
				}
				cfg.IgnitionURL = f.value
			}
		}
	case model.StepNetwork:
		for _, f := range m.fields {
			switch f.key {
			case "interface":
				cfg.Network.Interface = f.value
			case "address":
				cfg.Network.Address = f.value
			case "gateway":
				cfg.Network.Gateway = f.value
			case "dns":
				if f.value != "" {
					cfg.Network.DNS = strings.Split(f.value, ",")
				}
			}
		}
		// Switch to static mode if any static fields are filled in
		if cfg.Network.Address != "" || cfg.Network.Gateway != "" {
			cfg.Network.Mode = model.NetworkStatic
		} else {
			cfg.Network.Mode = model.NetworkDHCP
		}
	case model.StepUser:
		for _, f := range m.fields {
			switch f.key {
			case "hostname":
				cfg.Hostname = f.value
			case "timezone":
				if f.value != "" {
					cfg.Timezone = f.value
				} else {
					cfg.Timezone = "UTC"
				}
			case "username":
				if f.value != "" {
					if len(cfg.Users) == 0 {
						cfg.Users = append(cfg.Users, model.UserConfig{
							Username: f.value,
							Groups:   []string{"sudo", "docker"},
						})
					} else {
						cfg.Users[0].Username = f.value
					}
				}
			case "password":
				if f.value != "" && len(cfg.Users) > 0 {
					hash, err := hashPassword(f.value)
					if err != nil {
						m.err = err
						return
					}
					cfg.Users[0].PasswordHash = hash
				}
			case "github_user":
				// GitHub key fetch is handled async in handleEnter()
				// Nothing to do here — fetch triggers on step advance
			case "ssh_key":
				if f.value != "" {
					// Support multiple keys separated by semicolons
					keys := splitSSHKeys(f.value)
					cfg.SSHKeys = keys
					if len(cfg.Users) > 0 {
						cfg.Users[0].SSHKeys = keys
					}
				}
			}
		}
	case model.StepReview:
		for _, f := range m.fields {
			if f.key == "confirm" {
				m.Wizard.State.Confirmed = (strings.ToUpper(strings.TrimSpace(f.value)) == "YES")
			}
		}
	}
}

func (m *Model) initStepFields() {
	m.fields = nil
	m.fieldIdx = 0
	switch m.Wizard.State.CurrentStep {
	case model.StepWelcome:
		// Show OS picker first; channel cards follow after OS selection.
		m.osSubView = true
		m.cursor = 0
		m.fields = nil
	case model.StepNvidia:
		// Position cursor at the currently configured driver version.
		m.cursor = 0
		for i, opt := range model.NvidiaDriverOptions {
			if opt.ID == m.Wizard.State.Config.NvidiaDriverVersion {
				m.cursor = i
				break
			}
		}
	case model.StepNetwork:
		m.fields = []field{
			{label: "Interface", key: "interface", value: m.Wizard.State.Config.Network.Interface},
			{label: "IP Address (CIDR)", key: "address", value: m.Wizard.State.Config.Network.Address},
			{label: "Gateway", key: "gateway", value: m.Wizard.State.Config.Network.Gateway},
			{label: "DNS (comma-separated)", key: "dns", value: strings.Join(m.Wizard.State.Config.Network.DNS, ",")},
		}
	case model.StepUser:
		username := ""
		if len(m.Wizard.State.Config.Users) > 0 {
			username = m.Wizard.State.Config.Users[0].Username
		}
		sshKey := ""
		if len(m.Wizard.State.Config.SSHKeys) > 0 {
			sshKey = m.Wizard.State.Config.SSHKeys[0]
		}
		m.fields = []field{
			{label: "Hostname", key: "hostname", value: m.Wizard.State.Config.Hostname},
			{label: "Timezone (e.g. UTC, America/New_York)", key: "timezone", value: m.Wizard.State.Config.Timezone},
			{label: "Username", key: "username", value: username},
			{label: "Password (optional, leave blank for key-only)", key: "password", value: "", masked: true},
			{label: "GitHub Username (fetches SSH keys)", key: "github_user", value: ""},
			{label: "— OR — SSH Public Key(s) (separate with ;)", key: "ssh_key", value: sshKey},
		}
	case model.StepReview:
		m.fields = []field{
			{label: "Type YES to confirm installation", key: "confirm", value: ""},
		}
	case model.StepUpdate:
		// No fields — cursor-select screen
	case model.StepSysext:
		// Initialize bubbles/list for sysext selection.
		m.initSysextList()
	}
}

func (m *Model) View() tea.View {
	view := tea.NewView(m.render())
	view.AltScreen = true
	return view
}

func (m *Model) render() string {
	if m.quitting {
		return "Installation cancelled.\n"
	}

	// Form-based steps use huh rendering
	if m.activeForm != nil {
		return m.viewWithForm()
	}

	// Non-form steps use manual rendering
	var b strings.Builder
	b.WriteString(m.buildBreadcrumb())
	b.WriteString("\n")

	switch m.Wizard.State.CurrentStep {
	case model.StepWelcome:
		b.WriteString(m.viewChannelCards())
	case model.StepStorage:
		b.WriteString(m.viewStorage())
	case model.StepSysext:
		b.WriteString(m.viewSysext())
	case model.StepNvidia:
		b.WriteString(m.viewNvidia())
	case model.StepUpdate:
		b.WriteString(m.viewUpdate())
	case model.StepInstall:
		b.WriteString(m.viewInstall())
	case model.StepDone:
		b.WriteString(m.viewDone())
	}

	if m.err != nil {
		b.WriteString("\n")
		b.WriteString(errorStyle.Render(fmt.Sprintf("⚠ %s", m.err.Error())))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(helpStyle.Render("↑↓/jk navigate • enter confirm • esc back • q quit"))
	return b.String()
}

func (m *Model) viewStorage() string {
	var b strings.Builder
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	sizeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("75"))
	warnStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)

	b.WriteString("Select Target Disk\n\n")
	if len(m.Wizard.State.Disks) == 0 {
		b.WriteString("No disks detected!\n")
		return b.String()
	}
	for i, disk := range m.Wizard.State.Disks {
		cursor := "  "
		if i == m.cursor {
			cursor = "▸ "
		}

		// Line 1: cursor + model + size right-aligned
		model := disk.Model
		if model == "" {
			model = "Unknown Disk"
		}
		size := disk.SizeHuman
		padding := 56 - len(model) - len(size)
		if padding < 2 {
			padding = 2
		}
		line1 := cursor + model + strings.Repeat(" ", padding) + sizeStyle.Render(size)

		// Line 2: path + transport
		path := disk.Path
		if path == "" {
			path = disk.DevPath
		}
		transport := disk.Transport
		if disk.Removable {
			transport += " (removable)"
		}
		line2 := "    " + dimStyle.Render(path+"  "+transport)

		if i == m.cursor {
			b.WriteString(selectedStyle.Render(line1))
		} else {
			b.WriteString(line1)
		}
		b.WriteString("\n")
		b.WriteString(line2)
		b.WriteString("\n\n")
	}
	b.WriteString(warnStyle.Render("⚠ All data on the selected disk will be erased!"))
	b.WriteString("\n")
	return b.String()
}

func (m *Model) viewSysext() string {
	var b strings.Builder

	// Selected count header.
	selectedCount := 0
	for _, ext := range m.Wizard.State.Sysexts {
		if ext.Selected {
			selectedCount++
		}
	}
	fmt.Fprintf(&b, "System Extensions — %d selected\n\n", selectedCount)

	// Brief GPU notice — full configuration is on the dedicated GPU Setup screen.
	if m.Wizard.State.NvidiaGPUDetected {
		gpuStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("76"))
		b.WriteString(gpuStyle.Render("  ✓ NVIDIA GPU detected — nvidia-runtime auto-selected · configure on next screen") + "\n\n")
	}

	if len(m.Wizard.State.Sysexts) == 0 {
		b.WriteString("  No extensions available (catalog fetch may have failed)\n")
		return b.String()
	}

	// Use bubbles/list if initialized.
	if m.sysextListReady {
		// Sync cursor → list position.
		listIdx := m.sysextListLookup(m.cursor)
		if m.sysextList.Index() != listIdx {
			m.sysextList.Select(listIdx)
		}
		m.sysextList.Title = m.sysextTitle()

		b.WriteString(m.sysextList.View())
		b.WriteString("\n")

		// Detail panel for the currently highlighted item.
		if item, ok := m.sysextList.SelectedItem().(sysextItem); ok {
			b.WriteString(m.renderDetailPanel(item.entry))
		}
		return b.String()
	}

	// Fallback: manual rendering (list not initialized).
	tierOrder := []string{bakery.TierIntegrated, bakery.TierMaintained, bakery.TierExperimental}
	tierMap := map[string][]int{}
	var otherIndices []int

	for i, ext := range m.Wizard.State.Sysexts {
		tier := ext.SupportTier
		if tier == "" {
			otherIndices = append(otherIndices, i)
			continue
		}
		tierMap[tier] = append(tierMap[tier], i)
	}

	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))

	renderGroup := func(groupName string, indices []int) {
		if len(indices) == 0 {
			return
		}
		b.WriteString("  " + dimStyle.Render("── "+groupName+" ──") + "\n")
		for _, idx := range indices {
			ext := m.Wizard.State.Sysexts[idx]

			cursor := "    "
			if idx == m.cursor {
				cursor = "  ▸ "
			}
			check := "[ ]"
			if ext.Selected {
				check = "[✓]"
			}
			version := ext.Version
			if version != "" {
				version = "v" + version
			}
			cat := ext.Category
			if cat == "" {
				cat = "Other"
			}

			line := fmt.Sprintf("%s%s %-22s %-14s  %s", cursor, check, ext.Name, version, cat)
			if idx == m.cursor {
				b.WriteString(selectedStyle.Render(line))
			} else {
				b.WriteString(line)
			}
			b.WriteString("\n")

			// Detail panel — only for the cursor item.
			if idx == m.cursor {
				b.WriteString(m.renderDetailPanel(ext))
			}
		}
		b.WriteString("\n")
	}

	for _, tierName := range tierOrder {
		renderGroup(tierName, tierMap[tierName])
	}
	if len(otherIndices) > 0 {
		renderGroup("Other", otherIndices)
	}

	return b.String()
}

// renderDetailPanel renders the expandable info box for the highlighted sysext entry.
// Uses m.width for terminal-width-aware sizing; returns empty string when terminal is too narrow.
func (m *Model) renderDetailPanel(ext model.SysextEntry) string {
	effectiveWidth := m.width
	if effectiveWidth == 0 {
		effectiveWidth = 80
	}
	if effectiveWidth < 60 {
		return ""
	}

	// Content width: terminal width minus 8-space indent and 4 border chars (│ ... │).
	// With effectiveWidth ≥ 60 (guaranteed above), panelWidth ≥ min(52,28) = 28.
	panelWidth := min(52, effectiveWidth-32)

	// Resolve long description and caveats from the curated catalog.
	longDesc := ext.Description
	caveats := bakery.CaveatsFor(ext.Name)
	if meta, ok := bakery.Lookup(ext.Name); ok && meta.Long != "" {
		longDesc = meta.Long
	}

	cat := ext.Category
	if cat == "" {
		cat = "Other"
	}
	tier := ext.SupportTier
	if tier == "" {
		tier = "Unknown"
	}
	version := ext.Version
	if version == "" {
		version = "unknown"
	}

	contentWidth := panelWidth - 2 // subtract the "│ " and " │" borders

	var lines []string
	lines = append(lines, fmt.Sprintf("Version:  %s", version))
	lines = append(lines, fmt.Sprintf("Category: %s", cat))
	lines = append(lines, fmt.Sprintf("Support:  %s", tier))
	lines = append(lines, "")
	lines = append(lines, wordWrap(longDesc, contentWidth)...)

	if len(caveats) > 0 {
		lines = append(lines, "")
		for _, c := range caveats {
			lines = append(lines, wordWrap("! "+c, contentWidth)...)
		}
	}

	indent := "        " // 8 spaces
	border := strings.Repeat("─", panelWidth)
	top := "┌" + border + "┐"
	bottom := "└" + border + "┘"

	var b strings.Builder
	b.WriteString(indent + top + "\n")
	for _, line := range lines {
		// Truncate to content width using rune count to handle multi-byte chars.
		runes := []rune(line)
		if len(runes) > contentWidth {
			runes = runes[:contentWidth]
			line = string(runes)
		}
		padding := strings.Repeat(" ", contentWidth-len(runes))
		b.WriteString(indent + "│ " + line + padding + " │\n")
	}
	b.WriteString(indent + bottom + "\n")

	return b.String()
}

// viewNvidia renders the dedicated GPU Setup screen.
// Only reached when nvidia-runtime is selected in the sysext step.
// Full-width design: GPU detection status, two-component explanation, driver series picker.
func (m *Model) viewNvidia() string {
	var b strings.Builder

	headStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("170")).Bold(true)
	okStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("76")).Bold(true)
	warnStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	accent := lipgloss.NewStyle().Foreground(lipgloss.Color("75"))
	sepStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("238"))

	sep := sepStyle.Render(strings.Repeat("─", 62))

	b.WriteString(headStyle.Render("NVIDIA GPU Setup") + "\n\n")

	// —— GPU detection status ——
	if m.Wizard.State.NvidiaGPUDetected {
		b.WriteString(okStyle.Render("  ✓ NVIDIA GPU detected on this machine") + "\n")
		for _, gpu := range m.Wizard.State.NvidiaGPUs {
			b.WriteString(dimStyle.Render(fmt.Sprintf("    %s  ·  %s", gpu.PCIAddress, gpu.DeviceName)) + "\n")
		}
	} else {
		b.WriteString(warnStyle.Render("  ⚠ No NVIDIA GPU detected — continuing at your request") + "\n")
	}
	b.WriteString("\n")

	// —— Two-component explanation ——
	b.WriteString("  knuckle will configure two components at first boot:\n\n")

	b.WriteString(accent.Render("  ① Flatcar-official signed kernel driver") + "\n")
	b.WriteString(dimStyle.Render("    Source:  Flatcar release — built and signed per Flatcar kernel version") + "\n")
	b.WriteString(dimStyle.Render("    Action:  knuckle writes /etc/flatcar/enabled-sysext.conf → auto-activates at first boot") + "\n")
	b.WriteString(dimStyle.Render("    Note:    Works with Secure Boot — no source compilation needed") + "\n\n")

	b.WriteString(accent.Render("  ② NVIDIA Container Toolkit  (nvidia-runtime from Flatcar Bakery)") + "\n")
	b.WriteString(dimStyle.Render("    Ships:   nvidia-container-runtime · nvidia-ctk · libnvidia-container") + "\n")
	b.WriteString(dimStyle.Render("    Enables: GPU access inside Docker/containerd containers") + "\n")
	b.WriteString(dimStyle.Render("    CUDA:    comes from your container images — not installed on the host") + "\n\n")

	b.WriteString(sep + "\n")
	b.WriteString("\n  Select kernel driver series:\n\n")

	// —— Driver series picker ——
	for i, opt := range model.NvidiaDriverOptions {
		cursor := "    "
		if i == m.cursor {
			cursor = "  ▸ "
		}

		recommTag := ""
		if opt.Recommended {
			recommTag = "  " + okStyle.Render("[recommended]")
		}

		labelLine := fmt.Sprintf("%s%s%s", cursor, opt.Label, recommTag)
		if i == m.cursor {
			b.WriteString(selectedStyle.Render(cursor+opt.Label) + recommTag + "\n")
			// Show description on the selected row.
			if opt.Description != "" {
				b.WriteString(dimStyle.Render("        "+opt.Description) + "\n")
			}
		} else {
			_ = labelLine
			b.WriteString(cursor + opt.Label + "\n")
		}
		b.WriteString("\n")
	}

	return b.String()
}

// wordWrap splits s into lines of at most width runes, breaking on word boundaries.
func wordWrap(s string, width int) []string {
	if width <= 0 || s == "" {
		return []string{s}
	}
	var lines []string
	words := strings.Fields(s)
	if len(words) == 0 {
		return []string{""}
	}
	current := ""
	for _, word := range words {
		wRunes := []rune(word)
		if current == "" {
			current = word
		} else if len([]rune(current))+1+len(wRunes) <= width {
			current += " " + word
		} else {
			lines = append(lines, current)
			current = word
		}
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines
}

func (m *Model) viewUpdate() string {
	var b strings.Builder
	cfg := m.Wizard.State.Config

	type option struct {
		name string
		desc []string
	}

	var options []option
	if cfg.OS == model.OSFCOS {
		b.WriteString("Update Strategy\n\nChoose how Fedora CoreOS will handle OS updates:\n\n")
		options = []option{
			{"immediate (Recommended)", []string{
				"Zincati auto-updates and reboots as soon as an update is staged.",
				"Best for: single nodes, dev environments",
			}},
			{"disabled", []string{
				"Zincati is disabled — no automatic updates.",
				"You must update manually with 'rpm-ostree upgrade'.",
				"Best for: air-gapped or manually managed infrastructure",
			}},
		}
	} else {
		b.WriteString("Update Strategy\n\nChoose how Flatcar will handle OS updates:\n\n")
		options = []option{
			{"reboot (Recommended)", []string{
				"Auto-update and reboot immediately when an update is applied.",
				"Best for: single nodes, dev environments",
			}},
			{"off", []string{
				"Updates are downloaded but never applied automatically.",
				"You must run 'update_engine_client -update' manually.",
				"Best for: manually managed infrastructure",
			}},
			{"etcd-lock", []string{
				"Coordinates reboots with other nodes via etcd distributed lock.",
				"Only one node reboots at a time in the cluster.",
				"Best for: multi-node clusters running etcd",
			}},
		}
	}

	for i, opt := range options {
		cursor := "  "
		if i == m.cursor {
			cursor = "▸ "
		}
		line := fmt.Sprintf("%s%s", cursor, opt.name)
		if i == m.cursor {
			b.WriteString(selectedStyle.Render(line))
		} else {
			b.WriteString(line)
		}
		b.WriteString("\n")
		for _, d := range opt.desc {
			fmt.Fprintf(&b, "    %s\n", d)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func (m *Model) viewInstall() string {
	var b strings.Builder
	doneStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("42"))

	var osName string
	switch m.Wizard.State.Config.OS {
	case model.OSFCOS:
		osName = "Fedora CoreOS"
	case model.OSBluefinDDI:
		osName = "Bluefin Server"
	default:
		osName = "Flatcar Container Linux"
	}
	fmt.Fprintf(&b, "Installing %s...\n\n", osName)

	// Completed phases with green checkmarks
	for _, msg := range m.Wizard.State.ProgressMessages {
		b.WriteString("  " + doneStyle.Render("✓") + " " + msg + "\n")
	}

	// Current phase with spinner + progress bar
	if m.installing {
		fmt.Fprintf(&b, "  %s Working...\n", m.spinner.View())
		b.WriteString("\n  " + m.progress.View() + "\n")
	}

	if !m.installing && len(m.Wizard.State.ProgressMessages) == 0 {
		b.WriteString("\nPress Enter to start installation...")
	}
	return b.String()
}

func (m *Model) viewDone() string {
	var b strings.Builder
	cfg := &m.Wizard.State.Config

	if cfg.DryRun {
		b.WriteString("\n✅ Installation Complete! (dry-run — no changes made)\n\n")
	} else {
		b.WriteString("\n✅ Installation Complete!\n\n")
	}

	var osName string
	switch cfg.OS {
	case model.OSFCOS:
		osName = "Fedora CoreOS"
	case model.OSBluefinDDI:
		osName = "Bluefin Server"
	default:
		osName = "Flatcar Container Linux"
	}
	fmt.Fprintf(&b, "%s has been installed:\n\n", osName)

	if cfg.Disk.Model != "" {
		fmt.Fprintf(&b, "  Disk:     %s (%s)\n", cfg.Disk.Model, cfg.Disk.SizeHuman)
	} else if cfg.Disk.DevPath != "" {
		fmt.Fprintf(&b, "  Disk:     %s\n", cfg.Disk.DevPath)
	}
	if cfg.OS != model.OSBluefinDDI && cfg.Channel != "" {
		fmt.Fprintf(&b, "  Channel:  %s\n", cfg.Channel)
	}
	if cfg.Hostname != "" {
		fmt.Fprintf(&b, "  Hostname: %s\n", cfg.Hostname)
	}
	if len(cfg.Users) > 0 && cfg.Users[0].Username != "" {
		fmt.Fprintf(&b, "  User:     %s\n", cfg.Users[0].Username)
	}

	if cfg.NvidiaDriverVersion != "" {
		dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
		accent := lipgloss.NewStyle().Foreground(lipgloss.Color("75"))
		b.WriteString("\n")
		b.WriteString(accent.Render("  NVIDIA GPU verification (run after first boot):") + "\n")
		b.WriteString(dimStyle.Render("    nvidia-smi") + "\n")
		b.WriteString(dimStyle.Render("    docker run --rm --gpus all nvidia/cuda:12.6.3-base-ubuntu22.04 nvidia-smi") + "\n")
	}

	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	link := lipgloss.NewStyle().Foreground(lipgloss.Color("75"))
	b.WriteString("\n")
	b.WriteString(dim.Render("Community & help:") + "\n")
	switch cfg.OS {
	case model.OSFCOS:
		b.WriteString("  " + link.Render("https://discussion.fedoraproject.org/tag/coreos") + dim.Render("  — Fedora CoreOS community") + "\n")
		b.WriteString("  " + link.Render("https://docs.fedoraproject.org/en-US/fedora-coreos/") + dim.Render("  — Fedora CoreOS documentation") + "\n")
	case model.OSBluefinDDI:
		b.WriteString("  " + link.Render("https://projectbluefin.io") + dim.Render("  — Bluefin project") + "\n")
		b.WriteString("  " + link.Render("https://discord.gg/projectbluefin") + dim.Render("  — Bluefin community on Discord") + "\n")
	default:
		b.WriteString("  " + link.Render("https://flatcar.org/discord") + dim.Render("  — Flatcar community on Discord") + "\n")
		b.WriteString("  " + link.Render("https://www.flatcar.org/docs/") + dim.Render("  — Flatcar documentation") + "\n")
	}

	b.WriteString("\n")
	if cfg.DryRun {
		b.WriteString("Press q to exit.\n")
	} else {
		b.WriteString("Press r twice to reboot, or q to exit.\n")
	}
	return b.String()
}

// programRunner allows tests to inject a no-op runner in place of p.Run().
// Production code always uses the default (nil → real run).
var programRunner func(p *tea.Program) error

// Run starts the Bubble Tea program. rebootFn is called when the user
// confirms reboot on the Done screen; pass nil to suppress (e.g. dry-run).
func Run(w *wizard.Wizard, rebootFn func(context.Context) error) error {
	m := New(w)
	m.rebootFn = rebootFn
	p := tea.NewProgram(m)
	if programRunner != nil {
		return programRunner(p)
	}
	_, err := p.Run()
	return err
}

// hashPassword is a thin wrapper around wizard.HashPassword kept so existing
// TUI tests/usages don't churn; new code should call wizard.HashPassword.
func hashPassword(plain string) (string, error) { return wizard.HashPassword(plain) }

// splitSSHKeys is a thin wrapper around wizard.SplitSSHKeys.
func splitSSHKeys(input string) []string { return wizard.SplitSSHKeys(input) }

// detectLocalSSHKeys finds SSH public keys on the installer host.
// Checks ~/.ssh/*.pub for common key types.
func detectLocalSSHKeys() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	pattern := filepath.Join(home, ".ssh", "*.pub")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil
	}
	var keys []string
	for _, path := range matches {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			key := strings.TrimSpace(line)
			if strings.HasPrefix(key, "ssh-") || strings.HasPrefix(key, "ecdsa-") || strings.HasPrefix(key, "sk-") {
				keys = append(keys, key)
			}
		}
	}
	return keys
}

// mergeKeys is a thin wrapper around wizard.MergeSSHKeys.
func mergeKeys(sources ...[]string) []string { return wizard.MergeSSHKeys(sources...) }
