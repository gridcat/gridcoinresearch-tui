package tui

import (
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/gridcat/gridcoinresearch-tui/internal/config"
	"github.com/gridcat/gridcoinresearch-tui/internal/format"
	"github.com/gridcat/gridcoinresearch-tui/internal/rpc"
	"github.com/gridcat/gridcoinresearch-tui/internal/state"
	"github.com/gridcat/gridcoinresearch-tui/internal/theme"
	"github.com/gridcat/gridcoinresearch-tui/internal/ui"
)

func (m *Model) openConfigModal() {
	m.mode = modeConfig
	m.conf = newConfigState(m.cfg, m.sharingOn, m.walletName())
	m.conf.focused = cfgFieldNetwork
}

func (m *Model) focusConfigField(f configField) {
	m.conf.blurAll()
	m.conf.focused = f
	if ti := m.conf.inputFor(f); ti != nil {
		ti.Focus()
	}
}

func (m Model) handleConfigKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key == "esc" || key == "ctrl+c" {
		m.mode = modeDashboard
		m.conf.blurAll()
		return m, nil
	}
	// Navigation keys work regardless of which row is focused.
	switch key {
	case "tab", "down":
		// Wrap-around: modulo by cfgFieldCount cycles the focus through rows.
		m.focusConfigField((m.conf.focused + 1) % cfgFieldCount)
		return m, nil
	case "shift+tab", "up":
		m.focusConfigField((m.conf.focused - 1 + cfgFieldCount) % cfgFieldCount)
		return m, nil
	}

	// Per-field key handling.
	switch m.conf.focused {
	case cfgFieldNetwork:
		if key == " " || key == "enter" || key == "left" || key == "right" || key == "t" || key == "m" {
			// If the port field still holds the old network's default, migrate
			// it to the new network's default so users don't have to remember.
			oldDefault := config.DefaultPort(m.conf.testnet)
			m.conf.testnet = !m.conf.testnet
			if strings.TrimSpace(m.conf.port.Value()) == oldDefault {
				m.conf.port.SetValue(config.DefaultPort(m.conf.testnet))
			}
		}
		return m, nil
	case cfgFieldPeerSharing:
		if key == " " || key == "enter" || key == "left" || key == "right" {
			m.conf.peerSharing = !m.conf.peerSharing
		}
		return m, nil
	case cfgFieldApply:
		if key == "enter" || key == " " {
			return m.applyConfig()
		}
		return m, nil
	}

	// Any other focused field is a textinput, so delegate the keystroke.
	if ti := m.conf.inputFor(m.conf.focused); ti != nil {
		var cmd tea.Cmd
		*ti, cmd = ti.Update(msg)
		return m, cmd
	}
	return m, nil
}

