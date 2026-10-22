package cli

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/MiniMax-AI/OpenAgentCore/apps/daemon/internal/paths"
	"github.com/MiniMax-AI/OpenAgentCore/internal/runtimefs"
)

const nativeAttempts = 5

var (
	nativeStall        = 60 * time.Second
	nativeRetryDelay   = time.Second
	nativeDigest       = regexp.MustCompile(`^[0-9a-f]{64}$`)
	errNativeRedirect  = errors.New("install: a download redirect must use HTTPS without credentials")
	errNativeStall     = errors.New("install: download stalled (no data for 60 seconds)")
	errNativeNoArchive = errors.New("install: this Core has no qualified archive for this component and platform")
)

// nativeClient follows redirects only to HTTPS, as the release host requires.
var nativeClient = &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
	if len(via) > 5 || req.URL.Scheme != "https" || req.URL.User != nil {
		return errNativeRedirect
	}
	return nil
}}

// nativeInstallerBase is where Core serves this release's installer archives.
func nativeInstallerBase(onboardURL string) string {
	return strings.TrimSuffix(onboardURL, "installation") + "install/" + Version
}

// nativeDownloadDirectory holds only transient files: the installer's bundle.* directories and the
// partial archive that a retry or a later run resumes.
func nativeDownloadDirectory() (string, error) {
	home, err := paths.Root()
	if err != nil {
		return "", err
	}
	cache := filepath.Join(home, "native-download")
	if err = runtimefs.EnsurePrivateDir(cache); err != nil {
		return "", err
	}
	pruneNativeDownloads(cache)
	dir := filepath.Join(cache, Version)
	return dir, os.MkdirAll(dir, 0700)
}

// downloadNativeComponent fetches the archive of one component, verifies it against Core's
// checksum and extracts it into stage. It returns the archive, which the caller deletes once the
// component is published.
func downloadNativeComponent(ctx context.Context, out io.Writer, onboardURL, name, stage string, expected nativeComponent) (string, error) {
	dir, err := nativeDownloadDirectory()
	if err != nil {
		return "", err
	}
	file := runtime.GOOS + "-" + runtime.GOARCH + "-" + name
	archive, err := fetchNativeArchive(ctx, out, nativeInstallerBase(onboardURL)+"/"+file, dir, file)
	if err != nil {
		return "", err
	}
	return archive, extractNativeComponent(ctx, out, archive, stage, name, expected)
}

// pruneNativeDownloads keeps the current version and recent distribution directories of running
// installers. Failing to remove an old entry is harmless.
func pruneNativeDownloads(cache string) {
	entries, _ := os.ReadDir(cache)
	for _, entry := range entries {
		info, err := entry.Info()
		recent := err == nil && strings.HasPrefix(entry.Name(), "bundle.") && time.Since(info.ModTime()) < 24*time.Hour
		if entry.Name() != Version && !recent {
			_ = os.RemoveAll(filepath.Join(cache, entry.Name()))
		}
	}
}

// fetchNativeArchive returns the verified archive, downloading or resuming it as needed.
func fetchNativeArchive(ctx context.Context, out io.Writer, url, dir, file string) (string, error) {
	want, err := fetchNativeChecksum(ctx, out, url+".sha256")
	if err != nil {
		return "", err
	}
	part := filepath.Join(dir, file+".tar.gz.part")
	for pass := 0; ; pass++ {
		resumed := false
		if info, err := os.Stat(part); err == nil && info.Size() > 0 {
			resumed = true
			fmt.Fprintf(out, "Resuming the earlier %s download...\n", file)
		}
		if err = retryNativeDownload(ctx, out, func() (bool, error) { return downloadNativeFile(ctx, out, url+".tar.gz", part, file) }); err != nil {
			return "", err
		}
		got, err := nativeFileDigest(ctx, part)
		if err != nil {
			return "", err
		}
		if got == want {
			return part, nil
		}
		_ = os.Remove(part)
		// A resumed file may have been damaged earlier; one clean download decides.
		if !resumed || pass > 0 {
			return "", errors.New("install: archive checksum mismatch; the download was discarded, rerun the command to download it again")
		}
	}
}

