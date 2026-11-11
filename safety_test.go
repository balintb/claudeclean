package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

var forbidden = map[string]map[string]bool{
	"os":      {"Remove": true, "RemoveAll": true, "Truncate": true, "Rename": true},
	"syscall": {"Unlink": true, "Unlinkat": true, "Rmdir": true, "Rename": true, "Renameat": true, "Truncate": true, "Ftruncate": true},
	"unix":    {"Unlink": true, "Unlinkat": true, "Rmdir": true, "Rename": true, "Renameat": true, "Renameat2": true, "Truncate": true, "Ftruncate": true},
}

// Shipped code may only move things to the Trash.
func TestShippedCodeNeverDeletes(t *testing.T) {
	fset := token.NewFileSet()
	checked := 0
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (strings.HasPrefix(d.Name(), ".") && path != ".") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if strings.HasPrefix(filepath.ToSlash(path), "internal/fixture/") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		checked++
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.SelectorExpr:
				pkg, ok := n.X.(*ast.Ident)
				if !ok {
					return true
				}
				if forbidden[pkg.Name][n.Sel.Name] {
					t.Errorf("%s: forbidden call %s.%s", fset.Position(n.Pos()), pkg.Name, n.Sel.Name)
				}
				if n.Sel.Name == "O_TRUNC" {
					t.Errorf("%s: O_TRUNC is forbidden", fset.Position(n.Pos()))
				}
				if pkg.Name == "os" && (n.Sel.Name == "Create" || n.Sel.Name == "WriteFile") {
					t.Errorf("%s: os.%s can overwrite files", fset.Position(n.Pos()), n.Sel.Name)
				}
			case *ast.BasicLit:
				if n.Kind != token.STRING {
					return true
				}
				s, err := strconv.Unquote(n.Value)
				if err != nil {
					return true
				}
				base := filepath.Base(strings.Fields(s + " x")[0])
				if base == "rm" || base == "rmdir" || base == "unlink" || base == "shred" || base == "srm" {
					t.Errorf("%s: string %q looks like a deletion command", fset.Position(n.Pos()), s)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 10 {
		t.Fatalf("only %d files checked; is the walk broken?", checked)
	}
}