// applyConfig validates the form, copies the values into m.cfg, rebuilds
// the RPC client against the new endpoint, clears the per-source caches,
// and kicks off a fresh refresh batch. Anything typed into the form that
// fails validation leaves the modal open with an errMsg.
func (m Model) applyConfig() (tea.Model, tea.Cmd) {
	host := strings.TrimSpace(m.conf.host.Value())
	if host == "" {
		m.conf.errMsg = "host cannot be empty"
		return m, nil
	}
	port := strings.TrimSpace(m.conf.port.Value())
	if _, err := strconv.Atoi(port); err != nil || port == "" {
		m.conf.errMsg = "port must be a number"
		return m, nil
	}
	refresh, err := time.ParseDuration(strings.TrimSpace(m.conf.refresh.Value()))
	if err != nil || refresh < time.Second {
		m.conf.errMsg = "refresh must be a duration >= 1s (e.g. 5s, 30s, 1m)"
		return m, nil
	}
	m.conf.errMsg = ""

	// The name is saved to disk, under the endpoint the form now points at.
	// Only an edit is saved: changing just the port means "show me that
	// wallet", which should bring up that wallet's own name rather than copy
	// this one's across. This runs before anything else is applied, so a
	// failed save can keep the modal open and show the error.
	name := strings.TrimSpace(format.SanitizeTerminal(m.conf.name.Value()))
	if name != m.conf.origName {
		next, err := state.SetName(m.state, state.NameKey(m.conf.testnet, host, port), name)
		if err != nil {
			m.conf.errMsg = "could not save name: " + err.Error()
			return m, nil
		}
		m.state = next
	}

	m.cfg.Testnet = m.conf.testnet
	if m.conf.testnet {
		m.cfg.NetworkName = "testnet"
	} else {
		m.cfg.NetworkName = "mainnet"
	}
	// Repaint the chrome so a network toggle takes effect immediately rather
	// than waiting for a restart.
	theme.ApplyNetwork(m.cfg.Testnet)
	m.cfg.Host = host
	m.cfg.Port = port
	m.cfg.User = strings.TrimSpace(m.conf.user.Value())
	// m.cfg.Password is intentionally NOT touched here: the password is
	// read-only in the config modal, so we preserve whatever was resolved
	// at startup from flag/env/conf.
	m.cfg.Refresh = refresh

	// Rebuild the RPC client against the new endpoint and flush every
	// cached response / error so the dashboard starts fresh.
	m.rpc = rpc.New(rpc.Options{URL: m.cfg.URL(), User: m.cfg.User, Password: m.cfg.Password})
	m.loaded = false
	m.txsLoaded = false
	m.addrsLoaded = false
	m.peersLoaded = false
	// Drop the cached peers with everything else. They belong to the daemon we
	// just stopped talking to, and a peer-sharing beat that lands before the
	// new getpeerinfo answers would otherwise report them under the new
	// network tag, filing mainnet peers as testnet ones.
	m.peers = nil
	m.txs = nil
	m.txsLastBlock = "" // force a full re-seed against the new daemon
	m.addresses = nil
	m.walletErr = ""
	m.txsErr = ""
	m.addrsErr = ""
	m.mode = modeDashboard

	// Peer sharing and the name above are the only fields written to disk.
	// Everything else here is deliberately session-only (see the README), but
	// a consent decision the program forgets on exit is not a decision, and
	// re-asking on every launch would be nagging rather than consent.
	//
	// A flag or env var wins for the whole launch, so the toggle is inert in
	// that case; say so instead of silently discarding the change.
	if m.conf.peerSharing != m.sharingOn {
		if m.cfg.PeerSharing != state.PeerSharingUnset {
			m.sharingNote = "peer sharing is fixed by --peer-sharing for this run"
		} else {
			next, err := state.RecordConsent(m.state, m.conf.peerSharing)
			if err != nil {
				// Keep the in-memory answer in step with what is on disk: if
				// we could not save it, we do not pretend it took.
				m.conf.peerSharing = m.sharingOn
				m.sharingNote = "could not save peer sharing: " + err.Error()
			} else {
				m.state = next
				m.sharingOn = m.conf.peerSharing
				// Forget any booked slot either way. Switching on should wait
				// a full interval, not fire against a slot left over from the
				// last time it was on; switching off should leave nothing
				// behind to fire against. The heartbeat itself keeps running
				// regardless, so there is no timer to start or stop here.
				m.nextReportAt = time.Time{}
				if m.sharingOn {
					m.sharingNote = "peer sharing on"
				} else {
					m.sharingNote = "peer sharing off"
				}
			}
		}
	}

	// Don't start a new tickCmd here: the lineage seeded in Init re-arms itself
	// on every tickMsg and never stops, so it already keeps polling at the new
	// cfg.Refresh. Starting another would leak a second self-re-arming tick (and
	// double the refresh rate) on every Apply.
	spin := m.bumpInflight(6)
	return m, tea.Batch(m.refreshAllCmd(), spin, tea.SetWindowTitle(m.windowTitle()))
}

func (m Model) renderConfigModal() string {
	row := func(label string, field configField, value string) string {
		prefix := "  "
		labelStyle := theme.ConfigLabel
		if m.conf.focused == field {
			prefix = theme.Accent.Render("▸ ")
			labelStyle = theme.ConfigLabelFocused
			value = theme.ConfigValueFocused.Render(value)
		}
		return lipgloss.JoinHorizontal(lipgloss.Top, prefix, labelStyle.Render(label), value)
	}

	networkValue := "mainnet"
	if m.conf.testnet {
		networkValue = "testnet"
	}
	networkLine := row("Network", cfgFieldNetwork,
		networkValue+"  "+theme.Muted.Render("(space/←→ to toggle)"))

	hostLine := row("Host", cfgFieldHost, m.conf.host.View())
	portLine := row("Port", cfgFieldPort, m.conf.port.View())
	userLine := row("User", cfgFieldUser, m.conf.user.View())
	refreshLine := row("Refresh", cfgFieldRefresh, m.conf.refresh.View())
	nameLine := row("Name", cfgFieldName, m.conf.name.View())
	nameHelp := lipgloss.JoinHorizontal(lipgloss.Top,
		"  ",
		theme.ConfigLabel.Render(""),
		theme.Muted.Render("shown in the header, remembered for this host:port"),
	)

	// Peer sharing: the only row here that outlives the session. Show what it
	// does rather than just on/off, because "Peer sharing" alone tells a user
	// nothing about what leaves their machine.
	sharingValue := theme.Muted.Render("○ off")
	if m.conf.peerSharing {
		sharingValue = theme.Good.Render("● on")
	}
	if m.cfg.PeerSharing != state.PeerSharingUnset {
		// Fixed by --peer-sharing / GRC_PEER_SHARING for this launch, so the
		// toggle cannot take effect. Better to say so than to let it flip and
		// quietly do nothing.
		sharingValue += "  " + theme.Warn.Render("(fixed by flag/env)")
	} else {
		sharingValue += "  " + theme.Muted.Render("(space/←→ to toggle)")
	}
	sharingLine := row("Peer sharing", cfgFieldPeerSharing, sharingValue)
	sharingHelp := lipgloss.JoinHorizontal(lipgloss.Top,
		"  ",
		theme.ConfigLabel.Render(""),
		theme.Muted.Render("share the peers you connect to, to help the public node list"),
	)

	// Password is read-only, we only show whether it was resolved from
	// flag/env/conf at startup. This keeps the passphrase off screen and
	// saves the user from re-typing it to tweak unrelated fields.
	passStatus := theme.Muted.Render("not set")
	if m.cfg.Password != "" {
		passStatus = theme.Good.Render("● set (read-only)")
	}
	passLine := lipgloss.JoinHorizontal(lipgloss.Top,
		"  ",
		theme.ConfigLabel.Render("Password"),
		passStatus,
	)

	applyPrefix := "  "
	applyLabel := theme.Muted.Render("[ Apply ]")
	if m.conf.focused == cfgFieldApply {
		applyPrefix = theme.Accent.Render("▸ ")
		applyLabel = theme.Accent.Render("[ Apply ]")
	}
	applyLine := lipgloss.JoinHorizontal(lipgloss.Top, applyPrefix, strings.Repeat(" ", 12), applyLabel)

	srcLine := ""
	if m.cfg.ConfPath != "" {
		srcLine = theme.Muted.Render("loaded from: " + m.cfg.ConfPath)
	} else {
		srcLine = theme.Muted.Render("no conf file read; values from flags/env/defaults")
	}

	errLine := ""
	if m.conf.errMsg != "" {
		errLine = "\n" + theme.Bad.Render(m.conf.errMsg)
	}

	hint := theme.Muted.Render("tab/↓ next · shift+tab/↑ prev · enter on Apply to save · esc to cancel")

	body := lipgloss.JoinVertical(lipgloss.Left,
		networkLine,
		hostLine,
		portLine,
		userLine,
		passLine,
		refreshLine,
		nameLine,
		nameHelp,
		sharingLine,
		sharingHelp,
		"",
		applyLine,
		"",
		srcLine,
	) + errLine + "\n\n" + hint

	modal := ui.ModalBox(68, "Config", body)

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, modal)
}

