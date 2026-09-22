// State is the first thing this program stores about itself, so these tests
// lean on the two properties that matter: a broken file must never stop the
// TUI starting, and a recorded consent must actually survive a restart.
package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadStateMissingFileIsUnset(t *testing.T) {
	t.Setenv("GRC_STATE_DIR", t.TempDir())
	got := Load()
	if got.PeerSharing != PeerSharingUnset {
		t.Errorf("a missing file should read as never-asked, got %q", got.PeerSharing)
	}
}

func TestLoadStateCorruptFileIsUnset(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GRC_STATE_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Falling back to "never asked" means we ask again, which is the safe
	// direction: the alternative is silently treating garbage as consent.
	if got := Load(); got.PeerSharing != PeerSharingUnset {
		t.Errorf("corrupt file should read as never-asked, got %q", got.PeerSharing)
	}
}

func TestConsentRoundTrips(t *testing.T) {
	t.Setenv("GRC_STATE_DIR", t.TempDir())

	saved, err := RecordConsent(Load(), true)
	if err != nil {
		t.Fatal(err)
	}
	if saved.PeerSharing != PeerSharingOn {
		t.Errorf("expected on, got %q", saved.PeerSharing)
	}
	if len(saved.ReporterID) != 32 {
		t.Errorf("expected a 32-char reporter id, got %q", saved.ReporterID)
	}
	if saved.ConsentedAt == "" {
		t.Error("expected the consent to be timestamped")
	}

	reloaded := Load()
	if reloaded.PeerSharing != PeerSharingOn || reloaded.ReporterID != saved.ReporterID {
		t.Errorf("consent did not survive a reload: %+v", reloaded)
	}
}

func TestConsentDeclineStoresOffAndKeepsID(t *testing.T) {
	t.Setenv("GRC_STATE_DIR", t.TempDir())

	on, err := RecordConsent(Load(), true)
	if err != nil {
		t.Fatal(err)
	}
	off, err := RecordConsent(on, false)
	if err != nil {
		t.Fatal(err)
	}
	if off.PeerSharing != PeerSharingOff {
		t.Errorf("expected off, got %q", off.PeerSharing)
	}
	// Keeping the id means turning sharing back on later does not look like a
	// brand-new reporter, which would inflate the collector's count of
	// independent vantage points.
	if off.ReporterID != on.ReporterID {
		t.Error("declining should not discard the reporter id")
	}
}

func TestDeclineWithoutEverConsentingMintsNoID(t *testing.T) {
	t.Setenv("GRC_STATE_DIR", t.TempDir())
	off, err := RecordConsent(Load(), false)
	if err != nil {
		t.Fatal(err)
	}
	// Someone who said no has no reason to be given an identifier at all.
	if off.ReporterID != "" {
		t.Errorf("saying no should not mint an id, got %q", off.ReporterID)
	}
}

func TestStaleConsentVersionIsReasked(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GRC_STATE_DIR", dir)
	// A stored "yes" against older wording is not consent to the current
	// wording, so it must not carry over.
	old := `{"peer_sharing":"on","reporter_id":"a1b2c3d4e5f60718293a4b5c6d7e8f90","consent_version":0}`
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := Load(); got.PeerSharing != PeerSharingUnset {
		t.Errorf("stale consent version should re-ask, got %q", got.PeerSharing)
	}
}

func TestStateFileIsPrivate(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GRC_STATE_DIR", dir)
	if _, err := RecordConsent(State{}, true); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("state file mode = %o, want 600", perm)
	}
}

func TestReporterIDsAreDistinct(t *testing.T) {
	a, err := newReporterID()
	if err != nil {
		t.Fatal(err)
	}
	b, err := newReporterID()
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Error("reporter ids must not repeat")
	}
}
