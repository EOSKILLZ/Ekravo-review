package config

import (
	"bufio"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the full runtime configuration, resolved from the environment.
type Config struct {
	GitHub    GitHub
	Bot       Bot
	LLM       LLM
	Context   Context
	Research  Research
	Dashboard Dashboard
	Store     Store
	Log       Log
}

type GitHub struct {
	Token          string
	AppID          int64
	PrivateKeyPEM  string
	InstallationID int64
	BaseURL        string
	Repos          []string
	IgnoreRepos    map[string]struct{}
}

type Bot struct {
	Handle          string
	RepoURL         string
	AutoApprove     bool
	AutoMerge       bool
	AutoMergeMethod string
	SkipDrafts      bool
	RecheckUpdated  bool
	PollInterval    time.Duration
	WebhookSecret   string
	Address         string
	Workers         int
	QueueSize       int
	IgnorePatterns  []string
	MaxFiles        int
	MaxLinesPerFile int
}

type LLM struct {
	BaseURL         string
	APIKey          string
	Models          []string
	Timeout         time.Duration
	MaxTokens       int
	Temperature     float64
	ChunkChars      int
	MaxFindings     int
	ReasoningEffort string
}

// Context controls repository-wide retrieval.
type Context struct {
	Mode          string
	Fetch         string
	CacheDir      string
	MaxChars      int
	MaxTotalChars int
	MaxFiles      int
	MaxFileKB     int
	MaxRepoMB     int
	CacheRows     int
	RatioMax      float64
	MinDiffChars  int
	Timeout       time.Duration
	Workers       int
	Languages     []string
	Learnings     bool
	SimHash       bool
}

// Research controls the hosted web lookup used to verify external claims.
type Research struct {
	Enabled    bool
	MaxQueries int
	Timeout    time.Duration
	MaxResults int
}

// Dashboard controls the embedded review dashboard.
type Dashboard struct {
	Enabled bool
	Token   string
}

type Store struct {
	Path            string
	LegacyStateFile string
}

type Log struct {
	Level  string
	Format string
}

const defaultIgnorePatterns = "*.lock,package-lock.json,yarn.lock,pnpm-lock.yaml," +
	"*.min.js,*.min.css,*.map,dist/*,build/*,.next/*,__pycache__/*,vendor/*"

// Load reads .env (if present) and then the process environment.
func Load() (*Config, error) {
	if err := loadDotEnv(".env"); err != nil {
		return nil, err
	}

	c := &Config{
		GitHub: GitHub{
			Token:          os.Getenv("GITHUB_TOKEN"),
			AppID:          envInt64("GITHUB_APP_ID", 0),
			InstallationID: envInt64("GITHUB_APP_INSTALLATION_ID", 0),
			BaseURL:        envString("GITHUB_API_URL", "https://api.github.com"),
			Repos:          envList("GITHUB_REPO"),
			IgnoreRepos:    setOf(envList("IGNORE_REPOS")),
		},
		Bot: Bot{
			Handle:          envString("BOT_HANDLE", "ekravo-review"),
			RepoURL:         envString("REPO_URL", "https://github.com/ekravo/Ekravo-review"),
			AutoApprove:     envBool("AUTO_APPROVE", false),
			AutoMerge:       envBool("AUTO_MERGE", false),
			AutoMergeMethod: strings.ToLower(envString("AUTO_MERGE_METHOD", "merge")),
			SkipDrafts:      envBool("SKIP_DRAFT_PRS", true),
			RecheckUpdated:  envBool("RECHECK_UPDATED_PRS", true),
			PollInterval:    envDuration("POLL_INTERVAL", 300*time.Second),
			WebhookSecret:   os.Getenv("WEBHOOK_SECRET"),
			Address:         envString("ADDR", fmt.Sprintf(":%d", envInt("WEBHOOK_PORT", 8090))),
			Workers:         envInt("WORKERS", 4),
			QueueSize:       envInt("QUEUE_SIZE", 100),
			IgnorePatterns:  envListDefault("IGNORE_FILE_PATTERNS", defaultIgnorePatterns),
			MaxFiles:        envInt("MAX_FILES_IN_DIFF", 100),
			MaxLinesPerFile: envInt("MAX_LINES_PER_FILE", 2000),
		},
		LLM: LLM{
			BaseURL: envString("LLM_BASE_URL", "https://ekravo.com/v1"),
			APIKey:  os.Getenv("LLM_API_KEY"), Models: envListDefault("LLM_MODELS", "deepseek-api/deepseek-flash,deepseek/deepseek-v4-flash,openrouter/free"),
			Timeout:         envDuration("LLM_TIMEOUT", 180*time.Second),
			MaxTokens:       envInt("LLM_MAX_TOKENS", 4096),
			Temperature:     envFloat("LLM_TEMPERATURE", 0.1),
			ChunkChars:      envInt("LLM_CHUNK_CHARS", 120_000),
			MaxFindings:     envInt("MAX_FINDINGS", 25),
			ReasoningEffort: strings.ToLower(envString("LLM_REASONING_EFFORT", "none")),
		}, Context: Context{
			Mode:          strings.ToLower(envString("CONTEXT_MODE", "off")),
			Fetch:         strings.ToLower(envString("CONTEXT_FETCH", "auto")),
			CacheDir:      envString("CONTEXT_CACHE_DIR", ".ekravo-context"),
			MaxChars:      envInt("CONTEXT_MAX_CHARS", 10_000),
			MaxTotalChars: envInt("CONTEXT_MAX_TOTAL_CHARS", 30_000),
			MaxFiles:      envInt("CONTEXT_MAX_FILES", 8),
			MaxFileKB:     envInt("CONTEXT_MAX_FILE_KB", 512),
			MaxRepoMB:     envInt("CONTEXT_MAX_REPO_MB", 200),
			CacheRows:     envInt("CONTEXT_CACHE_ROWS", 200_000),
			RatioMax:      envFloat("CONTEXT_RATIO_MAX", 0.35),
			MinDiffChars:  envInt("CONTEXT_MIN_DIFF_CHARS", 400),
			Timeout:       envDuration("CONTEXT_TIMEOUT", 20*time.Second),
			Workers:       envInt("CONTEXT_PARSE_WORKERS", 0),
			Languages:     envList("CONTEXT_LANGS"),
			Learnings:     envBool("CONTEXT_LEARNINGS", true),
			SimHash:       envBool("CONTEXT_SIMHASH", true),
		}, Research: Research{
			Enabled:    envBool("WEB_RESEARCH", true),
			MaxQueries: envInt("WEB_RESEARCH_MAX_QUERIES", 3),
			Timeout:    envDuration("WEB_RESEARCH_TIMEOUT", 15*time.Second),
			MaxResults: envInt("WEB_RESEARCH_RESULTS", 4),
		},
		Dashboard: Dashboard{
			Enabled: envBool("DASHBOARD_ENABLED", true),
			Token:   os.Getenv("DASHBOARD_TOKEN"),
		},
		Store: Store{
			Path:            envString("DB_PATH", "ekravo-review.db"),
			LegacyStateFile: envString("STATE_FILE", ".processed_prs.json"),
		},
		Log: Log{
			Level:  strings.ToUpper(envString("LOG_LEVEL", "INFO")),
			Format: envString("LOG_FORMAT", "text"),
		},
	}

	c.GitHub.PrivateKeyPEM = loadPrivateKey()

	if err := c.Validate(); err != nil {
		return nil, err
	}
	return c, nil
}

// Validate reports configuration problems that would prevent a useful start.
func (c *Config) Validate() error {
	var problems []string

	if c.GitHub.Token == "" && (c.GitHub.AppID == 0 || c.GitHub.PrivateKeyPEM == "") {
		problems = append(problems, "set GITHUB_TOKEN, or GITHUB_APP_ID + GITHUB_APP_PRIVATE_KEY_PATH")
	}
	if len(c.GitHub.Repos) == 0 && c.Bot.WebhookSecret == "" {
		problems = append(problems, "set GITHUB_REPO (owner/repo[,owner/repo2]) or WEBHOOK_SECRET for webhook mode")
	}
	if c.LLM.APIKey == "" && hostedLLM(c.LLM.BaseURL) {
		problems = append(problems, "set LLM_API_KEY (create one at https://ekravo.com)")
	}
	if len(c.LLM.Models) == 0 {
		problems = append(problems, "set LLM_MODELS")
	}
	if c.Bot.Workers < 1 {
		problems = append(problems, "WORKERS must be at least 1")
	}
	if c.Bot.QueueSize < 1 {
		problems = append(problems, "QUEUE_SIZE must be at least 1")
	}
	switch c.Bot.AutoMergeMethod {
	case "merge", "squash", "rebase":
	default:
		problems = append(problems, "AUTO_MERGE_METHOD must be merge, squash, or rebase")
	}
	if c.LLM.ChunkChars < 2000 {
		problems = append(problems, "LLM_CHUNK_CHARS must be at least 2000")
	}
	// Zero values are allowed: Config literals in tests and embedders predate
	// the context feature and mean "off with defaults".
	switch c.Context.Mode {
	case "", "off", "shadow", "on":
	default:
		problems = append(problems, "CONTEXT_MODE must be off, shadow, or on")
	}
	switch c.Context.Fetch {
	case "", "auto", "tarball", "git", "api":
	default:
		problems = append(problems, "CONTEXT_FETCH must be auto, tarball, git, or api")
	}
	if c.Context.MaxChars != 0 && c.Context.MaxChars < 1000 {
		problems = append(problems, "CONTEXT_MAX_CHARS must be at least 1000")
	}
	if c.Context.RatioMax < 0 || c.Context.RatioMax > 1 {
		problems = append(problems, "CONTEXT_RATIO_MAX must be between 0 and 1")
	}
	if c.Context.Timeout != 0 && c.Context.Timeout < time.Second {
		problems = append(problems, "CONTEXT_TIMEOUT must be at least 1s")
	}
	if c.Research.Enabled && (c.Research.MaxQueries < 1 || c.Research.MaxQueries > 10) {
		problems = append(problems, "WEB_RESEARCH_MAX_QUERIES must be between 1 and 10")
	}
	if len(c.Bot.WebhookSecret) > 0 && len(c.Bot.WebhookSecret) < 16 {
		problems = append(problems, "WEBHOOK_SECRET should be at least 16 characters")
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

// WebhookMode reports whether the bot should serve webhooks instead of polling.
func (c *Config) WebhookMode() bool { return c.Bot.WebhookSecret != "" }

// hostedLLM reports whether the endpoint is the managed Ekravo gateway, which
// always needs a key. Local OpenAI-compatible servers (Ollama, LM Studio,
// llama.cpp, vLLM) commonly run without one.
func hostedLLM(baseURL string) bool {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || u.Host == "" {
		return true
	}
	host := strings.TrimPrefix(strings.ToLower(u.Host), "www.")
	return host == "ekravo.com" || strings.HasSuffix(host, ".ekravo.com")
}

func loadPrivateKey() string {
	if path := os.Getenv("GITHUB_APP_PRIVATE_KEY_PATH"); path != "" {
		if b, err := os.ReadFile(path); err == nil {
			return strings.TrimSpace(string(b))
		}
	}
	return strings.ReplaceAll(os.Getenv("GITHUB_APP_PRIVATE_KEY"), `\n`, "\n")
}

// loadDotEnv is a deliberately small .env reader: KEY=VALUE lines, optional
// quotes, # comments. Real environment variables always win.
func loadDotEnv(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read %s: %w", path, err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		val = strings.Trim(val, `"'`)
		if key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, val)
		}
	}
	return sc.Err()
}

func setOf(vals []string) map[string]struct{} {
	m := make(map[string]struct{}, len(vals))
	for _, v := range vals {
		if v != "" {
			m[strings.ToLower(v)] = struct{}{}
		}
	}
	return m
}

func envString(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envInt64(key string, def int64) int64 {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return def
}

func envFloat(key string, def float64) float64 {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if n, err := strconv.ParseFloat(v, 64); err == nil {
			return n
		}
	}
	return def
}

func envBool(key string, def bool) bool {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		switch strings.ToLower(v) {
		case "1", "true", "yes", "on":
			return true
		case "0", "false", "no", "off":
			return false
		}
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	if d, err := time.ParseDuration(v); err == nil {
		return d
	}
	if n, err := strconv.Atoi(v); err == nil { // bare seconds, legacy style
		return time.Duration(n) * time.Second
	}
	return def
}

func envList(key string) []string {
	return splitList(os.Getenv(key))
}

func envListDefault(key, def string) []string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return splitList(v)
	}
	return splitList(def)
}

func splitList(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
