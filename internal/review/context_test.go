package review

import (
	"strings"
	"testing"

	"github.com/ekravo/Ekravo-review/v2/internal/config"
)

func TestUserPromptPlacesContextBeforeDiff(t *testing.T) {
	meta := Meta{Number: 1, Title: "x", Author: "a", Base: "main", Head: "f"}
	block := "### reference: a.go:1-2 · defines A\n    1 | func A() {}\n"
	prompt := UserPromptWithContext(meta, "manifest-line", block, "+ 1 | x", 0, 1, false, false)

	ref := strings.Index(prompt, "Repository reference")
	diff := strings.Index(prompt, "Diff (marker")
	if ref < 0 || diff < 0 || ref > diff {
		t.Fatalf("context placement wrong:\n%s", prompt)
	}
	if !strings.Contains(prompt, "defines A") {
		t.Fatal("context body missing")
	}
}

func TestUserPromptWithoutContextHasNoReferenceBlock(t *testing.T) {
	prompt := UserPrompt(Meta{Number: 1}, "", "+ 1 | x", 0, 1, false, false)
	if strings.Contains(prompt, "Repository reference") {
		t.Fatal("unexpected reference block")
	}
}

func TestSystemPromptLearningsAreBounded(t *testing.T) {
	rc := config.DefaultRepoConfig()
	long := strings.Repeat("rule ", 400)
	system := SystemPromptWithLearnings(rc, []string{long, "short rule"})
	if !strings.Contains(system, "Repository learnings") {
		t.Fatal("learnings header missing")
	}
	if len(system) > len(SystemPrompt(rc))+1500 {
		t.Fatalf("learnings block too large: %d bytes", len(system))
	}
}
