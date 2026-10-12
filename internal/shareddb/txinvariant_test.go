package shareddb

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
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
// scans every db.Transaction closure in internal/ and fails if the closure
// either calls a GORM chain method on anything but its own tx parameter or
// passes the handle the transaction was opened on (d, db, m.db, ...) as an
// argument to another call. A helper that ignores its arguments and closes over
// the shared handle internally is out of scope for an AST-only scan; the
// direct and pass-through shapes are covered.
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
	seen := map[string]bool{}
	report := func(pos token.Pos, msg string) {
		key := fset.Position(pos).String()
		if seen[key] {
			return
		}
		seen[key] = true
		violations = append(violations, "  "+key+": "+msg)
	}

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
		// The shared handle the transaction was opened on (d, db, m.db, ...).
		// Any use of it inside the closure competes for the one connection.
		handle := exprString(fset, sel.X)
		ast.Inspect(lit.Body, func(m ast.Node) bool {
			call, ok := m.(*ast.CallExpr)
			if !ok {
				return true
			}
			// A GORM chain method on anything but tx is the classic mistake.
			if s, ok := call.Fun.(*ast.SelectorExpr); ok && gormMethods[s.Sel.Name] {
				if root := rootIdent(s.X); root != "" && root != txName {
					report(s.Pos(), fmt.Sprintf("%s.%s (want %s)", root, s.Sel.Name, txName))
				}
			}
			// Passing the shared handle to a helper is the same footgun in a
			// shape the receiver check cannot see.
			for _, arg := range call.Args {
				if handle != "" && exprString(fset, arg) == handle {
					report(arg.Pos(), fmt.Sprintf("shared handle %s used inside Transaction; thread %s", handle, txName))
				}
			}
			return true
		})
		return true
	})
	return violations
}

// exprString renders an expression back to source so the shared handle can be
// matched even when it is spelled as a field access (m.db) rather than a local.
func exprString(fset *token.FileSet, e ast.Expr) string {
	var sb strings.Builder
	if err := printer.Fprint(&sb, fset, e); err != nil {
		return ""
	}
	return sb.String()
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

// TestScanTransactionClosures proves the scanner flags both shapes of
// shared-handle use and passes a properly threaded closure, so the guard above
// cannot pass vacuously.
func TestScanTransactionClosures(t *testing.T) {
	const src = `package p

import "gorm.io/gorm"

type M struct{ db *gorm.DB }

func doThing(*gorm.DB) error { return nil }

func (m *M) bad() error {
	return m.db.Transaction(func(tx *gorm.DB) error {
		return m.db.Model(nil).Error
	})
}

func (m *M) badHelper() error {
	return m.db.Transaction(func(tx *gorm.DB) error {
		return doThing(m.db)
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
	if len(violations) != 2 {
		t.Fatalf("violations = %d (%v), want exactly the two bad closures' handle uses", len(violations), violations)
	}
	joined := strings.Join(violations, "\n")
	if !strings.Contains(joined, "m.Model") || !strings.Contains(joined, "want tx") {
		t.Errorf("violations = %q, want one pointing at the shared-handle m.Model use", joined)
	}
	if !strings.Contains(joined, "shared handle m.db") {
		t.Errorf("violations = %q, want one for passing the shared handle to a helper", joined)
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
