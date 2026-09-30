package selfupdate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// cacheTTL is how long a check result is trusted before checking again — keeps
// the nudge from hitting GitHub on every invocation while staying responsive
// to a release (15 minutes, so a fresh release is noticed within the quarter
// hour rather than a day).
const cacheTTL = 15 * time.Minute

// File permissions for the cache directory and file.
const (
	dirPerm  = 0o750
	filePerm = 0o600
)

// cache is the persisted last-check result.
type cache struct {
	CheckedAt time.Time `json:"checked_at"`
	Latest    string    `json:"latest"`
}

// cachePath returns ~/.cache/smart-test-runner/update-check.json.
func cachePath() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(base, "smart-test-runner", "update-check.json"), nil
}

// ReadCache returns a cached check if it is still fresh. The bool reports
// whether a fresh entry was found.
func ReadCache(now time.Time) (latest string, fresh bool) {
	path, err := cachePath()
	if err != nil {
		return "", false
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}

	var c cache
	if err := json.Unmarshal(data, &c); err != nil {
		return "", false
	}

	if now.Sub(c.CheckedAt) > cacheTTL {
		return "", false
	}

	return c.Latest, true
}

// WriteCache stores a check result for the cache TTL.
func WriteCache(latest string, now time.Time) {
	path, err := cachePath()
	if err != nil {
		return
	}

	if err := os.MkdirAll(filepath.Dir(path), dirPerm); err != nil {
		return
	}

	data, merr := json.Marshal(cache{CheckedAt: now, Latest: latest})
	if merr != nil {
		return
	}

	if werr := os.WriteFile(path, data, filePerm); werr != nil {
		return
	}
}
