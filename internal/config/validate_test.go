package config

import (
	"strings"
	"testing"
)

func validConfig() *Config {
	return &Config{
		GitHub: GitHub{Token: "t", Repos: []string{"o/r"}},
		Bot:    Bot{Workers: 1, QueueSize: 1, AutoMergeMethod: "merge"},
		LLM:    LLM{APIKey: "k", Models: []string{"m"}, ChunkChars: 2000},
	}
}

func TestValidateAPIKeyOnlyRequiredForHostedEkravo(t *testing.T) {
	local := validConfig()
	local.LLM.APIKey = ""
	local.LLM.BaseURL = "http://127.0.0.1:11434/v1"
	if err := local.Validate(); err != nil {
		t.Fatalf("keyless local endpoint rejected: %v", err)
	}

	hosted := validConfig()
	hosted.LLM.APIKey = ""
	hosted.LLM.BaseURL = "https://ekravo.com/v1"
	if err := hosted.Validate(); err == nil || !strings.Contains(err.Error(), "LLM_API_KEY") {
		t.Fatalf("hosted endpoint should require a key, got %v", err)
	}
}

func TestValidateAutoMergeMethod(t *testing.T) {
	c := validConfig()
	c.Bot.AutoMergeMethod = "octopus"
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "AUTO_MERGE_METHOD") {
		t.Fatalf("err = %v, want AUTO_MERGE_METHOD problem", err)
	}

	for _, method := range []string{"merge", "squash", "rebase"} {
		c := validConfig()
		c.Bot.AutoMergeMethod = method
		if err := c.Validate(); err != nil {
			t.Fatalf("%s rejected: %v", method, err)
		}
	}
}
