package portablesetup

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// Prevent moving a UAP membership read back before the host coordinator
// lease. This source-order guard complements the real competing-install
// RemoveGroup E2E; it does not claim to prove independent UAP recovery locks.
func TestRemoveGroupReadsMembershipAfterAcquiringCoordinatorLease(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "materializer_group.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var lease, membership token.Pos
	for _, declaration := range file.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "RemoveGroup" {
			continue
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if selector.Sel.Name == "AcquireCoordinatorLease" && lease == token.NoPos {
				lease = call.Pos()
			}
			if selector.Sel.Name == "Load" && membership == token.NoPos {
				if store, ok := selector.X.(*ast.SelectorExpr); ok && store.Sel.Name == "Store" {
					membership = call.Pos()
				}
			}
			return true
		})
	}
	if lease == token.NoPos || membership == token.NoPos {
		t.Fatal("RemoveGroup lease or explicit membership-read call was removed; review the concurrency contract")
	}
	if membership <= lease {
		t.Fatal("RemoveGroup reads UAP membership before acquiring the coordinator lease")
	}
}
