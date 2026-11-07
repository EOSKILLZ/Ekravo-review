package repocontext

import (
	"hash/fnv"
	"strings"
)

// SimHash produces a 64-bit locality-sensitive hash. Similar function bodies
// land within a small Hamming distance, which is enough to surface an existing
// implementation without an embedding service.
func SimHash(text string) uint64 {
	var v [64]int
	for _, tok := range tokenize(text) {
		h := fnv.New64a()
		_, _ = h.Write([]byte(tok))
		sum := h.Sum64()
		for i := 0; i < 64; i++ {
			if sum&(1<<uint(i)) != 0 {
				v[i]++
			} else {
				v[i]--
			}
		}
	}
	var out uint64
	for i := 0; i < 64; i++ {
		if v[i] > 0 {
			out |= 1 << uint(i)
		}
	}
	return out
}

// Hamming returns the number of differing bits.
func Hamming(a, b uint64) int {
	x := a ^ b
	n := 0
	for x != 0 {
		x &= x - 1
		n++
	}
	return n
}

func tokenize(text string) []string {
	text = strings.ToLower(text)
	var out []string
	start := -1
	for i := 0; i < len(text); i++ {
		c := text[i]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 {
			out = append(out, text[start:i])
			start = -1
		}
	}
	if start >= 0 {
		out = append(out, text[start:])
	}
	return out
}
