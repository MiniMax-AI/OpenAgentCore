package managedskills

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// pluginInstallTimeout caps a single plugin's download + extract step.
const pluginInstallTimeout = 60 * time.Second

// maxPluginZipBytes mirrors the server-side cap in
// server/internal/capability/parser/plugin_validator.go. Defense in
// depth.
const maxPluginZipBytes int64 = 32 * 1024 * 1024

// pluginsHTTPClient timeout is larger than pluginInstallTimeout so the
// per-call context cancel dominates.
var pluginsHTTPClient = &http.Client{
	Timeout: pluginInstallTimeout + 10*time.Second,
}

// fetchPluginZip GETs url into dst, capping the body at
// maxPluginZipBytes. Returns an OPEN file descriptor positioned at
// offset 0; the caller closes it. Holding the FD across verify +
// extract closes the TOCTOU between hashing the on-disk bytes and
// reading them for extract — even if someone swaps the file, the open
// FD points at the original inode.
//
// Only http/https are accepted to defend against a future
// canonical_spec letting attacker-supplied download_url reach this
// code with file:// or http://internal-ip/... values.
func fetchPluginZip(ctx context.Context, downloadURL, dst string) (*os.File, error) {
	parsed, err := url.Parse(downloadURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		// Don't include downloadURL — it carries the signature query
		// string.
		return nil, errors.New("download_url must be http(s)")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return nil, errors.New("build request failed")
	}
	resp, err := pluginsHTTPClient.Do(req)
	if err != nil {
		// Strip embedded URL via sanitizeHTTPClientError —
		// OSSAccessKeyId + Signature would otherwise leak into the
		// daemon log.
		return nil, fmt.Errorf("get failed: %s", sanitizeHTTPClientError(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4*1024))
		return nil, fmt.Errorf("get: status %d", resp.StatusCode)
	}

	// O_EXCL — the per-call uuid in the path makes a collision a
	// programmer error, not an attacker condition. Failing fast is
	// safer than silent truncation.
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open dst: %w", err)
	}

	limited := io.LimitReader(resp.Body, maxPluginZipBytes+1)
	written, err := io.Copy(f, limited)
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("copy body: %w", err)
	}
	if written > maxPluginZipBytes {
		_ = f.Close()
		return nil, fmt.Errorf("zip exceeds %d byte cap", maxPluginZipBytes)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("seek after write: %w", err)
	}
	return f, nil
}

// sanitizeHTTPClientError strips the URL embedded by *url.Error.
// net/http returns errors that include the full request URL — for
// presigned OSS URLs that's OSSAccessKeyId + Signature + Expires.
// Without redaction those credentials land in the daemon log via
// PluginInstallResult.Warnings → session.go logger.Warn.
//
// Format is `<method> "<url>": <inner>` — keep the method + inner
// message, drop the URL.
func sanitizeHTTPClientError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	open := strings.Index(msg, `"`)
	if open < 0 {
		return msg
	}
	close := strings.Index(msg[open+1:], `"`)
	if close < 0 {
		return msg
	}
	closeAbs := open + 1 + close
	if closeAbs+2 > len(msg) {
		return msg
	}
	return msg[:open] + "<redacted-url>" + msg[closeAbs+1:]
}

// verifyPluginSHA256FromFD hashes the bytes the open FD points at and
// compares against want (lowercase hex). Rewinds the FD afterwards.
func verifyPluginSHA256FromFD(fd *os.File, want string) error {
	want = strings.ToLower(strings.TrimSpace(want))
	if want == "" {
		return errors.New("verify: empty expected sha256")
	}
	if _, err := fd.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("verify: seek: %w", err)
	}
	h := sha256.New()
	if _, err := io.Copy(h, fd); err != nil {
		return fmt.Errorf("verify: hash: %w", err)
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != want {
		return fmt.Errorf("verify: sha256 mismatch (want=%s got=%s)", want, got)
	}
	return nil
}

