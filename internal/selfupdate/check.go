// This file is the read-only half of the self-update feature: everything
// needed to ask GitHub "is there a newer release?" and to make sense of the
// answer. It deliberately holds no process/disk side effects (those live in
// selfupdate.go) so every function here is a pure, unit-testable transform.
//
// Version strings come in two flavours the rest of this file has to reconcile:
//
//   - the binary's own build version (main.go's `version`) is UNprefixed,
//     e.g. "1.6.0", or the literal "dev" for a local `go build`.
//   - GitHub release tags are v-PREFIXED, e.g. "v1.6.0".
//
// normalizeVersion collapses both into a bare "major.minor.patch" core for
// comparison.
package selfupdate

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/gridcat/gridcoinresearch-tui/internal/buildinfo"
)

const (
	// Repo is the GitHub owner/repo the updater checks and downloads from.
	Repo = "gridcat/gridcoinresearch-tui"

	// APIBase is the default GitHub REST API base. Tests override the
	// argument to FetchLatest with an httptest server URL so they never
	// touch the network.
	APIBase = "https://api.github.com"

	// CheckInterval is how often the background check re-runs. Releases
	// are infrequent, so a long interval keeps outbound traffic minimal.
	CheckInterval = 6 * time.Hour

	// updateHTTPTimeout bounds every updater HTTP call. Deliberately short:
	// unlike the RPC client's 5-minute ceiling, a slow or blocked GitHub must
	// never make the TUI feel wedged.
	updateHTTPTimeout = 20 * time.Second

	// stampSectionMarker is the heading gridcoin-stamp-action appends to every
	// release body (introduced by a --- rule). Everything from here on is
	// on-chain notarization detail, not "what changed", so the updater strips
	// it. See TrimStampSection.
	stampSectionMarker = "### Blockchain Timestamps"
)

// Asset mirrors the one release-asset shape the updater needs.
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

// Release is the trimmed GitHub "latest release" payload the updater
// consumes. The full API response has dozens more fields we don't care about;
// encoding/json ignores the rest.
type Release struct {
	TagName string  `json:"tag_name"`
	Body    string  `json:"body"`
	Assets  []Asset `json:"assets"`
}

// newUpdateHTTPClient builds the short-timeout client used for every updater
// request. Separate from RPCClient's 5-minute client on purpose.
func newUpdateHTTPClient() *http.Client {
	return &http.Client{Timeout: updateHTTPTimeout}
}

// githubHeaders sets the headers GitHub's API expects: it rejects requests
// without a User-Agent, and asks callers to name the API version via Accept.
func githubHeaders(req *http.Request) {
	req.Header.Set("User-Agent", "gridcoinresearch-tui/"+buildinfo.Version)
	req.Header.Set("Accept", "application/vnd.github+json")
}

// FetchLatest queries GitHub for the newest published release. baseURL is
// the API root (APIBase in production; an httptest URL in tests).
func FetchLatest(baseURL string) (Release, error) {
	url := strings.TrimRight(baseURL, "/") + "/repos/" + Repo + "/releases/latest"
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return Release{}, err
	}
	githubHeaders(req)

	resp, err := newUpdateHTTPClient().Do(req)
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Release{}, githubStatusError(resp)
	}
	var rel Release
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&rel); err != nil {
		return Release{}, fmt.Errorf("decode release: %w", err)
	}
	if rel.TagName == "" {
		return Release{}, fmt.Errorf("release payload had no tag_name")
	}
	return rel, nil
}

// FetchReleases queries GitHub's release list. Manual update checks use this to
// show release notes for every version between the running binary and latest.
func FetchReleases(baseURL string) ([]Release, error) {
	url := strings.TrimRight(baseURL, "/") + "/repos/" + Repo + "/releases?per_page=100"
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	githubHeaders(req)

	resp, err := newUpdateHTTPClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, githubStatusError(resp)
	}
	var rels []Release
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&rels); err != nil {
		return nil, fmt.Errorf("decode releases: %w", err)
	}
	return rels, nil
}

func githubStatusError(resp *http.Response) error {
	// Cap the snippet so a huge error page can't blow up memory.
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return fmt.Errorf("github returned %s: %s", resp.Status, strings.TrimSpace(string(snippet)))
}

