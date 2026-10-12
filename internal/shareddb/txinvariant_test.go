package shareddb

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// gormMethods is the set of GORM chain methods that must run on the transaction
// handle (tx) inside a db.Transaction closure. Calling any of them on the
// shared *gorm.DB handle would block forever on the single pooled connection.
var gormMethods = map[string]bool{
	"Model": true, "Where": true, "Or": true, "Not": true, "Select": true,
	"Omit": true, "Joins": true, "InnerJoins": true, "LeftJoins": true,
	"Preload": true, "Scopes": true, "Table": true, "Unscoped": true,
	"Distinct": true, "Group": true, "Having": true, "Order": true,
	"Limit": true, "Offset": true, "Clauses": true, "Raw": true, "Exec": true,
	"Create": true, "Save": true, "Updates": true, "Update": true,
	"UpdateColumn": true, "Delete": true, "Find": true, "First": true,
	"Last": true, "Take": true, "Pluck": true, "Scan": true, "Count": true,
	"Row": true, "Rows": true, "AutoMigrate": true, "Migrator": true,
	"Association": true, "WithContext": true, "Session": true, "Attrs": true,
	"Assign": true, "MapColumns": true, "Begin": true, "Commit": true,
	"Rollback": true, "Transaction": true,
}

// TestTransactionClosuresThreadTx is the enforcement half of the one-connection
// pool: Open caps the pool at one connection, so a query on the shared handle
// from inside a db.Transaction closure would block the whole process. This
// scans every db.Transaction closure in internal/ and fails if a GORM method is
// called on anything other than the closure's own tx parameter.
func TestTransactionClosuresThreadTx(t *testing.T) {
	root := repoRoot(t)
	fset := token.NewFileSet()
	var violations []string

	walkErr := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		violations = append(violations, scanTransactionClosures(fset, path, file)...)
		return nil
	})
	if walkErr != nil {
		t.Fatalf("scan internal/: %v", walkErr)
	}

	if len(violations) > 0 {
		t.Fatalf("shared DB handle used inside a Transaction closure; thread the tx parameter instead:\n%s",
			strings.Join(violations, "\n"))
	}
}

func scanTransactionClosures(fset *token.FileSet, path string, file *ast.File) []string {
	var violations []string
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Transaction" || len(call.Args) == 0 {
			return true
		}
		lit, ok := call.Args[0].(*ast.FuncLit)
		if !ok || lit.Type.Params == nil || len(lit.Type.Params.List) != 1 {
			return true
		}
		txName := ""
		if len(lit.Type.Params.List[0].Names) > 0 {
			txName = lit.Type.Params.List[0].Names[0].Name
		}
		if txName == "" {
			return true
		}
		ast.Inspect(lit.Body, func(m ast.Node) bool {
			call, ok := m.(*ast.CallExpr)
			if !ok {
				return true
			}
			s, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || !gormMethods[s.Sel.Name] {
				return true
			}
			root := rootIdent(s.X)
			if root != "" && root != txName {
				violations = append(violations, fmt.Sprintf("  %s: %s.%s (want %s)", fset.Position(s.Pos()), root, s.Sel.Name, txName))
			}
			return true
		})
		return true
	})
	return violations
}

// rootIdent walks a selector/call chain down to the base identifier, so
// m.getDB().Model(...) and m.db.Model(...) both report "m".
func rootIdent(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return rootIdent(v.X)
	case *ast.CallExpr:
		return rootIdent(v.Fun)
	case *ast.IndexExpr:
		return rootIdent(v.X)
	case *ast.ParenExpr:
		return rootIdent(v.X)
	case *ast.StarExpr:
		return rootIdent(v.X)
	default:
		return ""
	}
}

// TestScanTransactionClosures proves the scanner flags a shared-handle use and
// passes a properly threaded closure, so the guard above cannot pass vacuously.
func TestScanTransactionClosures(t *testing.T) {
	const src = `package p

import "gorm.io/gorm"

type M struct{ db *gorm.DB }

func (m *M) bad() error {
	return m.db.Transaction(func(tx *gorm.DB) error {
		return m.db.Model(nil).Error
	})
}

func (m *M) good() error {
	return m.db.Transaction(func(tx *gorm.DB) error {
		return tx.Model(nil).Error
	})
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "snippet.go", src, 0)
	if err != nil {
		t.Fatalf("parse snippet: %v", err)
	}
	violations := scanTransactionClosures(fset, "snippet.go", file)
	if len(violations) != 1 {
		t.Fatalf("violations = %d (%v), want exactly the bad closure's m.db use", len(violations), violations)
	}
	if !strings.Contains(violations[0], "m.Model") || !strings.Contains(violations[0], "want tx") {
		t.Errorf("violation = %q, want it to point at the shared-handle m.Model use", violations[0])
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test directory")
		}
		dir = parent
	}
}
