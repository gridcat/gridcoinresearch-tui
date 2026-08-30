// Persistent state for the TUI.
//
// This is the first thing the program stores about itself. Until peer sharing
// existed it wrote nothing to disk beyond an opt-in --debug-log and the
// self-update temp file, and that was a property worth keeping: the config
// panel is explicitly session-only.
//
// Peer sharing breaks it for one reason. Consent that has to be re-given on
// every launch is not consent, it is nagging, and the honest way to record
// "you were asked and you said yes" is to remember it. So exactly one small
// file, holding exactly the answer and the random id that goes with it.
//
// Nothing here is fatal. A missing, unreadable or corrupt state file yields
// the zero value, which means "not asked yet" — the same posture readConfFile
// takes with a missing wallet conf.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// consentVersion is bumped when the consent screen's promise changes. A
// stored answer from an older version means the user agreed to different
// wording, so they get asked again rather than being opted in to something
// they never saw.
const consentVersion = 1

// PeerSharing is the tri-state answer. Unset is meaningfully different from
// off: it means "never asked", which is what triggers the consent screen.
type PeerSharing string

const (
	PeerSharingUnset PeerSharing = ""
	PeerSharingOn    PeerSharing = "on"
	PeerSharingOff   PeerSharing = "off"
)

// State is the on-disk shape. Keep it small and boring; anything that is not
// a user decision belongs in memory.
type State struct {
	PeerSharing PeerSharing `json:"peer_sharing"`
	// ReporterID is a random value generated locally at consent time. It lets
	// the collector count distinct vantage points and rate-limit one wallet
	// without knowing anything about who that wallet is: it is derived from
	// nothing, tied to no address, and the server only ever stores a hash of
	// it. Regenerating it is as simple as deleting the state file.
	ReporterID     string `json:"reporter_id,omitempty"`
	ConsentVersion int    `json:"consent_version,omitempty"`
	ConsentedAt    string `json:"consented_at,omitempty"`
}

// statePath returns the state file location, "" if the OS gives us nowhere
// sensible to write.
//
// GRC_STATE_DIR redirects it, which is how the tests keep off a developer's
// real config directory. Undocumented in the README for the same reason
// GRC_PEER_SHARING_URL is: it exists for tests and local development, not for
// users. (XDG_CONFIG_HOME would only work on Linux; macOS ignores it.)
func statePath() string {
	if dir := strings.TrimSpace(os.Getenv("GRC_STATE_DIR")); dir != "" {
		return filepath.Join(dir, "state.json")
	}
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		return ""
	}
	return filepath.Join(dir, "gridcoinresearch-tui", "state.json")
}

// loadState reads the state file. Any problem returns the zero value: a
// corrupt file must not stop the TUI from starting, and "unset" is a safe
// thing to fall back to because it only means we will ask again.
func loadState() State {
	path := statePath()
	if path == "" {
		return State{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return State{}
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return State{}
	}
	// A consent recorded against older wording does not carry over.
	if s.PeerSharing == PeerSharingOn && s.ConsentVersion != consentVersion {
		return State{}
	}
	return s
}

// saveState writes the state file atomically: temp file in the same directory
// then rename, so an interrupted write cannot leave a truncated file that
// loadState would silently read as "never asked".
func saveState(s State) error {
	path := statePath()
	if path == "" {
		return nil
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".state-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// 0600: the reporter id is not a secret, but it is an identifier, and
	// there is no reason for anyone else on the box to read it.
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// newReporterID makes a fresh random identifier. 16 bytes of crypto/rand,
// hex-encoded to the 32 lowercase hex characters the collector validates.
func newReporterID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// ensureReporterID makes sure a state carries an identifier, minting one if it
// does not.
//
// This exists for the launch-only opt-in. `--peer-sharing=on` and
// GRC_PEER_SHARING=on deliberately record no consent answer, so on a fresh
// install there is no stored identifier either, and a report without one is
// dropped before it is ever built. Minting here is what makes the documented
// scripted and containerised opt-in actually report.
//
// Only the identifier is written; PeerSharing is left exactly as it was, so a
// human who has never been asked still meets the consent screen on their next
// interactive launch. A failed save is not fatal: the identifier is returned
// either way and this run reports under it, the only cost being that the next
// launch mints a fresh one and the collector counts it as a new vantage point.
func ensureReporterID(prev State) State {
	if prev.ReporterID != "" {
		return prev
	}
	id, err := newReporterID()
	if err != nil {
		// No entropy, no identifier. The caller's guard keeps us from
		// reporting rather than sending something predictable.
		return prev
	}
	next := prev
	next.ReporterID = id
	_ = saveState(next)
	return next
}

// recordConsent persists the user's answer, minting a reporter id the first
// time they say yes. Returns the updated state so the caller can hold it
// without re-reading the file.
func recordConsent(prev State, share bool) (State, error) {
	next := prev
	next.ConsentVersion = consentVersion
	next.ConsentedAt = time.Now().UTC().Format(time.RFC3339)
	if share {
		next.PeerSharing = PeerSharingOn
		if next.ReporterID == "" {
			id, err := newReporterID()
			if err != nil {
				return prev, err
			}
			next.ReporterID = id
		}
	} else {
		next.PeerSharing = PeerSharingOff
		// The id is kept rather than cleared: turning sharing back on later
		// should not look like a brand-new reporter to the collector, which
		// would quietly inflate its count of independent vantage points.
	}
	return next, saveState(next)
}
