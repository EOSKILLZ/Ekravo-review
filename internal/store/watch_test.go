package store

import (
	"context"
	"testing"
)

func TestWatchDefaultsToAllAndPersists(t *testing.T) {
	s := open(t)
	ctx := context.Background()

	watch, err := s.Watch(ctx)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	if !watch.All || watch.Repos == nil || len(watch.Repos) != 0 {
		t.Fatalf("default watch = %+v", watch)
	}

	if err := s.SetWatch(ctx, Watch{All: false, Repos: []string{" o/r ", "O/R", "o/other", "  "}}); err != nil {
		t.Fatalf("SetWatch: %v", err)
	}
	watch, err = s.Watch(ctx)
	if err != nil {
		t.Fatalf("Watch after save: %v", err)
	}
	if watch.All || len(watch.Repos) != 2 || watch.Repos[0] != "o/r" || watch.Repos[1] != "o/other" {
		t.Fatalf("saved watch = %+v", watch)
	}

	if err := s.SetWatch(ctx, Watch{All: true}); err != nil {
		t.Fatalf("SetWatch all: %v", err)
	}
	watch, err = s.Watch(ctx)
	if err != nil {
		t.Fatalf("Watch after reset: %v", err)
	}
	if !watch.All || watch.Repos == nil || len(watch.Repos) != 0 {
		t.Fatalf("watch should be all with an empty list: %+v", watch)
	}
}
