package diff

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// Meta is the cheap, deterministic context a reviewer gets about a file before
// reading a single line of it: what language it is, what role it plays in the
// repository, and whether a tool generated it.
type Meta struct {
	Language  string
	Role      string
	Generated bool
}

// Label renders the metadata for a prompt header, e.g. "Go · test · generated".
func (m Meta) Label() string {
	lang := m.Language
	if lang == "" {
		lang = "text"
	}
	role := m.Role
	if role == "" {
		role = "source"
	}
	if m.Generated {
		return lang + " · " + role + " · generated"
	}
	return lang + " · " + role
}

var languageByExt = map[string]string{
	".go": "Go", ".py": "Python", ".pyi": "Python", ".ts": "TypeScript", ".tsx": "TypeScript",
	".js": "JavaScript", ".jsx": "JavaScript", ".mjs": "JavaScript", ".cjs": "JavaScript",
	".rs": "Rust", ".java": "Java", ".kt": "Kotlin", ".kts": "Kotlin", ".scala": "Scala",
	".rb": "Ruby", ".php": "PHP", ".cs": "C#", ".fs": "F#", ".c": "C", ".h": "C",
	".cc": "C++", ".cpp": "C++", ".cxx": "C++", ".hpp": "C++", ".hh": "C++",
	".swift": "Swift", ".m": "Objective-C", ".mm": "Objective-C", ".dart": "Dart",
	".ex": "Elixir", ".exs": "Elixir", ".erl": "Erlang", ".hs": "Haskell",
	".clj": "Clojure", ".lua": "Lua", ".zig": "Zig", ".nim": "Nim", ".jl": "Julia",
	".r": "R", ".pl": "Perl", ".sh": "Shell", ".bash": "Shell", ".zsh": "Shell",
	".ps1": "PowerShell", ".sql": "SQL", ".proto": "Protocol Buffers",
	".html": "HTML", ".htm": "HTML", ".css": "CSS", ".scss": "SCSS", ".sass": "Sass",
	".less": "Less", ".vue": "Vue", ".svelte": "Svelte", ".astro": "Astro",
	".yaml": "YAML", ".yml": "YAML", ".json": "JSON", ".jsonc": "JSON",
	".toml": "TOML", ".ini": "INI", ".cfg": "Config", ".conf": "Config",
	".md": "Markdown", ".mdx": "MDX", ".rst": "reStructuredText", ".txt": "Text",
	".tf": "Terraform", ".hcl": "HCL", ".gradle": "Gradle", ".cmake": "CMake",
	".dockerfile": "Dockerfile", ".prisma": "Prisma", ".graphql": "GraphQL",
	".gql": "GraphQL", ".ipynb": "Notebook",
}

var languageByName = map[string]string{
	"dockerfile": "Dockerfile", "containerfile": "Dockerfile", "makefile": "Make",
	"gnumakefile": "Make", "rakefile": "Ruby", "gemfile": "Ruby", "procfile": "Config",
	"go.mod": "Go", "go.sum": "Go", "package.json": "JSON", "tsconfig.json": "JSON",
	"cargo.toml": "TOML", "cargo.lock": "TOML", "pyproject.toml": "TOML",
	"requirements.txt": "Text", "poetry.lock": "TOML", "uv.lock": "TOML",
	"composer.json": "JSON", "pom.xml": "XML", "build.gradle": "Gradle",
	"jenkinsfile": "Groovy", "vagrantfile": "Ruby", "license": "Text",
	"nginx.conf": "Nginx", ".gitignore": "Git", ".dockerignore": "Dockerfile",
	".env": "Env", ".env.example": "Env",
}

