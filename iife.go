package strictvar

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strconv"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// checkIIFE reports package-level variables initialized by an immediately
// invoked function literal. The Go convention is var x T plus func init().
// The IIFE is a JavaScript idiom. A literal whose body is a lone return is
// a pointless wrapper and unwraps in place; one with real statements becomes
// an init function. A variable that a sibling initializer reads is left in
// place, since package variables initialize before init runs.
func (f *file) checkIIFE() {
	for _, decl := range f.syntax.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || len(vs.Names) != 1 || len(vs.Values) != 1 {
				continue
			}
			if vs.Type == nil && vs.Names[0].Name != "_" {
				f.checkIIFEValue(gd, vs)
			}
		}
	}
}

func (f *file) checkIIFEValue(gd *ast.GenDecl, vs *ast.ValueSpec) {
	var (
		value    = vs.Values[0]
		call, ok = value.(*ast.CallExpr)
	)
	if !ok || len(call.Args) != 0 {
		return
	}
	lit, ok := call.Fun.(*ast.FuncLit)
	if !ok {
		return
	}
	edits, report := f.iifeEdits(gd, vs, lit)
	if !report {
		return
	}
	f.report(value.Pos(), value.End(),
		"package-level IIFE can be var plus func init",
		"Rewrite as var plus func init", edits,
	)
}

// initializerRef reports whether another package-level initializer expression
// reads the variable name declares.
func (f *file) initializerRef(name *ast.Ident) (found bool) {
	obj := f.pass.TypesInfo.Defs[name]
	for _, sf := range f.pass.Files {
		for _, decl := range sf.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok || len(vs.Names) == 0 {
					continue
				}
				if vs.Names[0] == name {
					continue
				}
				for _, value := range vs.Values {
					if mentions(f.pass.TypesInfo, value, obj) {
						found = true
					}
				}
			}
		}
	}
	return found
}

// rename replaces the identifier between pos and end with name.
type rename struct {
	pos, end token.Pos
	name     string
}

// captures reports whether obj hides the package variable at one of the
// returns the rewrite writes an assignment to. A field does not: a selector
// resolves through the type, and the assignment never sees it. A local whose
// block ends before the return is out of scope there, so renaming it would
// change code that was never in the way.
func captures(obj types.Object, rets []*ast.ReturnStmt) bool {
	switch o := obj.(type) {
	case *types.Var:
		if o.IsField() {
			return false
		}
	case *types.Const, *types.TypeName:
	default:
		return false
	}
	for _, ret := range rets {
		if obj.Parent().Contains(ret.Pos()) {
			return true
		}
	}
	return false
}

// renames returns the identifiers to rename when body is moved into an init
// function, and whether every mention of the name could be resolved. A local
// declared with the package variable's name would shadow the assignment the
// rewrite writes, so the local takes the lowest number the package does not
// use.
//
// A name that resolves to no object, such as the variable a type switch
// declares, leaves the rewrite incomplete. Nothing is renamed then, and the
// caller withholds the fix rather than write a self-assignment.
func (f *file) renames(name string, body *ast.BlockStmt, rets []*ast.ReturnStmt) ([]rename, bool) {
	type mention struct {
		id  *ast.Ident
		obj types.Object
	}
	var mentions []mention
	named := make(map[types.Object]string)
	chosen := make(map[string]struct{})
	var out []rename
	ok := true
	ast.Inspect(body, func(n ast.Node) bool {
		id, isIdent := n.(*ast.Ident)
		if !isIdent || id.Name != name {
			return true
		}
		obj := f.pass.TypesInfo.Defs[id]
		if obj == nil {
			obj = f.pass.TypesInfo.Uses[id]
		}
		if obj == nil {
			ok = false
			return false
		}
		if captures(obj, rets) {
			mentions = append(mentions, mention{id, obj})
		}
		return true
	})
	if !ok || len(mentions) == 0 {
		return nil, ok
	}
	for _, m := range mentions {
		fresh, seen := named[m.obj]
		if !seen {
			fresh = f.free(name, chosen)
			named[m.obj] = fresh
		}
		out = append(out, rename{m.id.Pos(), m.id.End(), fresh})
	}
	return out, true
}

// free returns name plus the lowest counter neither chosen nor used by the
// package.
func (f *file) free(name string, chosen map[string]struct{}) string {
	for i := 1; ; i++ {
		candidate := name + strconv.Itoa(i)
		if _, ok := chosen[candidate]; ok {
			continue
		}
		if f.taken(candidate) {
			continue
		}
		chosen[candidate] = struct{}{}
		return candidate
	}
}

