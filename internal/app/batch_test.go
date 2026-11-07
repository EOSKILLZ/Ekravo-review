package app

import (
	"testing"

	"github.com/ekravo/Ekravo-review/v2/internal/diff"
)

func TestBatchChunksPacksByBudget(t *testing.T) {
	chunks := []diff.Chunk{
		{Chars: 40},
		{Chars: 40},
		{Chars: 40},
		{Chars: 40},
		{Chars: 40},
	}
	batches := batchChunks(chunks, 100)
	if len(batches) != 3 {
		t.Fatalf("expected 3 batches, got %d", len(batches))
	}
	if len(batches[0]) != 2 || len(batches[1]) != 2 || len(batches[2]) != 1 {
		t.Fatalf("unexpected packing: %d %d %d", len(batches[0]), len(batches[1]), len(batches[2]))
	}
}

func TestBatchChunksKeepsOversizedChunkAlone(t *testing.T) {
	chunks := []diff.Chunk{{Chars: 500}, {Chars: 10}, {Chars: 10}}
	batches := batchChunks(chunks, 100)
	if len(batches) != 2 {
		t.Fatalf("expected 2 batches, got %d", len(batches))
	}
	if len(batches[0]) != 1 || batches[0][0].Chars != 500 {
		t.Fatalf("oversized chunk was split or merged: %+v", batches[0])
	}
}
