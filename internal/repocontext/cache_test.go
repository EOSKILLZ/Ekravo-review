package repocontext

import (
	"context"
	"testing"
)

func TestCacheHitsByContentHash(t *testing.T) {
	c, err := OpenCache(t.TempDir(), 100)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	src := []byte("package demo\nfunc A() {}\n")
	hash := Hash(src)
	fs := &FileSymbols{Path: "a.go", Lang: "go", Defs: []Symbol{{Name: "A", Kind: "function", Line: 2}}}
	if err := c.Put(context.Background(), "o/r", "a.go", hash, fs); err != nil {
		t.Fatal(err)
	}

	got, ok := c.Get(context.Background(), "o/r", "a.go", hash)
	if !ok || len(got.Defs) != 1 || got.Defs[0].Name != "A" {
		t.Fatalf("cache get = %+v, %v", got, ok)
	}
	if _, ok := c.Get(context.Background(), "o/r", "a.go", Hash([]byte("different"))); ok {
		t.Fatal("cache should miss on a different content hash")
	}
	if _, ok := c.Get(context.Background(), "other/r", "a.go", hash); ok {
		t.Fatal("cache must be scoped per repository")
	}
}

func TestHashIsStable(t *testing.T) {
	a := Hash([]byte("x"))
	if a != Hash([]byte("x")) || len(a) != 64 {
		t.Fatalf("hash = %q", a)
	}
}
