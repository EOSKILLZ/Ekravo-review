package config

import "testing"

func TestRepoConfigDefaults(t *testing.T) {
	rc, err := ParseRepoConfig([]byte(""))
	if err != nil {
		t.Fatal(err)
	}
	if rc.Tone != "technical" || rc.Style != "detailed" || rc.BlockOn != "high" {
		t.Fatalf("defaults = %+v", rc)
	}
	if !rc.AllFocus() {
		t.Errorf("default focus should cover everything: %v", rc.Focus)
	}
	if rc.MaxFindings != 25 {
		t.Errorf("max findings = %d", rc.MaxFindings)
	}
}

func TestRepoConfigRejectsUnknownValues(t *testing.T) {
	rc, err := ParseRepoConfig([]byte(`
tone: nonsense
style: terse
block_on: everything
focus: [security, made-up]
ignore_patterns: [" *.gen.ts ", ""]
`))
	if err != nil {
		t.Fatal(err)
	}
	if rc.Tone != "technical" || rc.Style != "detailed" || rc.BlockOn != "high" {
		t.Fatalf("normalised = %+v", rc)
	}
	if len(rc.Focus) != 1 || rc.Focus[0] != "security" {
		t.Fatalf("focus = %v", rc.Focus)
	}
	if len(rc.IgnorePatterns) != 1 || rc.IgnorePatterns[0] != "*.gen.ts" {
		t.Fatalf("patterns = %v", rc.IgnorePatterns)
	}
}

func TestRepoConfigClampsMaxFindings(t *testing.T) {
	rc, err := ParseRepoConfig([]byte("max_findings: 500"))
	if err != nil {
		t.Fatal(err)
	}
	if rc.MaxFindings != 100 {
		t.Fatalf("max findings = %d, want 100", rc.MaxFindings)
	}
}

func TestToneRuleBalancedIsEmpty(t *testing.T) {
	rc := DefaultRepoConfig()
	rc.Tone = "balanced"
	if rc.ToneRule() != "" {
		t.Error("balanced tone should add no rule")
	}
}