// configField is a type-safe enum for rows in the config modal. iota gives
// each constant a unique integer starting from 0, so they can be compared
// and used as array indices.
type configField int

const (
	cfgFieldNetwork configField = iota
	cfgFieldHost
	cfgFieldPort
	cfgFieldUser
	cfgFieldRefresh
	cfgFieldName
	cfgFieldPeerSharing
	cfgFieldApply
	cfgFieldCount // sentinel: not a real field, used for modulo in tab navigation
)

// configState holds everything the config modal needs while it is open.
// Each editable row owns a textinput.Model (which handles cursor/typing),
// and errMsg is shown underneath the form when validation fails.
//
// Note: the RPC password is NOT editable here. It is shown as a read-only
// status line (see renderConfigModal) so shoulder-surfers can't reveal it
// and so the user doesn't have to re-type it to tweak unrelated fields.
type configState struct {
	focused configField
	testnet bool
	host    textinput.Model
	port    textinput.Model
	user    textinput.Model
	refresh textinput.Model
	// name is the wallet's local name. Like peer sharing it IS written to
	// disk (see applyConfig); origName is what the form opened with, so
	// applying can tell an edit from an untouched field.
	name     textinput.Model
	origName string
	// peerSharing is a boolean row toggled with space/←→, like the network
	// row. Like the name, and unlike every other field in this modal, it IS
	// written to disk when applied (see applyConfig). Consent that forgets
	// itself is not consent.
	peerSharing bool
	errMsg      string
}

func (cs *configState) blurAll() {
	cs.host.Blur()
	cs.port.Blur()
	cs.user.Blur()
	cs.refresh.Blur()
	cs.name.Blur()
}

// inputFor maps a configField enum to the pointer of the matching text
// input so the dispatch switch in app_update.go becomes a single line instead
// of a five-way copy-paste. Returns nil for rows that are not editable
// text inputs (Network toggle and Apply button), which is a signal to the
// caller to handle them specially.
func (cs *configState) inputFor(f configField) *textinput.Model {
	switch f {
	case cfgFieldHost:
		return &cs.host
	case cfgFieldPort:
		return &cs.port
	case cfgFieldUser:
		return &cs.user
	case cfgFieldRefresh:
		return &cs.refresh
	case cfgFieldName:
		return &cs.name
	}
	return nil
}

// newConfigState builds a fresh configState pre-populated with the values
// currently in the live Config. Used both for the initial Model and for
// resetting the form each time the config modal is opened.
func newConfigState(cfg config.Config, sharingOn bool, name string) configState {
	mk := func(value string, width int) textinput.Model {
		ti := textinput.New()
		ti.SetValue(value)
		ti.CharLimit = 128
		ti.Width = width
		return ti
	}
	nameInput := mk(name, 24)
	nameInput.CharLimit = 24
	nameInput.Placeholder = "e.g. orangepi-main"
	return configState{
		testnet:     cfg.Testnet,
		host:        mk(cfg.Host, 30),
		port:        mk(cfg.Port, 10),
		user:        mk(cfg.User, 30),
		refresh:     mk(cfg.Refresh.String(), 10),
		name:        nameInput,
		origName:    name,
		peerSharing: sharingOn,
	}
}
