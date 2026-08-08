package strictvar

import (
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// checkSingleUseStrings reports an unexported package variable holding a
// quoted string literal, directly or through a conversion, that is read
// exactly once inside a function: the content belongs where its one reader
// lives. No fix is offered, since the right destination inside the function
// is a placement judgment.
//
// Only interpreted string literals qualify: a raw string literal is an embed
// and stays hoisted, and any other initializer -- numeric tunables, computed
// values, registrations -- is configuration that earns its package-scope name.
// Reassignment, address-taking, exported names, multi-name specifications, and
// reads in package-scope initializers all stand down.
func checkSingleUseStrings(pass *analysis.Pass) {
	vars := singleUseStringCandidates(pass)
	if len(vars) == 0 {
		return
	}
	reads := make(map[types.Object][]token.Pos)
	for _, f := range pass.Files {
		collectPackageReads(pass, f, vars, reads)
	}
	for obj, spec := range vars {
		sites := reads[obj]
		if len(sites) != 1 || !inFunction(pass, sites[0]) {
			continue
		}
		pass.Report(analysis.Diagnostic{
			Pos: spec.Pos(),
			End: spec.End(),
			Message: "package variable " + obj.Name() +
				" is read once and can be declared where it is read",
		})
	}
}

// singleUseStringCandidates returns the unexported, initialized, embed-free
// package string variables by object.
func singleUseStringCandidates(pass *analysis.Pass) map[types.Object]*ast.ValueSpec {
	vars := make(map[types.Object]*ast.ValueSpec)
	for _, f := range pass.Files {
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok || len(vs.Names) != 1 || len(vs.Values) != 1 {
					continue
				}
				name := vs.Names[0]
				if name.IsExported() || name.Name == "_" {
					continue
				}
				if !quotedStringValue(pass, vs.Values[0]) {
					continue
				}
				if obj := pass.TypesInfo.Defs[name]; obj != nil {
					vars[obj] = vs
				}
			}
		}
	}
	return vars
}

// collectPackageReads gathers read positions for the candidates and deletes
// any candidate that is written or aliased.
func collectPackageReads(pass *analysis.Pass, f *ast.File, vars map[types.Object]*ast.ValueSpec, reads map[types.Object][]token.Pos) {
	info := pass.TypesInfo
	ast.Inspect(f, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.AssignStmt:
			for _, lhs := range node.Lhs {
				if id, ok := lhs.(*ast.Ident); ok {
					if obj := info.Uses[id]; obj != nil {
						delete(vars, obj)
					}
				}
			}
		case *ast.UnaryExpr:
			if node.Op != token.AND {
				return true
			}
			if id, ok := rootExpr(node.X).(*ast.Ident); ok {
				if obj := info.Uses[id]; obj != nil {
					delete(vars, obj)
				}
			}
		case *ast.Ident:
			obj := info.Uses[node]
			if obj == nil {
				return true
			}
			if _, ok := vars[obj]; ok {
				reads[obj] = append(reads[obj], node.Pos())
			}
		}
		return true
	})
}

// rootExpr unwraps index, star, selector, and paren layers to the base
// expression an address-of ultimately reaches.
func rootExpr(e ast.Expr) ast.Expr {
	for {
		switch node := e.(type) {
		case *ast.IndexExpr:
			e = node.X
		case *ast.StarExpr:
			e = node.X
		case *ast.SelectorExpr:
			e = node.X
		case *ast.ParenExpr:
			e = node.X
		default:
			return e
		}
	}
}

// quotedStringValue reports whether e is an interpreted string literal, bare
// or inside a conversion such as []byte("..."). A call that is not a
// conversion constructs a value -- an error sentinel, a parsed template -- and
// is not moved.
func quotedStringValue(pass *analysis.Pass, e ast.Expr) bool {
	call, ok := e.(*ast.CallExpr)
	if ok && len(call.Args) == 1 {
		if pass.TypesInfo.Types[call.Fun].IsType() {
			e = call.Args[0]
		}
	}
	lit, ok := e.(*ast.BasicLit)
	return ok && lit.Kind == token.STRING && strings.HasPrefix(lit.Value, `"`)
}

// inFunction reports whether pos falls inside a function body in the package.
func inFunction(pass *analysis.Pass, pos token.Pos) bool {
	for _, f := range pass.Files {
		if pos < f.Pos() || pos > f.End() {
			continue
		}
		var inside bool
		ast.Inspect(f, func(n ast.Node) bool {
			fd, ok := n.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				return true
			}
			if pos >= fd.Body.Pos() && pos <= fd.Body.End() {
				inside = true
			}
			return !inside
		})
		return inside
	}
	return false
}