// taken reports whether any file of the package uses name, field, method and
// label names included. It is deliberately wider than the scopes the rewrite
// writes into.
func (f *file) taken(name string) bool {
	for _, file := range f.pass.Files {
		var found bool
		ast.Inspect(file, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok && id.Name == name {
				found = true
			}
			return !found
		})
		if found {
			return true
		}
	}
	return false
}

// textRenamed returns the source from start to end with each rename applied.
func (f *file) textRenamed(start, end token.Pos, renames []rename) string {
	var (
		b   strings.Builder
		pos = start
	)
	for _, r := range renames {
		if r.pos < start || r.end > end {
			continue
		}
		b.WriteString(f.text(pos, r.pos))
		b.WriteString(r.name)
		pos = r.end
	}
	b.WriteString(f.text(pos, end))
	return b.String()
}

// iifeEdits unwraps a lone-return literal in place, and turns any other
// literal into var x T plus a func init holding the body, with each top-level
// return expr rewritten to x = expr.
//
// The second result says whether to report. A rewrite that cannot be
// expressed leaves it false: the fix would be wrong or would lose a comment,
// and a diagnostic without a fix is a demand the author cannot satisfy.
func (f *file) iifeEdits(gd *ast.GenDecl, vs *ast.ValueSpec, lit *ast.FuncLit) (edits []analysis.TextEdit, report bool) {
	value := vs.Values[0]
	if len(lit.Body.List) == 0 {
		return nil, true
	}
	if len(lit.Body.List) == 1 {
		var (
			ret, ok = lit.Body.List[0].(*ast.ReturnStmt)
			retOK   = ok && len(ret.Results) == 1 &&
				!f.hasComment(lit.Pos(), value.End())
		)
		if retOK {
			return []analysis.TextEdit{{
				Pos: value.Pos(),
				End: value.End(),
				NewText: []byte(f.text(
					ret.Results[0].Pos(), ret.Results[0].End(),
				)),
			}}, true
		}
	}
	if lit.Type.Results == nil || len(lit.Type.Results.List) != 1 {
		return nil, true
	}
	field := lit.Type.Results.List[0]
	if len(field.Names) != 0 {
		return nil, true
	}
	if f.hasComment(vs.Names[0].End(), lit.Body.Lbrace) {
		return nil, true
	}
	if f.initializerRef(vs.Names[0]) {
		// Package variables initialize before any init runs, so a
		// sibling initializer reading this variable would see the zero
		// value after the move.
		return nil, true
	}
	last := lit.Body.List[len(lit.Body.List)-1]
	var bad bool
	var rets []*ast.ReturnStmt
	ast.Inspect(lit.Body, func(n ast.Node) bool {
		if bad {
			return false
		}
		if _, ok := n.(*ast.FuncLit); ok {
			return false
		}
		if ret, ok := n.(*ast.ReturnStmt); ok {
			if len(ret.Results) != 1 {
				bad = true
				return false
			}
			rets = append(rets, ret)
		}
		return true
	})
	if bad {
		return nil, true
	}
	if f.hasComment(vs.End(), f.lineEnd(vs.End())) {
		return nil, true
	}
	for _, ret := range rets {
		if f.hasComment(ret.Pos(), ret.End()) {
			return nil, true
		}
	}
	name := vs.Names[0].Name
	renames, ok := f.renames(name, lit.Body, rets)
	if !ok {
		return nil, false
	}
	pos := lit.Body.Lbrace + 1
	var body strings.Builder
	for _, ret := range rets {
		body.WriteString(f.textRenamed(pos, ret.Pos(), renames))
		fmt.Fprintf(&body, "%s = %s", name,
			f.textRenamed(ret.Results[0].Pos(), ret.Results[0].End(), renames),
		)
		if ret != last {
			body.WriteString("; return")
		}
		pos = ret.End()
	}
	body.WriteString(f.textRenamed(pos, lit.Body.Rbrace, renames))
	return []analysis.TextEdit{{
		Pos: vs.Names[0].End(),
		End: value.End(),
		NewText: []byte(" " +
			f.text(field.Type.Pos(), field.Type.End()),
		),
	}, {
		Pos:     gd.End(),
		End:     gd.End(),
		NewText: []byte("\n\nfunc init() {" + body.String() + "}"),
	}}, true
}
