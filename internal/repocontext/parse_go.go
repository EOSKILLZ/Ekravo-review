package repocontext

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
)

// parseGo is the fast path: the standard library parser indexes thousands of
// Go files per second and gives exact declarations, signatures, receivers, and
// identifier uses without a type checker.
func parseGo(path string, src []byte) (*FileSymbols, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, src, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}

	out := &FileSymbols{Path: path, Lang: "go"}
	defPos := map[token.Pos]bool{}

	// add appends one definition and returns its index, so callers can attach
	// receiver or interface-method detail without a second pass.
	add := func(name, kind string, start, end token.Pos, sig string, exported bool) int {
		if name == "" || name == "_" {
			return -1
		}
		out.Defs = append(out.Defs, Symbol{
			Name:      name,
			Kind:      kind,
			Line:      fset.Position(start).Line,
			EndLine:   fset.Position(end).Line,
			StartByte: fset.Position(start).Offset,
			EndByte:   fset.Position(end).Offset,
			Signature: cleanSignature(sig),
			Exported:  exported,
		})
		return len(out.Defs) - 1
	}

	ast.Inspect(f, func(n ast.Node) bool {
		switch d := n.(type) {
		case *ast.FuncDecl:
			kind := "function"
			if d.Recv != nil {
				kind = "method"
			}
			end := d.End()
			if d.Body != nil {
				end = d.Body.Lbrace
			}
			i := add(d.Name.Name, kind, d.Pos(), d.End(),
				nodeText(fset, src, d.Pos(), end), ast.IsExported(d.Name.Name))
			if i >= 0 && d.Recv != nil {
				out.Defs[i].Receiver = receiverType(d.Recv)
			}
			defPos[d.Name.Pos()] = true

		case *ast.GenDecl:
			kind := ""
			switch d.Tok {
			case token.TYPE:
				kind = "type"
			case token.CONST:
				kind = "const"
			case token.VAR:
				kind = "var"
			default:
				return true
			}
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					kind, methods := "type", []string(nil)
					if iface, ok := s.Type.(*ast.InterfaceType); ok {
						kind, methods = "interface", interfaceMethodNames(iface)
					}
					i := add(s.Name.Name, kind, s.Pos(), s.End(),
						nodeText(fset, src, s.Pos(), s.End()), ast.IsExported(s.Name.Name))
					if i >= 0 && len(methods) > 0 {
						out.Defs[i].Methods = methods
					}
					defPos[s.Name.Pos()] = true
				case *ast.ValueSpec:
					sig := nodeText(fset, src, s.Pos(), s.End())
					for _, name := range s.Names {
						add(name.Name, kind, name.Pos(), s.End(), sig, ast.IsExported(name.Name))
						defPos[name.Pos()] = true
					}
				}
			}
		}
		return true
	})

	for _, imp := range f.Imports {
		if imp.Path == nil {
			continue
		}
		if p, err := strconv.Unquote(imp.Path.Value); err == nil {
			out.Imports = append(out.Imports, p)
		}
	}

	seen := map[[2]int]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if !ok || id.Name == "" || id.Name == "_" || defPos[id.Pos()] {
			return true
		}
		pos := fset.Position(id.Pos())
		key := [2]int{pos.Line, int(id.Pos())}
		if seen[key] {
			return true
		}
		seen[key] = true
		out.Refs = append(out.Refs, Ref{Name: id.Name, Line: pos.Line, Kind: "identifier"})
		return true
	})

	return out, nil
}

// receiverType returns the base type name a Go method is attached to.
func receiverType(recv *ast.FieldList) string {
	if recv == nil || len(recv.List) == 0 {
		return ""
	}
	return baseTypeName(recv.List[0].Type)
}

func baseTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return baseTypeName(t.X)
	case *ast.IndexExpr:
		return baseTypeName(t.X)
	case *ast.IndexListExpr:
		return baseTypeName(t.X)
	case *ast.ParenExpr:
		return baseTypeName(t.X)
	case *ast.SelectorExpr:
		return t.Sel.Name
	}
	return ""
}

// interfaceMethodNames lists explicitly declared interface methods. Embedded
// interfaces have no field names and are skipped.
func interfaceMethodNames(iface *ast.InterfaceType) []string {
	if iface == nil || iface.Methods == nil {
		return nil
	}
	var out []string
	for _, field := range iface.Methods.List {
		for _, name := range field.Names {
			if name.Name != "_" {
				out = append(out, name.Name)
			}
		}
	}
	return out
}

func nodeText(fset *token.FileSet, src []byte, start, end token.Pos) string {
	s := fset.Position(start).Offset
	e := fset.Position(end).Offset
	if s < 0 || e > len(src) || s >= e {
		return ""
	}
	return string(src[s:e])
}

func cleanSignature(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}
