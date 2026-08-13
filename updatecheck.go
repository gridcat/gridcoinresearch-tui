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
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	// updateRepo is the GitHub owner/repo the updater checks and downloads from.
	updateRepo = "gridcat/gridcoinresearch-tui"

	// updateAPIBase is the default GitHub REST API base. Tests override the
	// argument to fetchLatestRelease with an httptest server URL so they never
	// touch the network.
	updateAPIBase = "https://api.github.com"

	// updateCheckInterval is how often the background check re-runs. Releases
	// are infrequent, so a long interval keeps outbound traffic minimal.
	updateCheckInterval = 6 * time.Hour

	// updateHTTPTimeout bounds every updater HTTP call. Deliberately short —
	// unlike the RPC client's 5-minute ceiling, a slow or blocked GitHub must
	// never make the TUI feel wedged.
	updateHTTPTimeout = 20 * time.Second

	// stampSectionMarker is the heading gridcoin-stamp-action appends to every
	// release body (introduced by a --- rule). Everything from here on is
	// on-chain notarization detail, not "what changed", so the updater strips
	// it — see trimStampSection.
	stampSectionMarker = "### Blockchain Timestamps"
)

// releaseAsset mirrors the one release-asset shape the updater needs.
type releaseAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

// releaseInfo is the trimmed GitHub "latest release" payload the updater
// consumes. The full API response has dozens more fields we don't care about;
// encoding/json ignores the rest.
type releaseInfo struct {
	TagName string         `json:"tag_name"`
	Body    string         `json:"body"`
	Assets  []releaseAsset `json:"assets"`
}

// newUpdateHTTPClient builds the short-timeout client used for every updater
// request. Separate from RPCClient's 5-minute client on purpose.
func newUpdateHTTPClient() *http.Client {
	return &http.Client{Timeout: updateHTTPTimeout}
}

// githubHeaders sets the headers GitHub's API expects: it rejects requests
// without a User-Agent, and asks callers to name the API version via Accept.
func githubHeaders(req *http.Request) {
	req.Header.Set("User-Agent", "gridcoinresearch-tui/"+version)
	req.Header.Set("Accept", "application/vnd.github+json")
}

// fetchLatestRelease queries GitHub for the newest published release. baseURL is
// the API root (updateAPIBase in production; an httptest URL in tests).
func fetchLatestRelease(baseURL string) (releaseInfo, error) {
	url := strings.TrimRight(baseURL, "/") + "/repos/" + updateRepo + "/releases/latest"
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return releaseInfo{}, err
	}
	githubHeaders(req)

	resp, err := newUpdateHTTPClient().Do(req)
	if err != nil {
		return releaseInfo{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return releaseInfo{}, githubStatusError(resp)
	}
	var rel releaseInfo
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&rel); err != nil {
		return releaseInfo{}, fmt.Errorf("decode release: %w", err)
	}
	if rel.TagName == "" {
		return releaseInfo{}, fmt.Errorf("release payload had no tag_name")
	}
	return rel, nil
}

// fetchReleases queries GitHub's release list. Manual update checks use this to
// show release notes for every version between the running binary and latest.
func fetchReleases(baseURL string) ([]releaseInfo, error) {
	url := strings.TrimRight(baseURL, "/") + "/repos/" + updateRepo + "/releases?per_page=100"
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
	var rels []releaseInfo
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

// assetName is the plain release archive for the running OS/arch, e.g.
// "gridcoinresearch-tui_linux_amd64.tar.gz". It must match .goreleaser.yaml's
// name_template, and deliberately does NOT match the stamp action's
// "*-stamped.*" artifacts.
func assetName() string {
	return fmt.Sprintf("gridcoinresearch-tui_%s_%s.%s", runtime.GOOS, runtime.GOARCH, archiveExt())
}

// binaryName is the executable's name inside the archive.
func binaryName() string {
	if runtime.GOOS == "windows" {
		return "gridcoinresearch-tui.exe"
	}
	return "gridcoinresearch-tui"
}

// findAsset returns the download URL of the named asset, or "" if the release
// doesn't carry it.
func (r releaseInfo) findAsset(name string) string {
	for _, a := range r.Assets {
		if a.Name == name {
			return a.URL
		}
	}
	return ""
}

// trimStampSection returns just the "what changed" portion of a release body,
// dropping the stamp action's trailing "Blockchain Timestamps" section and the
// horizontal rule that leads into it. If the marker is absent (older releases,
// or a future wording change) the whole body is returned unchanged so the user
// still sees *something*.
func trimStampSection(body string) string {
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

// compareSemver returns -1, 0, or 1 as a is older than, equal to, or newer than
// b, comparing the numeric major.minor.patch cores. Missing components count as
// 0 ("1.2" == "1.2.0"); non-numeric junk in a field also counts as 0 so a
// malformed version can never panic.
func compareSemver(a, b string) int {
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

// isNewer reports whether the release tag is strictly newer than the current
// build version. A "dev" (or empty) current version always returns false so
// local/dev builds don't nag with an update badge — the modal still lets the
// user pull the latest release explicitly.
func isNewer(tag, current string) bool {
	if current == "dev" || current == "" {
		return false
	}
	return compareSemver(tag, current) > 0
}

// missedReleases returns the releases whose notes should be shown before
// updating. Dev builds have no comparable installed version, so they show only
// the latest release notes while still allowing a manual update.
func missedReleases(current string, latest releaseInfo, releases []releaseInfo) []releaseInfo {
	if current == "dev" || current == "" {
		if latest.TagName == "" {
			return nil
		}
		return []releaseInfo{latest}
	}

	out := make([]releaseInfo, 0, len(releases)+1)
	seen := make(map[string]bool, len(releases)+1)
	for _, rel := range releases {
		if rel.TagName == "" || !isNewer(rel.TagName, current) {
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
	if latest.TagName != "" && isNewer(latest.TagName, current) && !seen[latestKey] {
		out = append([]releaseInfo{latest}, out...)
	}
	return out
}
