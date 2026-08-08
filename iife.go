package strictvar

import (
	"fmt"
	"go/ast"
	"go/token"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// checkIIFE reports package-level variables initialized by an immediately
// invoked function literal. The Go convention is var x T plus func init(). The
// IIFE is a JavaScript idiom. A literal whose body is a lone return is a
// pointless wrapper and unwraps in place, and anything with real statements
// needs an init function's shape, which is the author's to write.
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
	edits := f.iifeEdits(gd, vs, lit)
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

// iifeEdits unwraps a lone-return literal in place, and turns any other
// literal into var x T plus a func init holding the body, with each top-level
// return expr rewritten to x = expr.
func (f *file) iifeEdits(gd *ast.GenDecl, vs *ast.ValueSpec, lit *ast.FuncLit) []analysis.TextEdit {
	value := vs.Values[0]
	if len(lit.Body.List) == 0 {
		return nil
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
			}}
		}
	}
	if lit.Type.Results == nil || len(lit.Type.Results.List) != 1 {
		return nil
	}
	field := lit.Type.Results.List[0]
	if len(field.Names) != 0 {
		return nil
	}
	if f.hasComment(vs.Names[0].End(), lit.Body.Lbrace) {
		return nil
	}
	if f.initializerRef(vs.Names[0]) {
		// Package variables initialize before any init runs: a
		// sibling initializer reading this variable would see the
		// zero value after the move.
		return nil
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
		return nil
	}
	name := vs.Names[0].Name
	pos := lit.Body.Lbrace + 1
	var body strings.Builder
	for _, ret := range rets {
		body.WriteString(f.text(pos, ret.Pos()))
		fmt.Fprintf(&body, "%s = %s", name,
			f.text(ret.Results[0].Pos(), ret.Results[0].End()),
		)
		if ret != last {
			body.WriteString("; return")
		}
		pos = ret.End()
	}
	body.WriteString(f.text(pos, lit.Body.Rbrace))
	return []analysis.TextEdit{{
		Pos: vs.Names[0].End(),
		End: value.End(),
		NewText: []byte(" " +
			f.text(field.Type.Pos(), field.Type.End())),
	}, {
		Pos:     gd.End(),
		End:     gd.End(),
		NewText: []byte("\n\nfunc init() {" + body.String() + "}"),
	}}
}
