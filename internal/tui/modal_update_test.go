// Tests for the self-update feature. Everything here is offline: the GitHub
// call is exercised against an httptest server, the archive/checksum logic runs
// on in-memory buffers, and the binary swap runs against a temp file, never
// the real executable.
package tui

import (
	"bytes"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/gridcat/gridcoinresearch-tui/internal/buildinfo"
	"github.com/gridcat/gridcoinresearch-tui/internal/config"
	"github.com/gridcat/gridcoinresearch-tui/internal/selfupdate"
)

func TestRenderReleaseChangelogsShowsAllMissed(t *testing.T) {
	out := renderReleaseChangelogs([]selfupdate.Release{
		{TagName: "v1.4.0", Body: "four"},
		{TagName: "v1.3.0", Body: "three\n\n### Blockchain Timestamps\nnoise"},
	})
	for _, want := range []string{"v1.4.0", "four", "v1.3.0", "three"} {
		if !bytes.Contains([]byte(out), []byte(want)) {
			t.Errorf("renderReleaseChangelogs missing %q in:\n%s", want, out)
		}
	}
	if bytes.Contains([]byte(out), []byte("Blockchain Timestamps")) {
		t.Errorf("renderReleaseChangelogs should trim stamp section, got:\n%s", out)
	}
}

func TestRenderReleaseChangelogsCapsCombinedOutput(t *testing.T) {
	releases := make([]selfupdate.Release, 0, 8)
	for i := 0; i < 8; i++ {
		releases = append(releases, selfupdate.Release{
			TagName: "v1." + strconv.Itoa(i) + ".0",
			Body:    "line a\nline b\nline c",
		})
	}
	out := renderReleaseChangelogs(releases)
	if got := len(strings.Split(out, "\n")); got > 13 {
		t.Fatalf("combined changelog rendered %d lines, want at most 13:\n%s", got, out)
	}
	if !strings.Contains(out, "see the release page") {
		t.Errorf("combined changelog should advertise truncation, got:\n%s", out)
	}
}

// TestChecksumRoundTrip ties the pieces together: a real sha256 over an archive
// must match what checksumFor parses out of a manifest.
// TestUpdateCheckBackgroundErrorDoesNotFailModal is the regression test for the
// race where a background check erroring while the user's manual check is in
// flight wrongly flipped the modal to "failed". The background error must be
// ignored by the modal, and the later manual success must still advance it.
func TestUpdateCheckBackgroundErrorDoesNotFailModal(t *testing.T) {
	m := Model{mode: modeUpdate, update: updateState{step: updateStepChecking}}

	next, _ := m.Update(updateCheckMsg{err: errors.New("network down"), manual: false})
	got := next.(Model)
	if got.update.step != updateStepChecking {
		t.Fatalf("background error moved modal to %v, want it left in checking", got.update.step)
	}

	// The user's own check then succeeds and must drive the modal forward.
	// version is "dev" in tests, so the modal shows "available" so a dev build
	// can still pull the latest.
	next2, _ := got.Update(updateCheckMsg{rel: selfupdate.Release{TagName: "v9.9.9"}, manual: true})
	got2 := next2.(Model)
	if got2.update.step != updateStepAvailable {
		t.Errorf("manual success step = %v, want available", got2.update.step)
	}
	if got2.latestVersion != "9.9.9" {
		t.Errorf("latestVersion = %q, want 9.9.9", got2.latestVersion)
	}
}

// TestUpdateCheckManualErrorFailsModal confirms a manual check's own failure
// still surfaces in the modal (the fix only silences background errors).
func TestUpdateCheckManualErrorFailsModal(t *testing.T) {
	m := Model{mode: modeUpdate, update: updateState{step: updateStepChecking}}
	next, _ := m.Update(updateCheckMsg{err: errors.New("boom"), manual: true})
	got := next.(Model)
	if got.update.step != updateStepFailed {
		t.Errorf("manual error step = %v, want failed", got.update.step)
	}
	if got.update.errMsg == "" {
		t.Errorf("failed modal should carry the error message")
	}
}

// TestUpdateCheckBackgroundUpdatesBadge confirms a silent background check still
// refreshes the cached release/version even though it doesn't touch the modal.
func TestUpdateCheckBackgroundUpdatesBadge(t *testing.T) {
	m := Model{}
	next, _ := m.Update(updateCheckMsg{rel: selfupdate.Release{TagName: "v2.0.0"}, manual: false})
	got := next.(Model)
	if got.latestVersion != "2.0.0" {
		t.Errorf("latestVersion = %q, want 2.0.0", got.latestVersion)
	}
}

func TestUpdateCheckBackgroundPreservesManualChangelog(t *testing.T) {
	oldVersion := buildinfo.Version
	defer func() { buildinfo.Version = oldVersion }()
	buildinfo.Version = "1.2.0"

	m := Model{mode: modeUpdate, update: updateState{step: updateStepChecking}}
	manual := updateCheckMsg{
		rel: selfupdate.Release{TagName: "v1.4.0"},
		missedReleases: []selfupdate.Release{
			{TagName: "v1.4.0", Body: "four"},
			{TagName: "v1.3.0", Body: "three"},
		},
		manual: true,
	}
	next, _ := m.Update(manual)
	got := next.(Model)
	if len(got.update.missedReleases) != 2 {
		t.Fatalf("manual changelog length = %d, want 2", len(got.update.missedReleases))
	}

	next, _ = got.Update(updateCheckMsg{rel: selfupdate.Release{TagName: "v1.4.0"}, manual: false})
	got = next.(Model)
	if len(got.update.missedReleases) != 2 || got.update.missedReleases[1].TagName != "v1.3.0" {
		t.Fatalf("background check replaced manual changelog: %#v", got.update.missedReleases)
	}
}

// TestNoUpdateCheckEnv covers the GRC_NO_UPDATE_CHECK opt-out spellings (the
// P3 fix), since strconv.ParseBool rejected "yes"/"on".
func TestNoUpdateCheckEnv(t *testing.T) {
	for _, v := range []string{"1", "true", "yes", "on", "YES", " On "} {
		t.Setenv("GRC_NO_UPDATE_CHECK", v)
		cfg, err := config.LoadConfig([]string{})
		if err != nil {
			t.Fatalf("LoadConfig(%q): %v", v, err)
		}
		if !cfg.NoUpdateCheck {
			t.Errorf("GRC_NO_UPDATE_CHECK=%q did not disable the check", v)
		}
	}
	for _, v := range []string{"", "0", "false", "no"} {
		t.Setenv("GRC_NO_UPDATE_CHECK", v)
		cfg, err := config.LoadConfig([]string{})
		if err != nil {
			t.Fatalf("LoadConfig(%q): %v", v, err)
		}
		if cfg.NoUpdateCheck {
			t.Errorf("GRC_NO_UPDATE_CHECK=%q wrongly disabled the check", v)
		}
	}
}
