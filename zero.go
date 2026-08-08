package strictvar

import (
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/analysis"
)

// checkZeroDecls reports variables declared with a zero value in a form other
// than var x T. Assignments in for, if, and switch init statements never
// appear in a statement list, so the positions where a var declaration is
// impossible are exempt by construction.
func (f *file) checkZeroDecls() {
	f.stmtLists(func(list []ast.Stmt) {
		for _, stmt := range list {
			assign, ok := stmt.(*ast.AssignStmt)
			if !ok || assign.Tok != token.DEFINE {
				continue
			}
			if len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
				continue
			}
			name, ok := assign.Lhs[0].(*ast.Ident)
			if !ok || name.Name == "_" {
				continue
			}
			f.zeroAssign(assign, name)
		}
	})
	ast.Inspect(f.syntax, func(n ast.Node) bool {
		decl, ok := n.(*ast.GenDecl)
		if !ok || decl.Tok != token.VAR {
			return true
		}
		for _, spec := range decl.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || len(vs.Names) != 1 || len(vs.Values) != 1 {
				continue
			}
			if vs.Type == nil {
				f.zeroVarSpec(decl, vs)
			} else {
				f.typedZeroSpec(decl, vs)
				f.checkTypeElision(decl, vs)
			}
		}
		return true
	})
}

// zeroAssign reports x := zero and suggests the var form.
func (f *file) zeroAssign(assign *ast.AssignStmt, name *ast.Ident) {
	repl, ok := f.zeroRewrite(name.Name, assign.Rhs[0])
	if !ok {
		return
	}
	var edits []analysis.TextEdit
	if !f.hasComment(assign.Pos(), assign.End()) {
		edits = []analysis.TextEdit{{
			Pos:     assign.Pos(),
			End:     assign.End(),
			NewText: []byte(repl),
		}}
	}
	f.report(assign.Pos(), assign.End(),
		"zero value can be declared with var: "+repl,
		"Declare with var", edits,
	)
}

// zeroVarSpec reports var x = zero and suggests dropping the value.
func (f *file) zeroVarSpec(decl *ast.GenDecl, vs *ast.ValueSpec) {
	repl, ok := f.zeroRewrite(vs.Names[0].Name, vs.Values[0])
	if !ok {
		return
	}
	pos, end := vs.Pos(), vs.End()
	text := repl[len("var "):]
	if !decl.Lparen.IsValid() {
		pos, end = decl.Pos(), decl.End()
		text = repl
	}
	var edits []analysis.TextEdit
	if !f.hasComment(pos, end) {
		edits = f.ownedEdits(decl, []analysis.TextEdit{{
			Pos:     pos,
			End:     end,
			NewText: []byte(text),
		}})
	}
	f.report(vs.Pos(), vs.End(),
		"zero value can be declared with var: "+repl,
		"Declare with var", edits,
	)
}

// typedZeroSpec reports var x T = zero and suggests dropping the value.
func (f *file) typedZeroSpec(decl *ast.GenDecl, vs *ast.ValueSpec) {
	typ := f.pass.TypesInfo.TypeOf(vs.Type)
	if typ == nil || !f.zeroForType(vs.Values[0], typ) {
		return
	}
	pos, end := vs.Pos(), vs.End()
	repl := vs.Names[0].Name + " " + f.text(vs.Type.Pos(), vs.Type.End())
	if !decl.Lparen.IsValid() {
		pos, end = decl.Pos(), decl.End()
		repl = "var " + repl
	}
	var edits []analysis.TextEdit
	if !f.hasComment(pos, end) {
		edits = f.ownedEdits(decl, []analysis.TextEdit{{
			Pos:     pos,
			End:     end,
			NewText: []byte(repl),
		}})
	}
	f.report(vs.Pos(), vs.End(),
		"zero value can be declared without a value: "+repl,
		"Drop the value", edits,
	)
}

// zeroRewrite returns the var declaration replacing a zero-value assignment to
// name, or ok=false when rhs is not a rewritable zero value.
func (f *file) zeroRewrite(name string, rhs ast.Expr) (string, bool) {
	info := f.pass.TypesInfo
	switch e := rhs.(type) {
	case *ast.CompositeLit:
		// T{}: var x T. Empty map, slice, and chan literals are not
		// the zero value.
		if len(e.Elts) != 0 || e.Type == nil {
			return "", false
		}
		if f.hasComment(e.Lbrace, e.Rbrace) {
			return "", false
		}
		switch info.TypeOf(e).Underlying().(type) {
		case *types.Struct, *types.Array:
		default:
			return "", false
		}
		return "var " + name + " " + f.text(e.Type.Pos(), e.Type.End()), true
	case *ast.BasicLit:
		if !constZero(info.Types[rhs].Value) {
			return "", false
		}
		switch e.Kind {
		case token.INT, token.FLOAT, token.IMAG, token.STRING:
			return "var " + name + " " +
				types.Default(info.TypeOf(e)).String(), true
		}
		return "", false
	case *ast.Ident:
		if info.Uses[e] == types.Universe.Lookup("false") {
			return "var " + name + " bool", true
		}
		return "", false
	case *ast.CallExpr:
		// T(0) and T(nil): var x T.
		if len(e.Args) != 1 {
			return "", false
		}
		tv, ok := info.Types[e.Fun]
		if !ok || !tv.IsType() {
			return "", false
		}
		if types.IsInterface(tv.Type) {
			// error(nil) is the interface zero value.
			if !isNil(e.Args[0], info) {
				return "", false
			}
		} else {
			zero := constZero(info.Types[e].Value) || isNil(e.Args[0], info)
			if !zero {
				return "", false
			}
		}
		return "var " + name + " " + f.text(e.Fun.Pos(), e.Fun.End()), true
	}
	return "", false
}