// extractPluginZipFromFD reads via io.NewSectionReader rather than
// re-opening the path so the byte stream stays identical to the
// verified one (TOCTOU defense).
func extractPluginZipFromFD(fd *os.File, size int64, dst string) error {
	zr, err := zip.NewReader(io.NewSectionReader(fd, 0, size), size)
	if err != nil {
		return fmt.Errorf("extract: open zip: %w", err)
	}

	root := detectSingleZipRoot(zr.File)
	absDst, err := filepath.Abs(dst)
	if err != nil {
		return fmt.Errorf("extract: abs dst: %w", err)
	}

	for _, f := range zr.File {
		name := normaliseZipPath(f.Name)
		if name == "" || strings.HasPrefix(name, "__MACOSX/") || name == "__MACOSX" {
			continue
		}
		// Skip non-regular zip entries (symlinks, devices, named
		// pipes). Symlink entries flagged with Unix lrwxrwxrwx mode
		// bits would otherwise be written as plain files containing
		// the link target string — an exfil vector.
		mode := f.Mode()
		if !f.FileInfo().IsDir() && !mode.IsRegular() {
			continue
		}
		if root != "" {
			if !strings.HasPrefix(name, root) {
				continue
			}
			name = strings.TrimPrefix(name, root)
			if name == "" {
				continue
			}
		}

		target := filepath.Join(absDst, name)
		rel, err := filepath.Rel(absDst, target)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("extract: entry %q escapes target", f.Name)
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("extract: mkdir %s: %w", target, err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("extract: mkdir parent of %s: %w", target, err)
		}
		if err := writeZipEntry(f, target); err != nil {
			return err
		}
	}
	return nil
}

// writeZipEntry streams one zip entry into target preserving the
// entry's mode bits (executables stay executable — hook scripts need
// this). 0644 default when no mode is set.
func writeZipEntry(f *zip.File, target string) error {
	rc, err := f.Open()
	if err != nil {
		return fmt.Errorf("extract: open entry %s: %w", f.Name, err)
	}
	defer rc.Close()

	mode := f.Mode().Perm()
	if mode == 0 {
		mode = 0o644
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return fmt.Errorf("extract: open target %s: %w", target, err)
	}
	defer out.Close()
	if _, err := io.Copy(out, rc); err != nil {
		return fmt.Errorf("extract: copy %s: %w", target, err)
	}
	return nil
}

// detectSingleZipRoot returns the common root directory (with trailing
// slash) shared by every non-MACOSX entry, or "" when there is none.
// Hidden directories (".*") are NOT treated as wrappers because
// `.claude-plugin/` is a legitimate plugin component.
//
// `normaliseZipPath` strips trailing slashes, so a bare directory
// entry like `my-plugin/` arrives as `my-plugin` —
// indistinguishable from a top-level file. Skipping entries without an
// internal "/" lets us pick a real file path and infer the wrapper.
// Without this, `zip -r foo foo/` would short-circuit on the leading
// `foo/` directory entry and leave the manifest nested.
// (Mirrors server-side plugin_validator.detectSingleRoot — the two
// must agree.)
func detectSingleZipRoot(files []*zip.File) string {
	var first string
	for _, f := range files {
		name := normaliseZipPath(f.Name)
		if name == "" || strings.HasPrefix(name, "__MACOSX/") || name == "__MACOSX" {
			continue
		}
		if !strings.Contains(name, "/") {
			continue
		}
		first = name
		break
	}
	if first == "" {
		return ""
	}
	idx := strings.Index(first, "/")
	if idx <= 0 {
		return ""
	}
	root := first[:idx+1]
	if strings.HasPrefix(root, ".") {
		return ""
	}
	for _, f := range files {
		name := normaliseZipPath(f.Name)
		if name == "" || strings.HasPrefix(name, "__MACOSX/") || name == "__MACOSX" {
			continue
		}
		// Bare directory entries (e.g. `my-plugin`) arrive without a
		// trailing slash. If the entry equals the root with the slash
		// trimmed, it's the wrapping dir itself.
		if name+"/" == root {
			continue
		}
		if !strings.HasPrefix(name, root) {
			return ""
		}
	}
	return root
}

// normaliseZipPath converts back-slashes to forward slashes (some
// Windows zip writers emit `\`) and strips trailing slashes that
// directory entries may carry.
func normaliseZipPath(name string) string {
	p := strings.ReplaceAll(name, "\\", "/")
	return strings.TrimSuffix(p, "/")
}

func stringField(m map[string]any, key string) string { s, _ := m[key].(string); return s }
