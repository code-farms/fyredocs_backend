package response

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// canonicalCodes parses codes.go and returns each constant's value → name, so
// the guard below never needs its own (driftable) list.
func canonicalCodes(t *testing.T) map[string]string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "codes.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	codes := map[string]string{}
	ast.Inspect(f, func(n ast.Node) bool {
		vs, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		for i, name := range vs.Names {
			if i < len(vs.Values) {
				if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
					if v, err := strconv.Unquote(lit.Value); err == nil {
						codes[v] = name.Name
					}
				}
			}
		}
		return true
	})
	if len(codes) < 10 {
		t.Fatalf("parsed only %d codes from codes.go — the parser no longer matches its shape", len(codes))
	}
	return codes
}

// TestNoHardCodedCanonicalErrorCodes keeps the error-code literal migration
// done: any response.* call (or WriteErr) in non-test code that passes a string
// literal equal to a canonical code's value must use the constant instead —
// one name per wire value, so a future rename is one edit, not a grep.
// Domain-specific codes without a constant (INVALID_NAME, …) are untouched.
func TestNoHardCodedCanonicalErrorCodes(t *testing.T) {
	codes := canonicalCodes(t)
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.work")); err != nil {
		t.Fatalf("expected the workspace root at %s", root)
	}
	fset := token.NewFileSet()
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == "vendor" || name == "node_modules" || strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") ||
			path == filepath.Join(root, "shared", "response", "codes.go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return nil // generated or broken files are other tests' business
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "response" {
				return true
			}
			for _, arg := range call.Args {
				lit, ok := arg.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				if v, err := strconv.Unquote(lit.Value); err == nil {
					if name, canonical := codes[v]; canonical {
						rel, _ := filepath.Rel(root, path)
						t.Errorf("%s:%d: response.%s passes %q — use response.%s", rel, fset.Position(lit.Pos()).Line, sel.Sel.Name, v, name)
					}
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