// Classify returns the language, role, and generated flag for a path.
func Classify(p string) Meta {
	p = strings.ReplaceAll(strings.TrimSpace(p), `\`, "/")
	base := strings.ToLower(path.Base(p))
	lower := strings.ToLower(p)

	m := Meta{Language: languageByName[base], Role: "source"}
	if m.Language == "" {
		m.Language = languageByExt[strings.ToLower(path.Ext(p))]
	}

	role := func(r string) { m.Role = r }

	switch {
	case isGenerated(lower, base):
		m.Generated = true
		role("source")
		if isLockfile(base) {
			role("lockfile")
		}
	case isLockfile(base):
		m.Generated = true
		role("lockfile")
	case isTest(lower, base):
		role("test")
	case isMigration(lower):
		role("migration")
	case isCI(lower, base):
		role("ci")
	case isDoc(lower, base):
		role("docs")
	case isStyle(lower):
		role("style")
	case isScript(lower):
		role("script")
	case isConfig(lower, base) && m.Language != "Go" && m.Language != "Python" &&
		m.Language != "TypeScript" && m.Language != "JavaScript":
		role("config")
	}

	if m.Language == "" {
		m.Language = "text"
	}
	return m
}

func isGenerated(p, base string) bool {
	if strings.Contains(p, "/vendor/") || strings.Contains(p, "/node_modules/") ||
		strings.Contains(p, "/dist/") || strings.Contains(p, "/build/") ||
		strings.Contains(p, "/generated/") || strings.Contains(p, "/__generated__/") {
		return true
	}
	for _, suffix := range []string{
		".min.js", ".min.css", ".map", ".pb.go", "_pb2.py", ".pb.cc", ".pb.h",
		".g.dart", ".freezed.dart", ".gen.go", ".generated.go", "_generated.go",
		".designer.cs", ".g.cs", ".snap",
	} {
		if strings.HasSuffix(base, suffix) {
			return true
		}
	}
	return false
}

func isLockfile(base string) bool {
	switch base {
	case "go.sum", "package-lock.json", "yarn.lock", "pnpm-lock.yaml", "bun.lockb",
		"cargo.lock", "poetry.lock", "uv.lock", "pipfile.lock", "gemfile.lock",
		"composer.lock", "packages.lock.json", "mix.lock", "pubspec.lock":
		return true
	}
	return false
}

func isTest(p, base string) bool {
	if strings.HasSuffix(base, "_test.go") || strings.HasSuffix(base, "_test.py") ||
		strings.Contains(base, ".test.") || strings.Contains(base, ".spec.") ||
		strings.HasSuffix(base, "_spec.rb") || strings.HasSuffix(base, "test.java") ||
		strings.HasSuffix(base, "tests.cs") {
		return true
	}
	for _, marker := range []string{"/test/", "/tests/", "/__tests__/", "/spec/", "/e2e/"} {
		if strings.Contains(p, marker) {
			return true
		}
	}
	return false
}

func isMigration(p string) bool {
	return strings.Contains(p, "/migrations/") || strings.Contains(p, "/migrate/") ||
		strings.Contains(p, "/schema/migrations/") || strings.HasSuffix(p, ".sql")
}

func isCI(p, base string) bool {
	return strings.Contains(p, "/.github/workflows/") || strings.HasPrefix(p, ".github/workflows/") ||
		strings.Contains(p, "/.circleci/") ||
		base == ".gitlab-ci.yml" || base == "jenkinsfile" || base == ".travis.yml" ||
		strings.HasPrefix(base, "dockerfile") || base == "docker-compose.yml" ||
		base == "docker-compose.yaml" || base == "compose.yml" || base == "compose.yaml"
}

func isDoc(p, base string) bool {
	switch strings.ToLower(path.Ext(base)) {
	case ".md", ".mdx", ".rst", ".adoc", ".txt":
		return true
	}
	return base == "license" || base == "notice" || base == "changelog" ||
		strings.HasPrefix(p, "docs/")
}

func isStyle(p string) bool {
	switch strings.ToLower(path.Ext(p)) {
	case ".css", ".scss", ".sass", ".less":
		return true
	}
	return false
}

func isScript(p string) bool {
	switch strings.ToLower(path.Ext(p)) {
	case ".sh", ".bash", ".zsh", ".ps1", ".bat", ".cmd":
		return true
	}
	return false
}

func isConfig(p, base string) bool {
	switch strings.ToLower(path.Ext(base)) {
	case ".yml", ".yaml", ".toml", ".ini", ".cfg", ".conf", ".properties", ".env":
		return true
	}
	return base == ".env" || strings.HasPrefix(base, ".env.") || base == "makefile" ||
		base == "cmakelists.txt" || strings.HasSuffix(base, ".tf") || base == ".editorconfig"
}

// RepoContext summarises the shape of the change set. It is inferred from file
// names only, so it is context for interpretation, never evidence of a defect.
func RepoContext(files []*File) string {
	if len(files) == 0 {
		return ""
	}

	langs := map[string]int{}
	roles := map[string]int{}
	var ecosystems []string
	seenEco := map[string]bool{}

	addEco := func(name string) {
		if name != "" && !seenEco[name] {
			seenEco[name] = true
			ecosystems = append(ecosystems, name)
		}
	}

	for _, f := range files {
		lang := f.Meta.Language
		if lang == "" {
			lang = "text"
		}
		langs[lang]++
		role := f.Meta.Role
		if role == "" {
			role = "source"
		}
		roles[role]++

		base := strings.ToLower(path.Base(f.Path))
		switch base {
		case "go.mod", "go.sum":
			addEco("Go modules")
		case "package.json", "package-lock.json", "yarn.lock", "pnpm-lock.yaml", "bun.lockb":
			addEco("Node.js")
		case "pnpm-workspace.yaml":
			addEco("pnpm workspace")
		case "tsconfig.json":
			addEco("TypeScript")
		case "pyproject.toml", "requirements.txt", "poetry.lock", "uv.lock", "setup.py":
			addEco("Python")
		case "cargo.toml", "cargo.lock":
			addEco("Rust")
		case "gemfile", "gemfile.lock":
			addEco("Ruby")
		case "composer.json":
			addEco("PHP")
		case "pom.xml", "build.gradle", "build.gradle.kts":
			addEco("JVM")
		case "dockerfile", "docker-compose.yml", "docker-compose.yaml", "compose.yml":
			addEco("Docker")
		case "makefile":
			addEco("Make")
		case "schema.prisma":
			addEco("Prisma")
		}
		lowerPath := strings.ToLower(f.Path)
		if strings.Contains(lowerPath, "/.github/workflows/") || strings.HasPrefix(lowerPath, ".github/workflows/") {
			addEco("GitHub Actions")
		}
		if strings.HasSuffix(lowerPath, ".tf") {
			addEco("Terraform")
		}
	}

	var b strings.Builder
	b.WriteString("Repository context (inferred from the changed file names; context, not evidence):\n")
	fmt.Fprintf(&b, "- Languages: %s\n", topCounts(langs, 5))
	fmt.Fprintf(&b, "- File roles: %s\n", topCounts(roles, 6))
	if len(ecosystems) > 0 {
		fmt.Fprintf(&b, "- Ecosystems touched: %s\n", strings.Join(ecosystems, ", "))
	}
	return b.String()
}

func topCounts(counts map[string]int, limit int) string {
	type kv struct {
		key string
		n   int
	}
	list := make([]kv, 0, len(counts))
	for k, n := range counts {
		list = append(list, kv{k, n})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].n != list[j].n {
			return list[i].n > list[j].n
		}
		return list[i].key < list[j].key
	})
	if len(list) > limit {
		list = list[:limit]
	}
	parts := make([]string, 0, len(list))
	for _, e := range list {
		parts = append(parts, fmt.Sprintf("%s (%d)", e.key, e.n))
	}
	return strings.Join(parts, ", ")
}
