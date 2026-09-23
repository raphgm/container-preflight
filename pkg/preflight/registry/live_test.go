package registry

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

// Hits real registries; run with PREFLIGHT_LIVE=1.
func TestLiveResolve(t *testing.T) {
	if os.Getenv("PREFLIGHT_LIVE") == "" {
		t.Skip("set PREFLIGHT_LIVE=1 to query real registries")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	r := NewRemote()

	img, err := r.Resolve(ctx, "postgres:16", "linux/arm64")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("postgres:16 index=%v platforms=%v size=%d", img.Index, img.Platforms, img.CompressedSize)
	if !img.Index || img.CompressedSize == 0 {
		t.Error("expected multi-arch index with arm64 size")
	}

	img, err = r.Resolve(ctx, "mcr.microsoft.com/mssql/server:2022-latest", "linux/arm64")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("mssql index=%v platforms=%v size=%d", img.Index, img.Platforms, img.CompressedSize)

	_, err = r.Resolve(ctx, "postgres:this-tag-does-not-exist-123", "linux/arm64")
	t.Logf("missing tag: %v", err)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
	_, err = r.Resolve(ctx, "raphgm/definitely-not-a-repo-xyz:1", "linux/arm64")
	t.Logf("missing repo: %v", err)
}

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"linux/arm64/v8": "linux/arm64",
		"Linux/aarch64":  "linux/arm64",
		"linux/x86_64":   "linux/amd64",
		"linux/arm/v7":   "linux/arm/v7",
		"linux/amd64/":   "linux/amd64",
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q; want %q", in, got, want)
		}
	}
	if Compatible("linux/amd64", "linux/arm64") {
		t.Error("amd64 must not satisfy arm64")
	}
	if !Compatible("linux/arm/v7", "linux/arm") {
		t.Error("variant-less want should accept v7")
	}
}
