package config

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// RepoConfig is the per-repository tuning loaded from .ekravo.yml.
type RepoConfig struct {
	Tone               string   `yaml:"tone"`
	Style              string   `yaml:"style"`
	Focus              []string `yaml:"focus"`
	IgnorePatterns     []string `yaml:"ignore_patterns"`
	CustomInstructions string   `yaml:"custom_instructions"`
	MaxFindings        int      `yaml:"max_findings"`
	BlockOn            string   `yaml:"block_on"`
}

var validTones = map[string]bool{"technical": true, "strict": true, "balanced": true, "lenient": true}

var validFocus = map[string]string{
	"bugs":          "Bugs & Logic Errors",
	"security":      "Security",
	"performance":   "Performance",
	"code-quality":  "Code Quality",
	"test-coverage": "Test Coverage",
	"docs":          "Documentation",
}

// ParseRepoConfig decodes and normalises .ekravo.yml.
func ParseRepoConfig(data []byte) (*RepoConfig, error) {
	var rc RepoConfig
	if err := yaml.Unmarshal(data, &rc); err != nil {
		return nil, fmt.Errorf("parse .ekravo.yml: %w", err)
	}
	rc.normalize()
	return &rc, nil
}

// DefaultRepoConfig matches the documented behaviour when no file exists.
func DefaultRepoConfig() *RepoConfig {
	rc := &RepoConfig{}
	rc.normalize()
	return rc
}

func (rc *RepoConfig) normalize() {
	rc.Tone = strings.ToLower(strings.TrimSpace(rc.Tone))
	if !validTones[rc.Tone] {
		rc.Tone = "technical"
	}

	rc.Style = strings.ToLower(strings.TrimSpace(rc.Style))
	if rc.Style != "concise" {
		rc.Style = "detailed"
	}

	rc.BlockOn = strings.ToLower(strings.TrimSpace(rc.BlockOn))
	switch rc.BlockOn {
	case "critical", "high", "medium", "none":
	default:
		rc.BlockOn = "high"
	}

	if rc.MaxFindings <= 0 {
		rc.MaxFindings = 25
	}
	if rc.MaxFindings > 100 {
		rc.MaxFindings = 100
	}

	seen := map[string]bool{}
	var focus []string
	for _, f := range rc.Focus {
		f = strings.ToLower(strings.TrimSpace(f))
		if _, ok := validFocus[f]; ok && !seen[f] {
			seen[f] = true
			focus = append(focus, f)
		}
	}
	if len(focus) == 0 {
		for k := range validFocus {
			focus = append(focus, k)
		}
	}
	rc.Focus = focus

	var patterns []string
	for _, p := range rc.IgnorePatterns {
		if p = strings.TrimSpace(p); p != "" {
			patterns = append(patterns, p)
		}
	}
	rc.IgnorePatterns = patterns
	rc.CustomInstructions = strings.TrimSpace(rc.CustomInstructions)
}

// FocusLabels returns the human labels for the active focus areas, sorted.
func (rc *RepoConfig) FocusLabels() []string {
	order := []string{"bugs", "security", "performance", "code-quality", "test-coverage", "docs"}
	var out []string
	for _, k := range order {
		for _, f := range rc.Focus {
			if f == k {
				out = append(out, validFocus[k])
				break
			}
		}
	}
	return out
}

// AllFocus reports whether every focus area is active.
func (rc *RepoConfig) AllFocus() bool { return len(rc.Focus) >= len(validFocus) }

// ToneRule returns the tone instruction, or "" for balanced.
func (rc *RepoConfig) ToneRule() string {
	switch rc.Tone {
	case "technical":
		return "Tone: senior security-conscious engineer reviewing production code. " +
			"Assume adversarial users and real traffic. Be direct: no praise, no filler. " +
			"For every problem state the concrete risk and a one-line fix."
	case "strict":
		return "Tone: strict. Report every real problem, including minor ones. Hold the code to a production standard."
	case "lenient":
		return "Tone: relaxed. Report only Critical and High problems; skip style nits."
	default:
		return ""
	}
}
