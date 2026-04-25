package fsys

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/pidutil"
)

// WriteFileAtomic writes data to path atomically using a temp file + rename.
// The temp file is created in the same directory as path to ensure the rename
// is on the same filesystem (required for atomic rename on POSIX). Permissions
// are enforced on the temp file before the rename so the final path is never
// visible with a wider mode (no write-then-chmod window).
func WriteFileAtomic(fs FS, path string, data []byte, perm os.FileMode) error {
	suffix := strconv.Itoa(os.Getpid()) + "." + strconv.FormatInt(time.Now().UnixNano(), 36)
	tmp := path + ".tmp." + suffix
	if err := fs.WriteFile(tmp, data, perm); err != nil {
		return fmt.Errorf("writing temp file: %w", err)
	}
	// Chmod before rename so the final path never exists with a wider mode
	// even briefly. umask can relax `perm` on the initial WriteFile; an
	// explicit Chmod normalises it.
	if err := fs.Chmod(tmp, perm); err != nil {
		_ = fs.Remove(tmp)
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := fs.Rename(tmp, path); err != nil {
		_ = fs.Remove(tmp)
		return fmt.Errorf("renaming temp file: %w", err)
	}
	sweepDeadAtomicOrphans(fs, path)
	return nil
}

// sweepDeadAtomicOrphans removes sibling temp files left behind by previous
// WriteFileAtomic callers that died (e.g., SIGTERM) between WriteFile and
// Rename. It is best-effort: any error during enumeration or removal is
// ignored so a stale-temp cleanup never fails an otherwise successful write.
//
// Only siblings of `target` matching the WriteFileAtomic suffix scheme
// (`<basename>.tmp.<pid>.<unixnano-base36>`) are considered. PIDs that are
// still alive — including in-progress writers from concurrent calls — are
// preserved.
func sweepDeadAtomicOrphans(fs FS, target string) {
	dir := filepath.Dir(target)
	prefix := filepath.Base(target) + ".tmp."
	entries, err := fs.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		pid, ok := parseAtomicTempPID(name[len(prefix):])
		if !ok {
			continue
		}
		if pidutil.Alive(pid) {
			continue
		}
		_ = fs.Remove(filepath.Join(dir, name))
	}
}

// parseAtomicTempPID parses the `<pid>.<unixnano-base36>` suffix produced by
// WriteFileAtomic and returns the PID. Returns ok=false when the input does
// not match the scheme (e.g., no dot, non-numeric PID).
func parseAtomicTempPID(suffix string) (int, bool) {
	dot := strings.IndexByte(suffix, '.')
	if dot <= 0 || dot == len(suffix)-1 {
		return 0, false
	}
	pid, err := strconv.Atoi(suffix[:dot])
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}

// WriteFileIfChangedAtomic writes data to path atomically only when the
// existing on-disk bytes differ. Returns nil with no write when the
// content already matches. A read error other than "not exist" is
// ignored and the write proceeds — this is a best-effort optimization to
// avoid churning mtime (and fsnotify watchers) on no-op writes, not a
// safety check.
func WriteFileIfChangedAtomic(fs FS, path string, data []byte, perm os.FileMode) error {
	if existing, err := fs.ReadFile(path); err == nil && bytes.Equal(existing, data) {
		return nil
	}
	return WriteFileAtomic(fs, path, data, perm)
}
