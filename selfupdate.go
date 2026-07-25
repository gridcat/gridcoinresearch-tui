// This file is the side-effecting half of the self-update feature: download the
// release archive, verify it against the release's checksums.txt, pull the
// binary out, and atomically swap it over the running executable. The re-exec
// that follows a successful swap lives in restart_unix.go / restart_windows.go
// (it needs the terminal restored first, so main.go drives it after the TUI
// exits).
package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// maxDownloadBytes caps any single asset download so a hostile or corrupt
// release can't exhaust memory. Release archives are a few MB; 64 MiB is a
// generous ceiling (for now). The release workflow re-checks every built
// binary against this number, so if a release ever outgrows it CI fails and
// says so — see .github/workflows/release.yml.
const maxDownloadBytes = 64 << 20

// applyUpdate downloads the release archive for this OS/arch, verifies its
// sha256 against the release's checksums.txt, extracts the binary, and
// atomically swaps it over the running executable. On success it returns the
// on-disk path of the now-updated executable, which the caller re-execs. The
// running process is NOT restarted here.
func applyUpdate(rel releaseInfo) (string, error) {
	archive := assetName()
	archiveURL := rel.findAsset(archive)
	if archiveURL == "" {
		return "", fmt.Errorf("release %s has no asset %q for %s/%s", rel.TagName, archive, runtime.GOOS, runtime.GOARCH)
	}
	sumsURL := rel.findAsset("checksums.txt")
	if sumsURL == "" {
		return "", fmt.Errorf("release %s has no checksums.txt to verify against", rel.TagName)
	}

	// Resolve the real on-disk path of the running binary, following symlinks so
	// we replace the actual file and not a symlink pointing at it.
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate own binary: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}

	// Fetch the manifest first so we know the expected hash before spending
	// bandwidth on the (larger) archive.
	sums, err := downloadBytes(sumsURL)
	if err != nil {
		return "", fmt.Errorf("download checksums: %w", err)
	}
	want, err := checksumFor(string(sums), archive)
	if err != nil {
		return "", err
	}
	data, err := downloadBytes(archiveURL)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", archive, err)
	}
	got := sha256.Sum256(data)
	if hex.EncodeToString(got[:]) != want {
		return "", fmt.Errorf("checksum mismatch for %s (download corrupt or tampered)", archive)
	}

	bin, err := extractBinary(data, archive)
	if err != nil {
		return "", err
	}
	if err := swapBinary(exe, bin); err != nil {
		return "", err
	}
	return exe, nil
}

// downloadBytes GETs a URL into memory, capped at maxDownloadBytes.
func downloadBytes(url string) ([]byte, error) {
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
		return nil, fmt.Errorf("http %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxDownloadBytes))
}

// checksumFor finds the sha256 hex for filename in a checksums.txt body. Each
// line is "<hex>  <filename>" (coreutils sha256sum style, two spaces).
func checksumFor(manifest, filename string) (string, error) {
	for _, line := range strings.Split(manifest, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) != 2 {
			continue
		}
		if fields[1] == filename {
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", fmt.Errorf("checksums.txt has no entry for %s", filename)
}

// extractBinary pulls the executable out of a verified archive, dispatching on
// the archive extension (zip on windows, tar.gz elsewhere).
func extractBinary(archive []byte, archiveName string) ([]byte, error) {
	want := binaryName()
	if strings.HasSuffix(archiveName, ".zip") {
		return extractFromZip(archive, want)
	}
	return extractFromTarGz(archive, want)
}

func extractFromTarGz(archive []byte, want string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("open gzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read tar: %w", err)
		}
		if filepath.Base(hdr.Name) == want {
			return io.ReadAll(io.LimitReader(tr, maxDownloadBytes))
		}
	}
	return nil, fmt.Errorf("archive did not contain %s", want)
}

func extractFromZip(archive []byte, want string) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, fmt.Errorf("open zip: %w", err)
	}
	for _, f := range zr.File {
		if filepath.Base(f.Name) == want {
			rc, err := f.Open()
			if err != nil {
				return nil, fmt.Errorf("open %s in zip: %w", want, err)
			}
			defer rc.Close()
			return io.ReadAll(io.LimitReader(rc, maxDownloadBytes))
		}
	}
	return nil, fmt.Errorf("archive did not contain %s", want)
}

// swapBinary writes newBin next to the running executable and atomically moves
// it into place, preserving the old binary's file mode. Writing into the target
// directory (rather than the system temp dir) keeps the final move on one
// filesystem so it's a true atomic rename with no cross-device copy.
func swapBinary(exe string, newBin []byte) error {
	dir := filepath.Dir(exe)

	// Preserve the current binary's permission bits; fall back to 0755.
	mode := os.FileMode(0o755)
	if fi, err := os.Stat(exe); err == nil {
		mode = fi.Mode().Perm()
	}

	tmp, err := os.CreateTemp(dir, ".grctui-update-*")
	if err != nil {
		return fmt.Errorf("stage new binary in %s: %w", dir, wrapPerm(err))
	}
	tmpName := tmp.Name()

	// Remove the staging file on any early failure; the successful path renames
	// it away so this becomes a no-op.
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(newBin); err != nil {
		tmp.Close()
		return fmt.Errorf("write new binary: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("flush new binary: %w", err)
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return fmt.Errorf("chmod new binary: %w", err)
	}
	if err := replaceExecutable(exe, tmpName); err != nil {
		return err
	}
	committed = true
	return nil
}

// wrapPerm dresses up a permission error with a hint about how to fix it. The
// binary often lives in a root-owned dir (/usr/local/bin), so a non-privileged
// run legitimately can't replace it — say so instead of a bare "permission
// denied".
func wrapPerm(err error) error {
	if errors.Is(err, os.ErrPermission) {
		return fmt.Errorf("%w — re-run with the privileges that own the binary (e.g. sudo)", err)
	}
	return err
}
