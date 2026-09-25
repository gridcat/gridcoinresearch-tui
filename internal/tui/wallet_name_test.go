// Tests for the local wallet name: it rides the header's top border, and the
// config panel saves it per endpoint without leaking it to other endpoints.
package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/gridcat/gridcoinresearch-tui/internal/config"
	"github.com/gridcat/gridcoinresearch-tui/internal/state"
)

func TestHeaderCarriesWalletName(t *testing.T) {
	cfg := config.Config{Host: "127.0.0.1", Port: "15715"}
	m := Model{width: 100, cfg: cfg}
	plain := m.renderHeader()
	if m.windowTitle() != "gridcoinresearch-tui" {
		t.Errorf("unnamed window title = %q", m.windowTitle())
	}

	m.state.Names = map[string]string{state.NameKey(false, "127.0.0.1", "15715"): "orangepi"}
	named := m.renderHeader()
	top := strings.SplitN(named, "\n", 2)[0]
	if !strings.Contains(top, "orangepi") {
		t.Errorf("name should sit in the top border, got:\n%s", named)
	}
	if strings.Contains(named, "gridcoinresearch-tui") {
		t.Errorf("a named header should drop the program name, got:\n%s", named)
	}
	if lipgloss.Height(named) != lipgloss.Height(plain) || lipgloss.Width(named) != lipgloss.Width(plain) {
		t.Error("a name must not change the header's size")
	}
	if m.windowTitle() != "orangepi" {
		t.Errorf("window title = %q, want the name", m.windowTitle())
	}

	// Another endpoint is another wallet: its name, not this one's.
	m.cfg.Port = "15716"
	if strings.Contains(m.renderHeader(), "orangepi") {
		t.Error("a name must not follow the TUI to a different endpoint")
	}
}

func TestConfigSavesNamePerEndpoint(t *testing.T) {
	t.Setenv("GRC_STATE_DIR", t.TempDir())
	cfg := config.Config{Host: "127.0.0.1", Port: "15715", Refresh: 10e9, PeerSharing: state.PeerSharingOff}
	m := Model{cfg: cfg}

	m.openConfigModal()
	m.conf.name.SetValue("  orangepi\x1b  ")
	next, _ := m.applyConfig()
	m = next.(Model)
	if m.walletName() != "orangepi" {
		t.Fatalf("name = %q, want trimmed and sanitized", m.walletName())
	}
	if got := state.Load().Names[state.NameKey(false, "127.0.0.1", "15715")]; got != "orangepi" {
		t.Errorf("name on disk = %q", got)
	}

	// Switching the port without touching the name shows the other wallet,
	// which has no name, instead of copying this one across.
	m.openConfigModal()
	m.conf.port.SetValue("15716")
	next, _ = m.applyConfig()
	m = next.(Model)
	if m.walletName() != "" {
		t.Errorf("untouched name was copied to the new endpoint: %q", m.walletName())
	}
	if len(state.Load().Names) != 1 {
		t.Errorf("names on disk = %v, want only the first", state.Load().Names)
	}
}