// archiveExt is the archive extension goreleaser uses for this OS: zip on
// windows, tar.gz everywhere else.
func archiveExt() string {
	if runtime.GOOS == "windows" {
		return "zip"
	}
	return "tar.gz"
}

// AssetName is the plain release archive for the running OS/arch, e.g.
// "gridcoinresearch-tui_linux_amd64.tar.gz". It must match .goreleaser.yaml's
// name_template, and deliberately does NOT match the stamp action's
// "*-stamped.*" artifacts.
func AssetName() string {
	return fmt.Sprintf("gridcoinresearch-tui_%s_%s.%s", runtime.GOOS, runtime.GOARCH, archiveExt())
}

// BinaryName is the executable's name inside the archive.
func BinaryName() string {
	if runtime.GOOS == "windows" {
		return "gridcoinresearch-tui.exe"
	}
	return "gridcoinresearch-tui"
}

// FindAsset returns the download URL of the named asset, or "" if the release
// doesn't carry it.
func (r Release) FindAsset(name string) string {
	for _, a := range r.Assets {
		if a.Name == name {
			return a.URL
		}
	}
	return ""
}

// TrimStampSection returns just the "what changed" portion of a release body,
// dropping the stamp action's trailing "Blockchain Timestamps" section and the
// horizontal rule that leads into it. If the marker is absent (older releases,
// or a future wording change) the whole body is returned unchanged so the user
// still sees *something*.
func TrimStampSection(body string) string {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	idx := strings.Index(body, stampSectionMarker)
	if idx < 0 {
		return strings.TrimSpace(body)
	}
	// Drop the trailing "---" rule line and any whitespace that introduced the
	// stamp heading.
	cut := strings.TrimRight(body[:idx], " \t\n-")
	return strings.TrimSpace(cut)
}

// normalizeVersion strips a leading "v" and any prerelease ("-rc.1") or build
// ("+meta") suffix so the bare major.minor.patch core can be compared.
// semantic-release emits clean vX.Y.Z tags, so in practice this just removes
// the tag's "v" prefix and normalizes against the binary's unprefixed version.
func normalizeVersion(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	return v
}

// CompareSemver returns -1, 0, or 1 as a is older than, equal to, or newer than
// b, comparing the numeric major.minor.patch cores. Missing components count as
// 0 ("1.2" == "1.2.0"); non-numeric junk in a field also counts as 0 so a
// malformed version can never panic.
func CompareSemver(a, b string) int {
	as := strings.Split(normalizeVersion(a), ".")
	bs := strings.Split(normalizeVersion(b), ".")
	for i := 0; i < 3; i++ {
		av, bv := 0, 0
		if i < len(as) {
			av, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			bv, _ = strconv.Atoi(bs[i])
		}
		if av != bv {
			if av < bv {
				return -1
			}
			return 1
		}
	}
	return 0
}

// IsNewer reports whether the release tag is strictly newer than the current
// build version. A "dev" (or empty) current version always returns false so
// local/dev builds don't nag with an update badge; the modal still lets the
// user pull the latest release explicitly.
func IsNewer(tag, current string) bool {
	if current == "dev" || current == "" {
		return false
	}
	return CompareSemver(tag, current) > 0
}

// Missed returns the releases whose notes should be shown before
// updating. Dev builds have no comparable installed version, so they show only
// the latest release notes while still allowing a manual update.
func Missed(current string, latest Release, releases []Release) []Release {
	if current == "dev" || current == "" {
		if latest.TagName == "" {
			return nil
		}
		return []Release{latest}
	}

	out := make([]Release, 0, len(releases)+1)
	seen := make(map[string]bool, len(releases)+1)
	for _, rel := range releases {
		if rel.TagName == "" || !IsNewer(rel.TagName, current) {
			continue
		}
		key := normalizeVersion(rel.TagName)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, rel)
	}

	latestKey := normalizeVersion(latest.TagName)
	if latest.TagName != "" && IsNewer(latest.TagName, current) && !seen[latestKey] {
		out = append([]Release{latest}, out...)
	}
	return out
}
