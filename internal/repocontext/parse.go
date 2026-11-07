package repocontext

import (
	"errors"
	"path"
	"regexp"
	"strings"

	"github.com/odvcencio/gotreesitter/grammars"
)

// ErrUnsupportedLang means no grammar and no useful fallback apply.
var ErrUnsupportedLang = errors.New("unsupported language")

var (
	regexDef = regexp.MustCompile(`(?m)^\s*(?:export\s+)?(?:async\s+)?(?:def|func|function|class|interface|struct|type|module|object|trait)\s+([A-Za-z_$][\w$]*)`)
	regexRef = regexp.MustCompile(`[A-Za-z_$][\w$]*`)
)

// Parse dispatches one file to the fastest correct extractor. Languages is an
// optional allow-list; nil means every registered grammar.
func Parse(filePath string, src []byte, languages []string) (*FileSymbols, error) {
	fs, err := parseFile(filePath, src, languages)
	if fs == nil {
		return nil, err
	}
	// Named imports are cheap to extract and language-agnostic, so every
	// extractor gets them, including the regex fallback.
	fs.NamedImports = extractNamedImports(src)
	if len(fs.Imports) == 0 {
		fs.Imports = extractImports(src)
	}
	return fs, err
}

func parseFile(filePath string, src []byte, languages []string) (*FileSymbols, error) {
	ext := strings.ToLower(path.Ext(filePath))
	if ext == ".go" {
		return parseGo(filePath, src)
	}
	entry := grammars.DetectLanguage(filePath)
	if entry == nil {
		return parseRegex(filePath, src)
	}
	if len(languages) > 0 && !allowed(strings.ToLower(entry.Name), languages) {
		return nil, ErrUnsupportedLang
	}
	fs, err := parseTree(filePath, src, entry)
	if err == nil && (len(fs.Defs) > 0 || len(fs.Refs) > 0) {
		fs.Imports = extractImports(src)
		return fs, nil
	}
	fs, ferr := parseRegex(filePath, src)
	if ferr == nil {
		return fs, nil
	}
	return nil, err
}

func allowed(lang string, languages []string) bool {
	for _, l := range languages {
		if strings.EqualFold(l, lang) {
			return true
		}
	}
	return false
}

func parseRegex(filePath string, src []byte) (*FileSymbols, error) {
	out := &FileSymbols{Path: filePath, Lang: "text", Imports: extractImports(src)}
	lines := strings.Split(string(src), "\n")
	for i, line := range lines {
		if m := regexDef.FindStringSubmatch(line); m != nil {
			out.Defs = append(out.Defs, Symbol{
				Name: m[1], Kind: "definition", Line: i + 1, EndLine: i + 1,
				Signature: cleanSignature(line),
				Exported:  m[1][0] >= 'A' && m[1][0] <= 'Z',
			})
		}
		for _, m := range regexRef.FindAllString(line, -1) {
			if len(m) >= 3 && !stopWords[m] {
				out.Refs = append(out.Refs, Ref{Name: m, Line: i + 1, Kind: "identifier"})
			}
		}
	}
	if len(out.Refs) > 4000 {
		out.Refs = out.Refs[:4000]
	}
	return out, nil
}

var stopWords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "this": true,
	"that": true, "from": true, "import": true, "return": true, "func": true,
	"function": true, "class": true, "const": true, "var": true, "let": true,
	"nil": true, "null": true, "true": true, "false": true, "string": true,
	"int": true, "error": true, "public": true, "private": true, "static": true,
}

var importLine = regexp.MustCompile(`(?m)^\s*(?:import\s+(?:\(|\w)|from\s+\S+\s+import|require\(|use\s+|#include\s+)["']?([A-Za-z0-9_./@-]+)`)

var (
	esNamedImport = regexp.MustCompile(`(?m)^[ \t]*import[ \t]+(?:type[ \t]+)?\{([^}]*)\}[ \t]*from[ \t]*['"]([^'"]+)['"]`)
	esExportFrom  = regexp.MustCompile(`(?m)^[ \t]*export[ \t]+(?:type[ \t]+)?\{([^}]*)\}[ \t]*from[ \t]*['"]([^'"]+)['"]`)
)

// extractNamedImports records ES-module named imports and re-exports, which is
// what makes an export graph possible without a type checker.
func extractNamedImports(src []byte) []ImportName {
	var out []ImportName
	seen := map[string]bool{}
	for _, re := range []*regexp.Regexp{esNamedImport, esExportFrom} {
		for _, loc := range re.FindAllSubmatchIndex(src, -1) {
			source := string(src[loc[4]:loc[5]])
			line := 1 + strings.Count(string(src[:loc[0]]), "\n")
			for _, part := range strings.Split(string(src[loc[2]:loc[3]]), ",") {
				part = strings.TrimSpace(part)
				part = strings.TrimPrefix(part, "type ")
				if part == "" || strings.HasPrefix(part, "*") {
					continue
				}
				fields := strings.Fields(part)
				if len(fields) == 0 {
					continue
				}
				name, local := fields[0], fields[0]
				if len(fields) >= 3 && fields[1] == "as" {
					local = fields[2]
				}
				key := source + "\x00" + name + "\x00" + local
				if seen[key] {
					continue
				}
				seen[key] = true
				out = append(out, ImportName{Source: source, Name: name, Local: local, Line: line})
				if len(out) >= 500 {
					return out
				}
			}
		}
	}
	return out
}

func extractImports(src []byte) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range importLine.FindAllSubmatch(src, -1) {
		p := string(m[1])
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
		if len(out) >= 100 {
			break
		}
	}
	return out
}
