package selfupdate

import (
	"testing"
	"time"
)

func TestCacheRoundTrip(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	WriteCache("v1.2.3", now)

	latest, fresh := ReadCache(now.Add(cacheTTL / 2))
	if !fresh || latest != "v1.2.3" {
		t.Fatalf("ReadCache = %q/%v want v1.2.3/true", latest, fresh)
	}
}

// TestCacheTTLIsFifteenMinutes pins the window: short enough that a fresh
// release is noticed promptly, long enough that ordinary runs do not hit
// GitHub every time.
func TestCacheTTLIsFifteenMinutes(t *testing.T) {
	if cacheTTL != 15*time.Minute {
		t.Fatalf("cacheTTL = %v want 15m", cacheTTL)
	}
}

func TestCacheExpires(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	WriteCache("v1.2.3", now)

	if _, fresh := ReadCache(now.Add(cacheTTL + time.Minute)); fresh {
		t.Fatal("cache should expire after the TTL")
	}
}

func TestCacheMissing(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	if _, fresh := ReadCache(time.Now()); fresh {
		t.Fatal("empty cache reported as fresh")
	}
}
