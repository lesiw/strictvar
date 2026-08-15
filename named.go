package strictvar

import (
	"go/ast"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/analysis"
)

// cand is a function-scoped zero-value declaration whose type matches exactly
// one unnamed result position and which is returned in that position.
type cand struct {
	gd  *ast.GenDecl
	vs  *ast.ValueSpec
	obj types.Object
	idx int
}

// checkNamedReturns reports a zero-value declaration at the top of a function
// body whose type matches exactly one unnamed result and which is returned in
// that position. The declaration belongs in the signature as a named return.
//
// The fix is only offered for a standalone, uncommented declaration in a
// single-result function: naming one result of a multi-result function
// requires naming them all, and a block spec's deletion is not expressible as
// a line removal.
func (f *file) checkNamedReturns() {
	ast.Inspect(f.syntax, func(n ast.Node) bool {
		var (
			ft   *ast.FuncType
			body *ast.BlockStmt
		)
		switch node := n.(type) {
		case *ast.FuncDecl:
			ft, body = node.Type, node.Body
		case *ast.FuncLit:
			ft, body = node.Type, node.Body
		default:
			return true
		}
		if body != nil {
			f.checkFuncReturns(ft, body)
		}
		return true
	})
}

func (f *file) checkFuncReturns(ft *ast.FuncType, body *ast.BlockStmt) {
	if ft.Results == nil || len(ft.Results.List) == 0 {
		return
	}
	for _, field := range ft.Results.List {
		if len(field.Names) > 0 {
			return
		}
	}
	var cands []cand
	for _, stmt := range body.List {
		ds, ok := stmt.(*ast.DeclStmt)
		if !ok {
			continue
		}
		gd, ok := ds.Decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || len(vs.Values) != 0 || vs.Type == nil {
				continue
			}
			if len(vs.Names) != 1 || vs.Names[0].Name == "_" {
				continue
			}
			if idx, ok := f.returnCand(ft, body, gd, vs); ok {
				cands = append(cands, cand{gd: gd, vs: vs,
					obj: f.pass.TypesInfo.Defs[vs.Names[0]], idx: idx})
			}
		}
	}
	// A zero-value declaration belongs in the signature only when every
	// result position is produced by such a declaration. Naming one
	// result of a multi-result function requires naming them all, and if
	// any result is produced by an expression (a literal, a method call)
	// rather than a declared zero value, there is nothing to name it for.
	covered := make(map[int]struct{})
	for _, c := range cands {
		covered[c.idx] = struct{}{}
	}
	if len(covered) != len(ft.Results.List) {
		return
	}
	f.reportNamedReturn(ft, cands)
}

// reportNamedReturn emits a single report for the function when every result
// position is produced by a needless-declared zero-value variable. The fix
// renames the results and removes the declarations; it is offered only for a
// standalone single-result declaration, since naming one result of a
// multi-result function requires naming them all.
func (f *file) reportNamedReturn(ft *ast.FuncType, cands []cand) {
	if len(ft.Results.List) == 1 {
		f.reportNamedReturnSingle(ft, cands[0])
		return
	}
	f.report(ft.Results.Pos(), ft.Results.End(),
		"function-scoped zero values can be named returns",
		"Name the return values", nil,
	)
}

func (f *file) reportNamedReturnSingle(ft *ast.FuncType, c cand) {
	var (
		name         = c.vs.Names[0]
		pos, end, ok = f.deleteLineSpan(c.gd)
		fixable      = ok && len(ft.Results.List) == 1 &&
			c.gd.Lparen == token.NoPos && c.gd.Doc == nil
		edits []analysis.TextEdit
	)
	if fixable {
		field := ft.Results.List[0]
		edits = f.ownedEdits(c.gd, []analysis.TextEdit{{
			Pos: field.Pos(),
			End: field.End(),
			NewText: []byte("(" + name.Name + " " +
				f.text(field.Pos(), field.End()) + ")",
			),
		}, {Pos: pos, End: end, NewText: nil}})
	}
	f.report(ft.Results.Pos(), ft.Results.End(),
		"function-scoped zero value "+name.Name+" can be a named return",
		"Name the return value", edits,
	)
}

// returnCand reports idx and ok=true when the zero-value declaration vs
// matches exactly one result position of ft and is returned in that position.
func (f *file) returnCand(ft *ast.FuncType, body *ast.BlockStmt, gd *ast.GenDecl, vs *ast.ValueSpec) (idx int, ok bool) {
	var (
		info = f.pass.TypesInfo
		name = vs.Names[0]
		obj  = info.Defs[name]
	)
	if obj == nil {
		return 0, false
	}
	idx = -1
	for i, field := range ft.Results.List {
		rt := info.TypeOf(field.Type)
		if rt == nil || !types.Identical(rt, obj.Type()) {
			continue
		}
		if idx >= 0 {
			return 0, false // ambiguous: matches more than one result
		}
		idx = i
	}
	if idx < 0 {
		return 0, false
	}
	if !f.returnedAt(body, obj, idx) {
		return 0, false
	}
	if f.capturedByFuncLit(body, obj) {
		return 0, false
	}
	if f.overwrittenFirst(body, gd, obj) {
		return 0, false
	}
	return idx, true
}

// overwrittenFirst reports whether the first statement using obj after its
// declaration assigns the whole variable. Such a variable never carries its
// zero value, so it belongs to a := declaration, not a named return.
func (f *file) overwrittenFirst(body *ast.BlockStmt, gd *ast.GenDecl, obj types.Object) bool {
	info := f.pass.TypesInfo
	for _, stmt := range body.List {
		if stmt.Pos() < gd.End() || !mentions(info, stmt, obj) {
			continue
		}
		assign, ok := stmt.(*ast.AssignStmt)
		if !ok || assign.Tok != token.ASSIGN || len(assign.Lhs) != 1 {
			return false
		}
		id, ok := assign.Lhs[0].(*ast.Ident)
		if !ok || info.Uses[id] != obj {
			return false
		}
		return !mentions(info, assign.Rhs[0], obj)
	}
	return false
}

// returnedAt reports whether obj appears as result index idx of a return
// statement in body.
func (f *file) returnedAt(body *ast.BlockStmt, obj types.Object, idx int) bool {
	var (
		found bool
		info  = f.pass.TypesInfo
	)
	ast.Inspect(body, func(n ast.Node) bool {
		if found {
			return false
		}
		ret, ok := n.(*ast.ReturnStmt)
		if !ok || idx >= len(ret.Results) {
			return true
		}
		id, ok := ret.Results[idx].(*ast.Ident)
		if ok && info.Uses[id] == obj {
			found = true
		}
		return true
	})
	return found
}

// capturedByFuncLit reports whether obj is referenced inside a function
// literal in body. Naming the result changes what a deferred closure's writes
// mean, so captured variables are left alone.
func (f *file) capturedByFuncLit(body *ast.BlockStmt, obj types.Object) bool {
	var (
		found bool
		info  = f.pass.TypesInfo
	)
	ast.Inspect(body, func(n ast.Node) bool {
		if found {
			return false
		}
		lit, ok := n.(*ast.FuncLit)
		if !ok {
			return true
		}
		ast.Inspect(lit, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok && info.Uses[id] == obj {
				found = true
			}
			return !found
		})
		return false
	})
	return found
}
