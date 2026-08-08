package strictvar

import (
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// checkBlockCommentSpacing reports a commented spec that touches the spec
// above it. A doc comment inside a block starts a visual group, so a blank
// line should precede it.
func (f *file) checkBlockCommentSpacing(gd *ast.GenDecl) {
	for i := 1; i < len(gd.Specs); i++ {
		vs, ok := gd.Specs[i].(*ast.ValueSpec)
		if !ok || vs.Doc == nil {
			continue
		}
		if f.Line(vs.Doc.Pos()) != f.Line(gd.Specs[i-1].End())+1 {
			continue
		}
		f.report(vs.Pos(), vs.End(),
			"commented declaration should be preceded by a blank line",
			"Insert a blank line", []analysis.TextEdit{{
				Pos:     f.lineStart(vs.Doc.Pos()),
				End:     f.lineStart(vs.Doc.Pos()),
				NewText: []byte("\n"),
			}},
		)
	}
}

// checkBlockPairs reports zero-value specs in a var block that repeat the type
// of an earlier zero-value spec. The fix rewrites the whole block in one edit:
// separate insert and delete edits on one block interleave badly under
// repeated fix application.
func (f *file) checkBlockPairs(gd *ast.GenDecl) {
	var (
		firstDup *ast.ValueSpec
		msg      string
		combined = make(map[*ast.ValueSpec]string)
		skip     = make(map[*ast.ValueSpec]struct{})
		first    = make(map[string]*ast.ValueSpec)
	)
	for _, spec := range gd.Specs {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok {
			return
		}
		if len(vs.Values) != 0 || vs.Type == nil {
			continue
		}
		typ := f.text(vs.Type.Pos(), vs.Type.End())
		prev, ok := first[typ]
		if !ok {
			first[typ] = vs
			combined[vs] = names(vs)
			continue
		}
		if vs.Doc != nil || vs.Comment != nil {
			return
		}
		combined[prev] += ", " + names(vs)
		skip[vs] = struct{}{}
		if firstDup == nil {
			firstDup = prev
		}
		if prev == firstDup {
			msg = combined[prev] + " " + typ
		}
	}
	if firstDup == nil {
		return
	}
	var lines []string
	fixOK := f.trailOK(gd.Lparen + 1)
	for _, spec := range gd.Specs {
		vs := spec.(*ast.ValueSpec)
		if _, ok := skip[vs]; ok {
			continue
		}
		bad := f.Line(vs.Pos()) == f.Line(gd.Lparen) || !f.trailOK(vs.End())
		if bad {
			fixOK = false
			break
		}
		c, merged := combined[vs]
		if merged && c != names(vs) {
			lines = append(lines, c+" "+
				f.text(vs.Type.Pos(), f.lineEnd(vs.End())),
			)
			continue
		}
		lines = append(lines, f.lineText(vs))
	}
	indent := f.indent(gd.Pos())
	text := "(" + strings.TrimRight(
		f.text(gd.Lparen+1, f.lineEnd(gd.Lparen)), " \t",
	) + "\n"
	text += blockText(indent, lines) + indent + ")"
	var edits []analysis.TextEdit
	last := gd.Specs[len(gd.Specs)-1]
	fixable := fixOK && !f.shadowRebind(gd) &&
		f.Line(gd.Rparen) > f.Line(last.End()) &&
		!f.strayComments(gd.Lparen, gd.Rparen, f.blockKept(gd)) &&
		parses("var "+text)
	if fixable {
		f.owned[gd] = struct{}{}
		edits = []analysis.TextEdit{{
			Pos:     gd.Lparen,
			End:     gd.Rparen + 1,
			NewText: []byte(text),
		}}
	}
	f.report(firstDup.Pos(), firstDup.End(),
		"zero-value declarations of the same type can be combined: "+
			msg, "Combine the declarations", edits,
	)
}

// blockKept returns the comment-carrying ranges of a block: the paren line and
// each spec line.
func (f *file) blockKept(gd *ast.GenDecl) [][2]token.Pos {
	kept := [][2]token.Pos{{gd.Lparen, f.lineEnd(gd.Lparen)}}
	for _, spec := range gd.Specs {
		kept = append(kept, [2]token.Pos{spec.Pos(), f.lineEnd(spec.End())})
	}
	return kept
}

// shadowRebind reports whether moving the block's declarations could rebind a
// name: some value reads an identifier spelled like a block name that resolves
// elsewhere, so a declaration moving above the value would capture it.
// Selector fields do not count, since only the root of a selector chain
// resolves in scope.
func (f *file) shadowRebind(gd *ast.GenDecl) bool {
	var (
		found bool
		walk  func(n ast.Node)
		info  = f.pass.TypesInfo
		owned = make(map[string]types.Object)
	)
	for _, spec := range gd.Specs {
		for _, n := range spec.(*ast.ValueSpec).Names {
			if obj := info.Defs[n]; obj != nil {
				owned[n.Name] = obj
			}
		}
	}
	walk = func(n ast.Node) {
		if found || n == nil {
			return
		}
		if sel, ok := n.(*ast.SelectorExpr); ok {
			walk(sel.X)
			return
		}
		if id, ok := n.(*ast.Ident); ok {
			obj, has := owned[id.Name]
			if has && info.Uses[id] != nil && info.Uses[id] != obj {
				found = true
			}
			return
		}
		ast.Inspect(n, func(c ast.Node) bool {
			if c == n {
				return true
			}
			walk(c)
			return false
		})
	}
	for _, spec := range gd.Specs {
		vs := spec.(*ast.ValueSpec)
		for _, v := range vs.Values {
			walk(v)
		}
		if vs.Type != nil {
			walk(vs.Type)
		}
	}
	return found
}