func fetchNativeChecksum(ctx context.Context, out io.Writer, url string) (string, error) {
	var digest string
	err := retryNativeDownload(ctx, out, func() (bool, error) {
		ctx, cancel := context.WithTimeoutCause(ctx, nativeStall, errNativeStall)
		defer cancel()
		response, retry, err := getNative(ctx, url, 0)
		if err != nil {
			return retry, stalled(ctx, err)
		}
		defer response.Body.Close()
		raw, err := io.ReadAll(io.LimitReader(response.Body, 1025))
		digest = strings.TrimSpace(string(raw))
		if err != nil {
			return true, stalled(ctx, err)
		}
		if !nativeDigest.MatchString(digest) {
			return false, errors.New("install: Core returned an invalid archive checksum")
		}
		return false, nil
	})
	return digest, err
}

// getNative returns a 200 or 206 response, or whether its failure is worth another attempt.
func getNative(ctx context.Context, url string, offset int64) (*http.Response, bool, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, false, errors.New("install: invalid Core installer address")
	}
	if offset > 0 {
		request.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	response, err := nativeClient.Do(request)
	if err != nil {
		return nil, !errors.Is(err, errNativeRedirect), err
	}
	code := response.StatusCode
	switch {
	case code == http.StatusOK || code == http.StatusPartialContent || code == http.StatusRequestedRangeNotSatisfiable:
		return response, false, nil
	case code == http.StatusNotFound:
		err = errNativeNoArchive
	default:
		err = fmt.Errorf("install: download failed (HTTP %d); check Core and the download host", code)
	}
	response.Body.Close()
	return nil, code == 408 || code == 429 || code >= 500, err
}

func retryNativeDownload(ctx context.Context, out io.Writer, attempt func() (bool, error)) error {
	for n := 1; ; n++ {
		retry, err := attempt()
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !retry || nativeDiskFull(err) {
			return err
		}
		if n == nativeAttempts {
			return fmt.Errorf("%w (downloaded data is kept; check the network to Core and GitHub, set HTTPS_PROXY if a proxy is required, and rerun the command)", err)
		}
		fmt.Fprintf(out, "Download interrupted; retrying (%d/%d)...\n", n, nativeAttempts-1)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(n) * nativeRetryDelay):
		}
	}
}

