// Tests for the self-update feature. Everything here is offline: the GitHub
// call is exercised against an httptest server, the archive/checksum logic runs
// on in-memory buffers, and the binary swap runs against a temp file — never
// the real executable.
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCompareSemver(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.6.0", "1.6.0", 0},
		{"v1.6.0", "1.6.0", 0}, // "v" prefix normalized away
		{"1.6.1", "1.6.0", 1},
		{"1.6.0", "1.6.1", -1},
		{"2.0.0", "1.9.9", 1},
		{"1.10.0", "1.9.0", 1},     // numeric, not lexical
		{"1.6", "1.6.0", 0},        // missing patch == 0
		{"1.6.0-rc.1", "1.6.0", 0}, // prerelease suffix stripped for the core
		{"garbage", "1.0.0", -1},   // junk fields count as 0
	}
	for _, c := range cases {
		if got := compareSemver(c.a, c.b); got != c.want {
			t.Errorf("compareSemver(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestIsNewer(t *testing.T) {
	cases := []struct {
		tag, current string
		want         bool
	}{
		{"v1.6.1", "1.6.0", true},
		{"v1.6.0", "1.6.0", false},
		{"v1.5.0", "1.6.0", false},
		{"v9.9.9", "dev", false}, // dev builds never nag
		{"v9.9.9", "", false},    // empty current treated like dev
	}
	for _, c := range cases {
		if got := isNewer(c.tag, c.current); got != c.want {
			t.Errorf("isNewer(%q, %q) = %v, want %v", c.tag, c.current, got, c.want)
		}
	}
}

func TestTrimStampSection(t *testing.T) {
	// A real-shaped v1.6.0 body: semantic-release + goreleaser changelog, then
	// the stamp action's appended "Blockchain Timestamps" section.
	body := "## 1.6.0 (2026-07-18)\n\n#### Feature\n\n* introduce polls previews (3a8bc68c)\n\n" +
		"## Changelog\n* 3a8bc68c feat: introduce polls previews\n\n\n---\n" +
		"### Blockchain Timestamps (Gridcoin)\n\n| file | sha256 | proof |\n| --- | --- | --- |\n"
	got := trimStampSection(body)
	if want := "introduce polls previews"; !bytes.Contains([]byte(got), []byte(want)) {
		t.Errorf("trimmed changelog lost the real notes: %q", got)
	}
	if bytes.Contains([]byte(got), []byte("Blockchain Timestamps")) {
		t.Errorf("trimmed changelog still contains the stamp section: %q", got)
	}
	if bytes.HasSuffix([]byte(got), []byte("-")) {
		t.Errorf("trailing rule not stripped: %q", got)
	}

	// No marker → body returned intact (older releases).
	plain := "## 1.0.0\n\n* first release"
	if got := trimStampSection(plain); got != plain {
		t.Errorf("trimStampSection(no marker) = %q, want unchanged %q", got, plain)
	}
}

func TestAssetName(t *testing.T) {
	// The name must match .goreleaser.yaml exactly and never be the -stamped one.
	name := assetName()
	wantExt := ".tar.gz"
	if runtime.GOOS == "windows" {
		wantExt = ".zip"
	}
	if filepath.Ext(name) == "" {
		t.Fatalf("assetName() = %q, expected an extension", name)
	}
	if !bytes.HasSuffix([]byte(name), []byte(wantExt)) {
		t.Errorf("assetName() = %q, want suffix %q", name, wantExt)
	}
	if bytes.Contains([]byte(name), []byte("stamped")) {
		t.Errorf("assetName() = %q must not be the stamped artifact", name)
	}
}

func TestFindAsset(t *testing.T) {
	rel := releaseInfo{Assets: []releaseAsset{
		{Name: "checksums.txt", URL: "https://example/checksums.txt"},
		{Name: "gridcoinresearch-tui_linux_amd64.tar.gz", URL: "https://example/linux"},
	}}
	if got := rel.findAsset("checksums.txt"); got != "https://example/checksums.txt" {
		t.Errorf("findAsset(checksums.txt) = %q", got)
	}
	if got := rel.findAsset("nope.tar.gz"); got != "" {
		t.Errorf("findAsset(missing) = %q, want empty", got)
	}
}

func TestChecksumFor(t *testing.T) {
	manifest := "abc123  gridcoinresearch-tui_linux_amd64.tar.gz\n" +
		"def456  gridcoinresearch-tui_darwin_arm64.tar.gz\n"
	got, err := checksumFor(manifest, "gridcoinresearch-tui_darwin_arm64.tar.gz")
	if err != nil {
		t.Fatalf("checksumFor: %v", err)
	}
	if got != "def456" {
		t.Errorf("checksumFor = %q, want def456", got)
	}
	if _, err := checksumFor(manifest, "absent.tar.gz"); err == nil {
		t.Errorf("checksumFor(absent) should error")
	}
}

// makeTarGz builds an in-memory tar.gz containing one file.
func makeTarGz(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestExtractBinaryTarGz(t *testing.T) {
	want := []byte("#!/fake/binary\x00\x01\x02")
	// Name the entry as the archive would (binaryName is GOOS-aware); the tar
	// also carries LICENSE/README, which extractBinary must skip.
	archive := makeTarGzMulti(t, map[string][]byte{
		"LICENSE":    []byte("license text"),
		binaryName(): want,
		"README.md":  []byte("readme"),
	})
	got, err := extractBinary(archive, "gridcoinresearch-tui_linux_amd64.tar.gz")
	if err != nil {
		t.Fatalf("extractBinary: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("extracted %q, want %q", got, want)
	}
}

// makeTarGzMulti builds an in-memory tar.gz with several files.
func makeTarGzMulti(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestSwapBinary(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "grctui")
	if err := os.WriteFile(exe, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	newContent := []byte("NEW BINARY BYTES")
	if err := swapBinary(exe, newContent); err != nil {
		t.Fatalf("swapBinary: %v", err)
	}
	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, newContent) {
		t.Errorf("after swap, exe = %q, want %q", got, newContent)
	}
	fi, err := os.Stat(exe)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o755 {
		t.Errorf("mode after swap = %v, want 0755", fi.Mode().Perm())
	}
	// No staging leftovers.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("temp dir has %d entries after swap, want 1 (leftover staging file?)", len(entries))
	}
}

func TestFetchLatestRelease(t *testing.T) {
	rel := releaseInfo{
		TagName: "v1.6.0",
		Body:    "notes",
		Assets:  []releaseAsset{{Name: "checksums.txt", URL: "https://x/checksums.txt"}},
	}
	var gotUA, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		gotPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(rel)
	}))
	defer srv.Close()

	got, err := fetchLatestRelease(srv.URL)
	if err != nil {
		t.Fatalf("fetchLatestRelease: %v", err)
	}
	if got.TagName != "v1.6.0" {
		t.Errorf("tag = %q, want v1.6.0", got.TagName)
	}
	if gotUA == "" {
		t.Errorf("no User-Agent sent (GitHub rejects those)")
	}
	if wantPath := "/repos/" + updateRepo + "/releases/latest"; gotPath != wantPath {
		t.Errorf("request path = %q, want %q", gotPath, wantPath)
	}
}

func TestFetchLatestReleaseHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusForbidden)
	}))
	defer srv.Close()
	if _, err := fetchLatestRelease(srv.URL); err == nil {
		t.Errorf("expected an error on HTTP 403")
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
	next2, _ := got.Update(updateCheckMsg{rel: releaseInfo{TagName: "v9.9.9"}, manual: true})
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
	next, _ := m.Update(updateCheckMsg{rel: releaseInfo{TagName: "v2.0.0"}, manual: false})
	got := next.(Model)
	if got.latestVersion != "2.0.0" {
		t.Errorf("latestVersion = %q, want 2.0.0", got.latestVersion)
	}
}

// TestNoUpdateCheckEnv covers the GRC_NO_UPDATE_CHECK opt-out spellings — the
// P3 fix, since strconv.ParseBool rejected "yes"/"on".
func TestNoUpdateCheckEnv(t *testing.T) {
	for _, v := range []string{"1", "true", "yes", "on", "YES", " On "} {
		t.Setenv("GRC_NO_UPDATE_CHECK", v)
		cfg, err := LoadConfig([]string{})
		if err != nil {
			t.Fatalf("LoadConfig(%q): %v", v, err)
		}
		if !cfg.NoUpdateCheck {
			t.Errorf("GRC_NO_UPDATE_CHECK=%q did not disable the check", v)
		}
	}
	for _, v := range []string{"", "0", "false", "no"} {
		t.Setenv("GRC_NO_UPDATE_CHECK", v)
		cfg, err := LoadConfig([]string{})
		if err != nil {
			t.Fatalf("LoadConfig(%q): %v", v, err)
		}
		if cfg.NoUpdateCheck {
			t.Errorf("GRC_NO_UPDATE_CHECK=%q wrongly disabled the check", v)
		}
	}
}

func TestChecksumRoundTrip(t *testing.T) {
	archive := makeTarGz(t, binaryName(), []byte("payload"))
	sum := sha256.Sum256(archive)
	hexsum := hex.EncodeToString(sum[:])
	manifest := hexsum + "  " + assetName() + "\n"
	got, err := checksumFor(manifest, assetName())
	if err != nil {
		t.Fatalf("checksumFor: %v", err)
	}
	if got != hexsum {
		t.Errorf("checksumFor = %q, want %q", got, hexsum)
	}
}
