package strictvar

import (
	"go/ast"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/analysis"
)

// checkMakeDecls reports a zero-value declaration whose first use, in the same
// statement list, overwrites the whole variable. The declaration and the
// assignment belong together: x := v. When the declaration lives in a
// preamble var block, or is about to be grouped into one, and the assignment
// immediately follows the block, the value folds into its spec instead: x =
// v. A block outside the preamble dissolves first, and the standalone form of
// this check takes over on a later pass.
func (f *file) checkMakeDecls() {
	f.stmtLists(f.checkListMakeDecls)
}

func (f *file) checkListMakeDecls(list []ast.Stmt) {
	for i, stmt := range list {
		ds, ok := stmt.(*ast.DeclStmt)
		if !ok {
			continue
		}
		gd, ok := ds.Decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		if gd.Lparen.IsValid() {
			if preambleAnchored(list, i) {
				f.checkBlockAssign(list, i, gd)
			}
			continue
		}
		if _, claimed := f.owned[gd]; claimed {
			// A grouping rule is rewriting this declaration into a
			// block or a combined spec. The rewritten form of this
			// check takes over on the next pass.
			continue
		}
		vs, ok := zeroSpec(gd)
		if !ok || len(vs.Names) != 1 {
			continue
		}
		obj := f.pass.TypesInfo.Defs[vs.Names[0]]
		if obj == nil {
			continue
		}
		f.checkFirstUse(list[i+1:], gd, obj)
	}
}

// checkFirstUse scans forward for the first statement mentioning obj. If that
// statement overwrites the whole variable at the same list depth with a value
// := would type identically, the declaration is reported.
func (f *file) checkFirstUse(rest []ast.Stmt, gd *ast.GenDecl, obj types.Object) {
	info := f.pass.TypesInfo
	for _, stmt := range rest {
		if !mentions(info, stmt, obj) {
			continue
		}
		assign, ok := stmt.(*ast.AssignStmt)
		if !ok || assign.Tok != token.ASSIGN {
			return
		}
		if len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
			return
		}
		id, ok := assign.Lhs[0].(*ast.Ident)
		if !ok || info.Uses[id] != obj {
			return
		}
		rhs := assign.Rhs[0]
		if mentions(info, rhs, obj) || !f.typeFaithful(rhs, obj.Type()) {
			return
		}
		var edits []analysis.TextEdit
		pos, end, ok := f.deleteLineSpan(gd)
		if ok && gd.Doc == nil {
			edits = []analysis.TextEdit{{Pos: pos, End: end, NewText: nil}, {
				Pos:     assign.TokPos,
				End:     assign.TokPos + 1,
				NewText: []byte(":="),
			}}
		}
		f.report(assign.Pos(), assign.End(), obj.Name()+
			" can be declared where it is "+assignKind(info, rhs),
			"Fold the declaration into the assignment", edits,
		)
		return
	}
}

// checkBlockAssign reports a zero spec in a var block whose whole variable is
// overwritten by the statement immediately after the block. The value folds
// into the spec: moving it any further up could not reorder anything, because
// nothing runs in between.
func (f *file) checkBlockAssign(list []ast.Stmt, i int, gd *ast.GenDecl) {
	if i+1 >= len(list) {
		return
	}
	info := f.pass.TypesInfo
	assign, ok := list[i+1].(*ast.AssignStmt)
	if !ok || assign.Tok != token.ASSIGN {
		return
	}
	if len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
		return
	}
	id, ok := assign.Lhs[0].(*ast.Ident)
	if !ok {
		return
	}
	rhs := assign.Rhs[0]
	for i, spec := range gd.Specs {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok || len(vs.Values) != 0 || vs.Type == nil {
			continue
		}
		if len(vs.Names) != 1 || info.Defs[vs.Names[0]] == nil {
			continue
		}
		obj := info.Defs[vs.Names[0]]
		if info.Uses[id] != obj || mentions(info, rhs, obj) {
			continue
		}
		if !f.typeFaithful(rhs, obj.Type()) {
			continue
		}
		// The folded value runs at the spec's position: a name a
		// later spec declares is not yet in scope there.
		if f.mentionsLater(gd, i, rhs) {
			continue
		}
		var edits []analysis.TextEdit
		pos, end, ok := f.deleteLineSpan(assign)
		if ok {
			edits = f.ownedEdits(gd, []analysis.TextEdit{{
				Pos: vs.Pos(),
				End: vs.End(),
				NewText: []byte(vs.Names[0].Name + " = " +
					f.text(rhs.Pos(), rhs.End()),
				),
			}, {Pos: pos, End: end, NewText: nil}})
		}
		f.report(vs.Pos(), vs.End(), obj.Name()+" can be "+
			assignKind(info, rhs)+" in its declaration",
			"Fold the assignment into the declaration", edits,
		)
		return
	}
}

// typeFaithful reports whether x := rhs gives x the type its var declaration
// did, by re-inferring the value in isolation: go/types records converted
// constant types, not what := would infer.
func (f *file) typeFaithful(rhs ast.Expr, typ types.Type) bool {
	info := &types.Info{Types: make(map[ast.Expr]types.TypeAndValue)}
	err := types.CheckExpr(f.pass.Fset, f.pass.Pkg, rhs.Pos(), rhs, info)
	if err != nil {
		return false
	}
	tv, ok := info.Types[rhs]
	return ok && tv.Type != nil && types.Identical(types.Default(tv.Type), typ)
}

// assignKind names the assignment for diagnostics: a make call is made,
// anything else is assigned.
func assignKind(info *types.Info, rhs ast.Expr) string {
	if _, ok := makeCall(info, rhs); ok {
		return "made"
	}
	return "assigned"
}

// mentionsLater reports whether expr reads a name declared by a spec at or
// after index i of gd.
func (f *file) mentionsLater(gd *ast.GenDecl, i int, expr ast.Expr) bool {
	info := f.pass.TypesInfo
	for _, spec := range gd.Specs[i:] {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		for _, name := range vs.Names {
			obj := info.Defs[name]
			if obj != nil && mentions(info, expr, obj) {
				return true
			}
		}
	}
	return false
}

// makeCall returns expr as a call to the builtin make.
func makeCall(info *types.Info, expr ast.Expr) (*ast.CallExpr, bool) {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return nil, false
	}
	fn, ok := call.Fun.(*ast.Ident)
	if !ok || info.Uses[fn] != types.Universe.Lookup("make") {
		return nil, false
	}
	return call, true
}

// mentions reports whether node references obj.
func mentions(info *types.Info, node ast.Node, obj types.Object) bool {
	var found bool
	ast.Inspect(node, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && info.Uses[id] == obj {
			found = true
		}
		return !found
	})
	return found
}
