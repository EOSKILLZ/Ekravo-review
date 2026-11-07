package repocontext

import (
	"fmt"
	"strings"
	"sync"

	gts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

// queryCache compiles each language's shipped tags query once. gotreesitter
// bundles a tree-sitter tags.scm with its grammars, which gives Aider-style
// definition/reference captures without vendoring twenty query files.
var queryCache sync.Map // language name -> *gts.Query

var defNodeTypes = map[string]string{
	"function_declaration":   "function",
	"function_definition":    "function",
	"function_item":          "function",
	"method_declaration":     "method",
	"method_definition":      "method",
	"class_declaration":      "class",
	"class_definition":       "class",
	"class_specifier":        "class",
	"struct_declaration":     "type",
	"struct_item":            "type",
	"struct_specifier":       "type",
	"interface_declaration":  "interface",
	"trait_item":             "trait",
	"trait_definition":       "trait",
	"enum_declaration":       "enum",
	"enum_item":              "enum",
	"enum_specifier":         "enum",
	"type_alias_declaration": "type",
	"type_declaration":       "type",
	"record_declaration":     "type",
	"object_declaration":     "object",
	"protocol_declaration":   "interface",
	"namespace_definition":   "module",
	"mod_item":               "module",
	"impl_item":              "impl",
}

// parseTree extracts definitions and references with tree-sitter. It returns
// ErrUnsupportedLang when the grammar is unknown, so the caller can fall back.
func parseTree(path string, src []byte, entry *grammars.LangEntry) (*FileSymbols, error) {
	if entry == nil || entry.Language == nil {
		return nil, ErrUnsupportedLang
	}
	lang := entry.Language()
	parser := gts.NewParser(lang)
	tree, err := parser.Parse(src)
	if err != nil {
		return nil, err
	}

	out := &FileSymbols{Path: path, Lang: strings.ToLower(entry.Name)}
	seenDef := map[string]bool{}
	seenRef := map[string]bool{}

	if entry.TagsQuery != "" {
		if q := compiledQuery(entry); q != nil {
			for _, match := range q.Execute(tree) {
				var nameNode *gts.Node
				var defNode *gts.Node
				defKind := ""
				var refNode *gts.Node
				refKind := ""
				for _, c := range match.Captures {
					switch {
					case strings.HasPrefix(c.Name, "name.definition."):
						nameNode = c.Node
						defKind = strings.TrimPrefix(c.Name, "name.definition.")
					case c.Name == "name" && defNode != nil:
						nameNode = c.Node
					case strings.HasPrefix(c.Name, "definition."):
						defNode = c.Node
						if defKind == "" {
							defKind = strings.TrimPrefix(c.Name, "definition.")
						}
					case strings.HasPrefix(c.Name, "name.reference."):
						refNode = c.Node
						refKind = strings.TrimPrefix(c.Name, "name.reference.")
					case strings.HasPrefix(c.Name, "reference.") && refNode == nil:
						refNode = c.Node
					}
				}
				if defNode != nil && nameNode != nil {
					addTreeDef(out, src, defNode, nameNode, defKind, lang, seenDef)
				}
				if refNode != nil {
					name := strings.TrimSpace(refNode.Text(src))
					line := int(refNode.StartPoint().Row) + 1
					if name != "" && len(name) < 200 {
						key := fmt.Sprintf("%d:%s", line, name)
						if !seenRef[key] {
							seenRef[key] = true
							out.Refs = append(out.Refs, Ref{Name: name, Line: line, Kind: refKind})
						}
					}
				}
			}
		}
	}

	// Tags queries do not exist for every grammar, and some only capture
	// definitions. Backfill definitions structurally, and identifiers as refs.
	if len(out.Defs) == 0 || len(out.Refs) == 0 {
		walkGeneric(tree.RootNode(), lang, src, out, seenDef, seenRef)
	}

	if len(out.Refs) > 6000 {
		out.Refs = out.Refs[:6000]
	}
	return out, nil
}

func compiledQuery(entry *grammars.LangEntry) *gts.Query {
	key := strings.ToLower(entry.Name)
	if v, ok := queryCache.Load(key); ok {
		return v.(*gts.Query)
	}
	q, err := gts.NewQuery(entry.TagsQuery, entry.Language())
	if err != nil {
		queryCache.Store(key, (*gts.Query)(nil))
		return nil
	}
	queryCache.Store(key, q)
	return q
}

func addTreeDef(out *FileSymbols, src []byte, defNode, nameNode *gts.Node, kind string, lang *gts.Language, seen map[string]bool) {
	name := strings.TrimSpace(nameNode.Text(src))
	if name == "" || len(name) > 200 {
		return
	}
	line := int(defNode.StartPoint().Row) + 1
	key := fmt.Sprintf("%d:%s", line, name)
	if seen[key] {
		return
	}
	seen[key] = true

	if kind == "" {
		kind = "definition"
	}
	start, end := int(defNode.StartByte()), int(defNode.EndByte())
	sig := treeSignature(defNode, lang, src)
	out.Defs = append(out.Defs, Symbol{
		Name: name, Kind: kind,
		Line: line, EndLine: int(defNode.EndPoint().Row) + 1,
		StartByte: start, EndByte: end,
		Signature: sig,
		Exported:  name[0] >= 'A' && name[0] <= 'Z',
	})
}

func treeSignature(n *gts.Node, lang *gts.Language, src []byte) string {
	start := int(n.StartByte())
	end := int(n.EndByte())
	if body := n.ChildByFieldName("body", lang); body != nil {
		end = int(body.StartByte())
	}
	if start < 0 || end > len(src) || start >= end {
		return ""
	}
	return cleanSignature(string(src[start:end]))
}

func walkGeneric(root *gts.Node, lang *gts.Language, src []byte, out *FileSymbols, seenDef, seenRef map[string]bool) {
	var walk func(n *gts.Node, depth int)
	walk = func(n *gts.Node, depth int) {
		if n == nil || depth > 80 {
			return
		}
		typ := n.Type(lang)
		if kind, ok := defNodeTypes[typ]; ok {
			name := n.ChildByFieldName("name", lang)
			if name == nil {
				for i := 0; i < n.NamedChildCount(); i++ {
					c := n.NamedChild(i)
					switch c.Type(lang) {
					case "identifier", "field_identifier", "type_identifier", "property_identifier", "constant":
						name = c
					}
					if name != nil {
						break
					}
				}
			}
			if name != nil {
				addTreeDef(out, src, n, name, kind, lang, seenDef)
			}
		}

		switch typ {
		case "identifier", "field_identifier", "type_identifier", "property_identifier", "constant", "variable_name":
			text := strings.TrimSpace(n.Text(src))
			line := int(n.StartPoint().Row) + 1
			if len(text) >= 2 && len(text) < 200 {
				key := fmt.Sprintf("%d:%s", line, text)
				if !seenRef[key] {
					seenRef[key] = true
					out.Refs = append(out.Refs, Ref{Name: text, Line: line, Kind: "identifier"})
				}
			}
		}

		for i := 0; i < n.NamedChildCount(); i++ {
			walk(n.NamedChild(i), depth+1)
		}
	}
	walk(root, 0)
}
