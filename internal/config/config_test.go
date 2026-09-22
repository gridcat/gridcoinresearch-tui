package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gridcat/gridcoinresearch-tui/internal/state"
)

// TestReadConfFile ensures the conf parser picks up the keys we care about
// and silently skips the ones we don't (server=, rpcallowip=).
func TestReadConfFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gridcoinresearch.conf")
	content := `# comment line
rpcuser=alice
rpcpassword=s3cret
rpcport=15715
server=1
rpcallowip=127.0.0.1
rpcconnect=192.168.1.10
`
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	v := readConfFile(path)
	if v == nil {
		t.Fatal("expected non-nil")
	}
	if v.rpcuser != "alice" || v.rpcpassword != "s3cret" || v.rpcport != "15715" || v.rpcconnect != "192.168.1.10" {
		t.Errorf("parsed = %+v", v)
	}
}

// TestReadConfFileMissing locks in the "missing conf is not an error" rule.
// Both a nonexistent path and an empty path must return nil (signalling
// "no values found") rather than crashing.
func TestReadConfFileMissing(t *testing.T) {
	if v := readConfFile("/nonexistent/path/nope.conf"); v != nil {
		t.Errorf("expected nil for missing file, got %+v", v)
	}
	if v := readConfFile(""); v != nil {
		t.Errorf("expected nil for empty path, got %+v", v)
	}
}

// TestLoadConfigDefaults checks that running with no flags and an empty
// HOME yields the built-in fallback values (mainnet port, localhost, empty
// credentials). t.Setenv("HOME", t.TempDir()) makes sure we don't read the
// developer's real conf file.
func TestLoadConfigDefaults(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GRC_RPC_HOST", "")
	t.Setenv("GRC_RPC_PORT", "")
	t.Setenv("GRC_RPC_USER", "")
	t.Setenv("GRC_RPC_PASSWORD", "")

	cfg, err := LoadConfig([]string{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Host != "127.0.0.1" {
		t.Errorf("host = %q, want 127.0.0.1", cfg.Host)
	}
	if cfg.Port != "15715" {
		t.Errorf("port = %q, want 15715 (mainnet default)", cfg.Port)
	}
	if cfg.User != "" || cfg.Password != "" {
		t.Errorf("expected empty credentials, got user=%q pass=%q", cfg.User, cfg.Password)
	}
	if cfg.NetworkName != "mainnet" {
		t.Errorf("network = %q", cfg.NetworkName)
	}
}

// TestLoadConfigTestnetDefaults checks that --testnet flips the default port.
func TestLoadConfigTestnetDefaults(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GRC_RPC_PORT", "")
	cfg, err := LoadConfig([]string{"--testnet"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != "25715" {
		t.Errorf("testnet port = %q, want 25715", cfg.Port)
	}
}

// TestLoadConfigFlagOverridesEnv pins the cascade order: an explicit flag
// must win over an environment variable that would otherwise apply.
func TestLoadConfigFlagOverridesEnv(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GRC_RPC_HOST", "env.example")
	cfg, err := LoadConfig([]string{"--rpc-host", "flag.example"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Host != "flag.example" {
		t.Errorf("host = %q, want flag.example (flag wins)", cfg.Host)
	}
}

func TestParsePeerSharing(t *testing.T) {
	for _, v := range []string{"on", "ON", "1", "true", "yes", " on "} {
		if got := parsePeerSharing(v); got != state.PeerSharingOn {
			t.Errorf("parsePeerSharing(%q) = %q, want on", v, got)
		}
	}
	for _, v := range []string{"off", "0", "false", "no"} {
		if got := parsePeerSharing(v); got != state.PeerSharingOff {
			t.Errorf("parsePeerSharing(%q) = %q, want off", v, got)
		}
	}
	// A typo must leave the stored answer in charge rather than guessing.
	for _, v := range []string{"", "maybe", "yep"} {
		if got := parsePeerSharing(v); got != state.PeerSharingUnset {
			t.Errorf("parsePeerSharing(%q) = %q, want unset", v, got)
		}
	}
}