// zeroForType reports whether value is the zero value of typ. Used for
// declarations that carry an explicit type, where an untyped zero is converted
// before assignment.
func (f *file) zeroForType(value ast.Expr, typ types.Type) bool {
	info := f.pass.TypesInfo
	if isNil(value, info) {
		return nilable(typ)
	}
	if types.IsInterface(typ) {
		// A concrete zero boxed in an interface is not the interface
		// zero value.
		return false
	}
	if namedConstRef(info, value) {
		// A named constant is not a zero value: the name is the
		// point of the initializer.
		return false
	}
	if constZero(info.Types[value].Value) {
		return true
	}
	if lit, ok := value.(*ast.CompositeLit); ok && len(lit.Elts) == 0 {
		if f.hasComment(lit.Lbrace, lit.Rbrace) {
			return false
		}
		switch info.TypeOf(lit).Underlying().(type) {
		case *types.Struct, *types.Array:
			return types.Identical(info.TypeOf(lit), typ)
		}
	}
	return false
}

func constZero(v constant.Value) bool {
	if v == nil {
		return false
	}
	switch v.Kind() {
	case constant.Bool:
		return !constant.BoolVal(v)
	case constant.Int, constant.Float, constant.Complex:
		return constant.Sign(v) == 0
	case constant.String:
		return constant.StringVal(v) == ""
	}
	return false
}

// namedConstRef reports whether e is a reference to a named constant. The
// predeclared false is the zero literal, not a name.
func namedConstRef(info *types.Info, e ast.Expr) bool {
	var id *ast.Ident
	switch n := e.(type) {
	case *ast.Ident:
		id = n
	case *ast.SelectorExpr:
		id = n.Sel
	default:
		return false
	}
	if info.Uses[id] == types.Universe.Lookup("false") {
		return false
	}
	_, ok := info.Uses[id].(*types.Const)
	return ok
}

func isNil(e ast.Expr, info *types.Info) bool {
	id, ok := e.(*ast.Ident)
	return ok && info.Uses[id] == types.Universe.Lookup("nil")
}

func nilable(typ types.Type) bool {
	switch t := typ.Underlying().(type) {
	case *types.Slice, *types.Map, *types.Chan,
		*types.Pointer, *types.Interface, *types.Signature:
		return true
	case *types.Basic:
		return t.Kind() == types.UnsafePointer
	}
	return false
}

// checkNewExprs reports &T{} expressions that can be new(T). Only types whose
// composite literal is the zero value qualify: structs and arrays. An empty
// map or slice literal is non-nil, so its address is not interchangeable with
// new. Parentheses around the &T{} come off with it: new(T) needs none.
func (f *file) checkNewExprs() {
	consumed := make(map[*ast.UnaryExpr]struct{})
	ast.Inspect(f.syntax, func(n ast.Node) bool {
		var (
			un   *ast.UnaryExpr
			span ast.Expr
		)
		switch node := n.(type) {
		case *ast.ParenExpr:
			u, ok := node.X.(*ast.UnaryExpr)
			if !ok {
				return true
			}
			un, span = u, node
		case *ast.UnaryExpr:
			if _, ok := consumed[node]; ok {
				return true
			}
			un, span = node, node
		default:
			return true
		}
		if un.Op != token.AND {
			return true
		}
		lit, ok := un.X.(*ast.CompositeLit)
		if !ok || len(lit.Elts) != 0 || lit.Type == nil {
			return true
		}
		switch f.pass.TypesInfo.TypeOf(lit).Underlying().(type) {
		case *types.Struct, *types.Array:
		default:
			return true
		}
		if f.shadowed("new", un.Pos()) {
			return true
		}
		if span != un {
			if f.hasComment(span.Pos(), span.End()) {
				// A comment inside the parentheses stays, and only the
				// &T{} itself rewrites.
				span = un
			} else {
				consumed[un] = struct{}{}
			}
		}
		var edits []analysis.TextEdit
		typ := f.text(lit.Type.Pos(), lit.Type.End())
		repl := "new(" + typ + ")"
		if !f.hasComment(un.Pos(), un.End()) && !f.ownedSpan(un.Pos()) {
			edits = []analysis.TextEdit{{
				Pos:     span.Pos(),
				End:     span.End(),
				NewText: []byte(repl),
			}}
		}
		f.report(span.Pos(), span.End(),
			"&"+typ+"{} can be "+repl,
			"Replace with new", edits,
		)
		return true
	})
}

// checkTypeElision reports a block spec whose explicit type matches what its
// value infers: year int = -1 can be year = -1.
func (f *file) checkTypeElision(decl *ast.GenDecl, vs *ast.ValueSpec) {
	if !decl.Lparen.IsValid() {
		return
	}
	typ := f.pass.TypesInfo.TypeOf(vs.Type)
	if typ == nil || f.zeroForType(vs.Values[0], typ) {
		return
	}
	if !f.typeFaithful(vs.Values[0], typ) {
		return
	}
	edits := f.ownedEdits(decl, []analysis.TextEdit{{
		Pos:     vs.Names[0].End(),
		End:     vs.Type.End(),
		NewText: nil,
	}})
	f.report(vs.Pos(), vs.End(),
		"redundant type can be elided: "+vs.Names[0].Name+" = "+
			f.text(vs.Values[0].Pos(), vs.Values[0].End()),
		"Elide the type", edits)
}

// ownedEdits withholds a fix from a declaration another rule is rewriting: two
// fixes editing the same span discard each other, so the spec-level rewrite
// waits for the following pass.
func (f *file) ownedEdits(decl *ast.GenDecl, edits []analysis.TextEdit) []analysis.TextEdit {
	if _, claimed := f.owned[decl]; claimed {
		return nil
	}
	return edits
}