// downloadNativeFile appends to dest from where an earlier attempt stopped and fails when no data
// arrives for nativeStall. There is no total time limit.
func downloadNativeFile(ctx context.Context, out io.Writer, url, dest, label string) (bool, error) {
	var offset int64
	if info, err := os.Stat(dest); err == nil {
		offset = info.Size()
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	stall := time.AfterFunc(nativeStall, func() { cancel(errNativeStall) })
	defer stall.Stop()
	response, retry, err := getNative(ctx, url, offset)
	if err != nil {
		return retry, stalled(ctx, err)
	}
	defer response.Body.Close()
	flags := os.O_WRONLY | os.O_CREATE | os.O_APPEND
	switch response.StatusCode {
	case http.StatusRequestedRangeNotSatisfiable:
		return false, nil // The partial file already holds everything; the checksum decides.
	case http.StatusOK:
		flags, offset = os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0
	}
	if response.ContentLength > 0 {
		if err = requireNativeSpace(filepath.Dir(dest), uint64(response.ContentLength)); err != nil {
			return false, err
		}
	}
	file, err := os.OpenFile(dest, flags, 0600)
	if err != nil {
		return false, err
	}
	progress := &nativeProgress{out: out, label: label, done: offset}
	if response.ContentLength > 0 {
		progress.total = offset + response.ContentLength
	}
	_, err = io.Copy(file, &nativeBody{Reader: response.Body, stall: stall, progress: progress})
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	return err != nil, stalled(ctx, err)
}

// stalled reports a stall instead of the cancellation it caused.
func stalled(ctx context.Context, err error) error {
	if err != nil && errors.Is(context.Cause(ctx), errNativeStall) {
		return errNativeStall
	}
	return err
}

type nativeBody struct {
	io.Reader
	stall    *time.Timer
	progress *nativeProgress
}

func (b *nativeBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	if n > 0 {
		b.stall.Reset(nativeStall)
		b.progress.add(int64(n))
	}
	return n, err
}

// nativeProgress prints one short line for each tenth of a download.
type nativeProgress struct {
	out         io.Writer
	label       string
	done, total int64
	step        int64
}

func (p *nativeProgress) add(n int64) {
	p.done += n
	if p.total == 0 {
		return
	}
	if step := p.done * 10 / p.total; step > p.step {
		p.step = step
		fmt.Fprintf(p.out, "Downloading %s: %d%% (%d of %d MiB)\n", p.label, step*10, p.done>>20, p.total>>20)
	}
}

func nativeFileDigest(ctx context.Context, path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err = nativeCopy(ctx, hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// extractNativeComponent unpacks components/<name>/ from a verified archive into stage. Only regular
// files and directories under that path are accepted, and only files that bundle.json lists; every
// file is then checked against its digest.
func extractNativeComponent(ctx context.Context, out io.Writer, archive, stage, name string, expected nativeComponent) error {
	var size uint64
	if err := eachNativeEntry(ctx, archive, name, func(h *tar.Header, _ string, _ io.Reader) error {
		size += uint64(h.Size)
		return nil
	}); err != nil {
		return err
	}
	if err := requireNativeSpace(filepath.Dir(stage), size); err != nil {
		return err
	}
	fmt.Fprintf(out, "Extracting %s...\n", name)
	if err := eachNativeEntry(ctx, archive, name, func(h *tar.Header, rel string, r io.Reader) error {
		target := filepath.Join(stage, filepath.FromSlash(rel))
		if h.Typeflag == tar.TypeDir {
			return os.MkdirAll(target, 0700)
		}
		listed, ok := expected.Files[rel]
		if !ok {
			return errors.New("install: component archive holds a file that bundle.json does not list")
		}
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		mode := os.FileMode(0600)
		if listed.Executable {
			mode = 0700
		}
		file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err != nil {
			return err
		}
		_, err = nativeCopy(ctx, file, r)
		if closeErr := file.Close(); err == nil {
			err = closeErr
		}
		return err
	}); err != nil {
		return err
	}
	if err := checkComponentFiles(ctx, stage, expected); err != nil {
		if errors.Is(err, errNativeComponentMismatch) || errors.Is(err, os.ErrNotExist) {
			return errors.New("install: component archive does not match bundle.json; Core serves inconsistent installer files")
		}
		return err
	}
	return nil
}

// eachNativeEntry visits the files and directories of a component archive; the root directory itself is skipped.
func eachNativeEntry(ctx context.Context, archive, name string, visit func(*tar.Header, string, io.Reader) error) error {
	file, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer file.Close()
	zipped, err := gzip.NewReader(nativeCopyReader{ctx: ctx, Reader: file})
	if err != nil {
		return errors.New("install: component archive is invalid; remove it and retry")
	}
	reader := tar.NewReader(zipped)
	prefix := "components/" + name + "/"
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("install: component archive is invalid or incomplete: %w", err)
		}
		if header.Typeflag != tar.TypeDir && header.Typeflag != tar.TypeReg {
			return errors.New("install: component archive may contain only files and directories")
		}
		path := header.Name
		if header.Typeflag == tar.TypeDir {
			path = strings.TrimSuffix(path, "/") + "/"
		}
		rel, ok := strings.CutPrefix(path, prefix)
		rel = strings.TrimSuffix(rel, "/")
		if !ok || (rel == "" && header.Typeflag != tar.TypeDir) || (rel != "" && !validBundlePath(rel)) {
			return errors.New("install: component archive contains an unexpected path")
		}
		if rel == "" {
			continue
		}
		if err = visit(header, rel, reader); err != nil {
			return err
		}
	}
}
