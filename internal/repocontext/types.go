// Package repocontext builds a structural index of a repository at a pull
// request's base commit and retrieves the small, validated slices of code that
// make a diff reviewable in context.
//
// Nothing in this package calls a model. Parsing, graph construction, ranking,
// and rendering are deterministic, so context cost is bounded and reproducible.
package repocontext

import (
	"context"
	"io"
	"time"
)

// Symbol is one definition extracted from a source file.
type Symbol struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"` // function, method, class, type, ...
	Line      int    `json:"line"`
	EndLine   int    `json:"end_line"`
	StartByte int    `json:"-"`
	EndByte   int    `json:"-"`
	Signature string `json:"signature,omitempty"`
	Exported  bool   `json:"exported,omitempty"`
	// Receiver is the receiver type name for a Go method, which lets the index
	// compare a type's method set against an interface declaration.
	Receiver string `json:"receiver,omitempty"`
	// Methods lists the method names declared by an interface.
	Methods []string `json:"methods,omitempty"`
	SimHash uint64   `json:"-"`
	Body    string   `json:"-"`
}

// Ref is a name use inside a file.
type Ref struct {
	Name string `json:"name"`
	Line int    `json:"line"`
	Kind string `json:"kind"` // call, reference, identifier
}

// ImportName is one named ES-module import, kept so a removed export can be
// traced to the files that still import it.
type ImportName struct {
	Source string `json:"source"`
	Name   string `json:"name"`
	Local  string `json:"local,omitempty"`
	Line   int    `json:"line"`
}

// FileSymbols is the cached parse result for one file.
type FileSymbols struct {
	Path         string       `json:"path"`
	Lang         string       `json:"lang"`
	Defs         []Symbol     `json:"defs"`
	Refs         []Ref        `json:"refs"`
	Imports      []string     `json:"imports"`
	NamedImports []ImportName `json:"named_imports,omitempty"`
}

// TreeEntry is one path in a repository tree.
type TreeEntry struct {
	Path string
	Size int
	Dir  bool
}

// Source is the subset of the GitHub API the context engine needs. The app
// adapts *githubx.Client to it, and tests use a fake.
type Source interface {
	RepoSizeKB(ctx context.Context, repo string) (int, error)
	Tarball(ctx context.Context, repo, ref string, w io.Writer) error
	Tree(ctx context.Context, repo, ref string) ([]TreeEntry, error)
	FileAt(ctx context.Context, repo, path, ref string) ([]byte, error)
	// GitAuth returns the value for the git http.extraheader config, or "".
	GitAuth(ctx context.Context, repo string) (string, error)
}

// Options bounds what the engine may fetch, parse, and send.
type Options struct {
	Fetch         string // auto | tarball | git | api
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

// DefaultOptions are safe production defaults.
func DefaultOptions() Options {
	return Options{
		Fetch:         "auto",
		CacheDir:      ".ekravo-context",
		MaxChars:      10_000,
		MaxTotalChars: 30_000,
		MaxFiles:      8,
		MaxFileKB:     512,
		MaxRepoMB:     200,
		CacheRows:     200_000,
		RatioMax:      0.35,
		MinDiffChars:  400,
		Timeout:       20 * time.Second,
		Workers:       0, // NumCPU, capped at 8
		SimHash:       true,
	}
}

// Budget returns the context character budget for one section.
func (o Options) Budget(diffChars int) int {
	if diffChars < o.MinDiffChars {
		return 0
	}
	budget := int(float64(diffChars) * o.RatioMax)
	if budget > o.MaxChars {
		budget = o.MaxChars
	}
	if budget < 2_000 {
		budget = 2_000
	}
	if budget > o.MaxChars {
		budget = o.MaxChars
	}
	return budget
}

// Stats describes one retrieval.
type Stats struct {
	Files       int
	Chars       int
	Snippets    int
	Partial     bool
	IndexMS     int64
	Fetched     int // files fetched/parsed for the index
	Cached      int // parse cache hits
	Unresolved  int // candidates dropped because they did not resolve
	Symbols     int
	Mode        string
	IndexSource string
}

// Index is the structural view of a repository at one commit.
type Index struct {
	Repo    string
	SHA     string
	Files   []*IndexFile
	Partial bool
	Source  string // tarball | git | api | cache
	BuiltAt time.Time

	byPath     map[string]int
	bySymbol   map[string][]SymRef
	graph      map[int]map[int]float64
	methodSets map[string]map[string]bool
	typeDefs   map[string]typeDef
}

// typeDef is where a named type is declared.
type typeDef struct {
	Path string
	Line int
}

// IndexFile is one parsed source file plus its source text.
type IndexFile struct {
	Path    string
	Lang    string
	Source  []byte
	Symbols *FileSymbols
	Test    bool
}

// SymRef points at a definition or a use.
type SymRef struct {
	File int
	Sym  *Symbol // set for definitions
	Ref  *Ref    // set for references
}

// Impact is a deterministic, structural warning about the change.
type Impact struct {
	Kind   string `json:"kind"`
	Symbol string `json:"symbol"`
	Path   string `json:"path"`
	Line   int    `json:"line"`
	Detail string `json:"detail"`
}
