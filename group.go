package strictvar

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// declItem is a var or const declaration at a known index in its scope's
// statement or declaration list.
type declItem struct {
	decl *ast.GenDecl
	idx  int
}

// blockText assembles the body of a synthesized block: one spec per line,
// source newlines dropped.
func blockText(indent string, lines []string) (text string) {
	for _, l := range lines {
		text += indent + "\t" + l + "\n"
	}
	return
}

// parses reports whether text parses as a declaration inside a function body.
// A rebuilt block that fails to parse is withheld: a span bug must yield a
// false negative, never corrupt code.
func parses(text string) bool {
	_, err := parser.ParseFile(token.NewFileSet(), "p.go",
		"package p\nfunc _() {\n"+text+"\n}\n", 0,
	)
	return err == nil
}

// span adapts a position pair to ast.Node for text helpers.
type span struct{ pos, end token.Pos }

func (s span) Pos() token.Pos { return s.pos }

func (s span) End() token.Pos { return s.end }

// checkGroups enforces the grouping rules: two or more adjacent declarations
// become a block, at function scope only in the preamble where a var block
// anywhere else dissolves instead, same-type zero values merge, and a block
// immediately following another block of its kind merges into it.
func (f *file) checkGroups() {
	f.checkPreamble()
	var items []declItem
	for i, d := range f.syntax.Decls {
		if gd, ok := d.(*ast.GenDecl); ok {
			items = append(items, declItem{gd, i})
		}
	}
	f.checkScope(items, true)
	f.stmtLists(func(list []ast.Stmt) {
		var items []declItem
		for i, stmt := range list {
			ds, ok := stmt.(*ast.DeclStmt)
			if !ok {
				continue
			}
			gd, ok := ds.Decl.(*ast.GenDecl)
			_, claimed := f.owned[gd]
			if ok && !claimed {
				items = append(items, declItem{gd, i})
			}
		}
		f.checkScope(items, false)
	})
	ast.Inspect(f.syntax, func(n ast.Node) bool {
		var (
			gd, ok     = n.(*ast.GenDecl)
			_, claimed = f.owned[gd]
		)
		if !ok || !gd.Lparen.IsValid() || claimed {
			return true
		}
		if gd.Tok == token.VAR {
			f.checkBlockPairs(gd)
		}
		if gd.Tok == token.VAR || gd.Tok == token.CONST {
			f.checkBlockCommentSpacing(gd)
		}
		return true
	})
}

// checkScope applies the adjacency, one-block, and := rules to the
// declarations of a single scope.
func (f *file) checkScope(items []declItem, file bool) {
	for _, tok := range []token.Token{token.VAR, token.CONST} {
		for _, run := range runs(items, tok) {
			if tok == token.VAR && !file {
				f.checkZeroCombines(run)
				continue
			}
			block := len(run) >= 3 || len(run) == 2 &&
				!f.zeroPairShape(run[0].decl, run[1].decl)
			if block {
				f.reportRun(run)
			} else if len(run) == 2 {
				f.checkZeroCombines(run)
			}
		}
		f.checkOneBlock(items, tok)
	}
	if file {
		return
	}
	for _, it := range items {
		if it.decl.Tok == token.VAR {
			f.checkNonZeroVar(it.decl)
		}
	}
}

// runs returns the maximal groups of adjacent unparenthesized declarations of
// the given kind. A declaration with a doc comment is its own documentation
// unit. At package scope its comment renders as prose on pkgsite, where a
// block spec's comment renders as source text, so it never joins a group and
// breaks adjacency.
func runs(items []declItem, tok token.Token) (out [][]declItem) {
	var (
		cur  []declItem
		prev = -2
	)
	for _, it := range items {
		if it.decl.Tok != tok || it.decl.Doc != nil {
			continue
		}
		if it.decl.Lparen.IsValid() {
			continue
		}
		if it.idx != prev+1 && len(cur) > 0 {
			out = append(out, cur)
			cur = nil
		}
		cur = append(cur, it)
		prev = it.idx
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

// checkNonZeroVar reports a function-scoped var declaration carrying a
// non-zero value: initialized variables use :=, keeping var visually reserved
// for zero values. Declarations grouped into a block by the adjacency rule are
// handled there instead.
func (f *file) checkNonZeroVar(gd *ast.GenDecl) {
	if gd.Lparen.IsValid() || len(gd.Specs) != 1 {
		return
	}
	vs, ok := gd.Specs[0].(*ast.ValueSpec)
	if !ok || len(vs.Names) != 1 || len(vs.Values) != 1 {
		return
	}
	name := vs.Names[0]
	if name.Name == "_" {
		return
	}
	if vs.Type == nil {
		if _, zero := f.zeroRewrite(name.Name, vs.Values[0]); zero {
			return
		}
	} else {
		typ := f.pass.TypesInfo.TypeOf(vs.Type)
		if typ == nil || f.zeroForType(vs.Values[0], typ) {
			return
		}
		if !f.typeFaithful(vs.Values[0], typ) {
			// The declared type changes the value's type: var is the
			// one form that widening can take, so the declaration is
			// doing work := cannot express.
			return
		}
	}
	var edits []analysis.TextEdit
	repl := name.Name + " := " + f.text(vs.Values[0].Pos(), vs.Values[0].End())
	if !containsEmptyRef(vs.Values[0]) && !f.hasComment(gd.Pos(), gd.End()) {
		edits = []analysis.TextEdit{{
			Pos:     gd.Pos(),
			End:     gd.End(),
			NewText: []byte(repl),
		}}
	}
	f.report(gd.Pos(), gd.End(),
		"non-zero value can be declared with :=: "+repl,
		"Declare with :=", edits,
	)
}

// containsEmptyRef reports whether expr contains a &T{} the new(T) check will
// rewrite. Its edit would nest inside this rule's edit, so withholding this
// fix lets the rewrites apply on separate passes.
func containsEmptyRef(expr ast.Expr) bool {
	var found bool
	ast.Inspect(expr, func(n ast.Node) bool {
		un, ok := n.(*ast.UnaryExpr)
		if ok && un.Op == token.AND {
			lit, ok := un.X.(*ast.CompositeLit)
			if ok && len(lit.Elts) == 0 {
				found = true
			}
		}
		return !found
	})
	return found
}

// reportRun reports two or more adjacent declarations and suggests merging
// them into one block. A comment on its own line between the declarations
// marks them as deliberately separate: no report.
func (f *file) reportRun(run []declItem) {
	first, last := run[0].decl, run[len(run)-1].decl
	if first.Tok == token.CONST {
		for _, it := range run {
			if mentionsIota(f.pass.TypesInfo, it.decl) {
				return
			}
		}
	}
	var kept [][2]token.Pos
	for _, it := range run {
		for _, spec := range it.decl.Specs {
			kept = append(kept, [2]token.Pos{
				spec.Pos(), f.lineEnd(spec.End()),
			})
		}
	}
	if f.strayComments(first.Pos(), f.lineEnd(last.End()), kept) {
		return
	}
	kind := first.Tok.String()
	f.report(first.Pos(), last.End(), fmt.Sprintf(
		"%d adjacent %s declarations can be grouped into a %s block",
		len(run), kind, kind,
	), "Group the declarations into one block", f.runMergeEdits(run))
}

// runMergeEdits builds the block replacing a run of declarations. Trailing
// line comments ride along with their specs. reportRun has already
// established that no other comment lies in the span.
func (f *file) runMergeEdits(run []declItem) []analysis.TextEdit {
	var lines []string
	for _, it := range run {
		for _, spec := range it.decl.Specs {
			_, ok := spec.(*ast.ValueSpec)
			if !ok || !f.trailOK(spec.End()) {
				return nil
			}
			lines = append(lines, f.lineText(spec))
		}
	}
	first, last := run[0].decl, run[len(run)-1].decl
	hi := f.lineEnd(last.End())
	indent := f.indent(first.Pos())
	text := first.Tok.String() + " (\n" +
		blockText(indent, lines) + indent + ")"
	if !parses(text) {
		return nil
	}
	return []analysis.TextEdit{{
		Pos:     first.Pos(),
		End:     hi,
		NewText: []byte(text),
	}}
}

// lineText returns the source of node through the end of its final line,
// without trailing whitespace: the node plus any trailing line comment.
func (f *file) lineText(node ast.Node) string {
	return strings.TrimRight(f.text(node.Pos(), f.lineEnd(node.End())), " \t")
}

// mentionsIota reports whether the declaration references the predeclared
// iota.
func mentionsIota(info *types.Info, decl *ast.GenDecl) bool {
	var found bool
	ast.Inspect(decl, func(n ast.Node) bool {
		id, ok := n.(*ast.Ident)
		if ok && info.Uses[id] == types.Universe.Lookup("iota") {
			found = true
		}
		return !found
	})
	return found
}

// checkZeroCombines reports runs of adjacent same-type zero-value
// declarations anywhere in a function and suggests combining each run into
// one spec: var x, y int. The combine is independent of block grouping, so it
// applies mid-scope where blocks do not form. A comment on its own line
// between declarations marks them as deliberately separate and splits the
// run.
func (f *file) checkZeroCombines(run []declItem) {
	var (
		group []*ast.GenDecl
		flush = func() {
			if len(group) >= 2 {
				f.reportZeroRun(group)
			}
			group = nil
		}
	)
	for _, it := range run {
		if _, ok := zeroSpec(it.decl); !ok {
			flush()
			continue
		}
		if len(group) > 0 {
			var (
				prev  = group[len(group)-1]
				lo    = f.lineEnd(prev.End()) + 1
				apart = f.hasComment(lo, f.lineStart(it.decl.Pos()))
			)
			if apart || !f.zeroPairShape(prev, it.decl) {
				flush()
			}
		}
		group = append(group, it.decl)
	}
	flush()
}

// reportZeroRun reports a run of same-type zero-value declarations, anchored
// on the last declaration, and suggests combining them into one spec. A
// comment anywhere but trailing the last declaration would be orphaned by the
// rewrite, so the fix stands down.
func (f *file) reportZeroRun(group []*ast.GenDecl) {
	var (
		parts       []string
		first, last = group[0], group[len(group)-1]
	)
	for _, gd := range group {
		vs, _ := zeroSpec(gd)
		parts = append(parts, names(vs))
	}
	fs, _ := zeroSpec(first)
	combined := "var " + strings.Join(parts, ", ") + " " +
		f.text(fs.Type.Pos(), fs.Type.End())
	var edits []analysis.TextEdit
	fixOK := !f.hasComment(first.Pos(), last.End())
	for _, gd := range group[:len(group)-1] {
		fixOK = fixOK && f.trailOK(gd.End())
	}
	if fixOK {
		for _, gd := range group {
			f.owned[gd] = struct{}{}
		}
		edits = []analysis.TextEdit{{
			Pos:     first.Pos(),
			End:     last.End(),
			NewText: []byte(combined),
		}}
	}
	f.report(last.Pos(), last.End(),
		"zero-value declarations of the same type can be combined: "+
			combined, "Combine the declarations", edits,
	)
}

// zeroRunStmts reports whether the statements are all declarations in one
// zero-run shape: same-type zero-value declarations the combine rule owns,
// yielding one spec rather than a block.
func (f *file) zeroRunStmts(units []ast.Stmt) bool {
	var prev *ast.GenDecl
	for _, stmt := range units {
		ds, ok := stmt.(*ast.DeclStmt)
		if !ok {
			return false
		}
		gd, ok := ds.Decl.(*ast.GenDecl)
		if !ok {
			return false
		}
		if prev == nil {
			if gd.Tok != token.VAR {
				return false
			}
			if _, ok := zeroSpec(gd); !ok {
				return false
			}
		} else if !f.zeroPairShape(prev, gd) {
			return false
		}
		prev = gd
	}
	return true
}

// zeroPairShape reports whether a and b are zero declarations of one type: the
// pair-combine rule owns that shape, yielding one spec rather than a block.
func (f *file) zeroPairShape(a, b *ast.GenDecl) bool {
	if a.Tok != token.VAR {
		return false
	}
	as, aok := zeroSpec(a)
	bs, bok := zeroSpec(b)
	if !aok || !bok {
		return false
	}
	return f.text(as.Type.Pos(), as.Type.End()) ==
		f.text(bs.Type.Pos(), bs.Type.End())
}

// zeroSpec returns the sole value-free spec of an unparenthesized var
// declaration.
func zeroSpec(gd *ast.GenDecl) (*ast.ValueSpec, bool) {
	if gd.Lparen.IsValid() || len(gd.Specs) != 1 {
		return nil, false
	}
	vs, ok := gd.Specs[0].(*ast.ValueSpec)
	if !ok || len(vs.Values) != 0 || vs.Type == nil {
		return nil, false
	}
	return vs, true
}

func names(vs *ast.ValueSpec) string {
	var s strings.Builder
	s.WriteString(vs.Names[0].Name)
	for _, n := range vs.Names[1:] {
		s.WriteString(", ")
		s.WriteString(n.Name)
	}
	return s.String()
}

// checkOneBlock reports every parenthesized block after the first of its kind
// in a scope. A block with a doc comment is its own documentation unit and
// takes no part: it neither absorbs other blocks nor merges away.
func (f *file) checkOneBlock(items []declItem, tok token.Token) {
	var (
		prev    *ast.GenDecl
		prevIdx int
	)
	for _, it := range items {
		if it.decl.Tok != tok || it.decl.Doc != nil {
			continue
		}
		if !it.decl.Lparen.IsValid() {
			continue
		}
		if prev != nil && it.idx == prevIdx+1 {
			// Only a block immediately following another can
			// merge: a block placed beyond intervening code is
			// the author's island.
			f.reportExtraBlock(prev, it.decl)
		}
		prev, prevIdx = it.decl, it.idx
	}
}

// reportExtraBlock reports block b, which can be absorbed into block a. A
// comment on its own line between the blocks marks them deliberately separate,
// as does a comment inside b not trailing a spec.
func (f *file) reportExtraBlock(a, b *ast.GenDecl) {
	if b.Tok == token.CONST && mentionsIota(f.pass.TypesInfo, b) {
		return
	}
	if f.hasComment(f.lineEnd(a.End())+1, f.lineStart(b.Pos())) {
		return
	}
	kept := [][2]token.Pos{{b.Lparen, f.lineEnd(b.Lparen)}}
	for _, spec := range b.Specs {
		vs, ok := spec.(*ast.ValueSpec)
		if ok && vs.Doc != nil {
			kept = append(kept, [2]token.Pos{vs.Doc.Pos(), vs.Doc.End()})
		}
		kept = append(kept, [2]token.Pos{spec.Pos(), f.lineEnd(spec.End())})
	}
	if f.strayComments(b.Lparen, b.Rparen, kept) {
		return
	}
	edits := f.blockMergeEdits(a, b)
	if edits != nil {
		f.owned[a] = struct{}{}
		f.owned[b] = struct{}{}
	}
	// The diagnostic anchors on the first spec: the block header
	// vanishes under the fix, and the spec line survives it.
	pos, end := b.Pos(), b.End()
	if len(b.Specs) > 0 {
		pos, end = b.Specs[0].Pos(), b.Specs[0].End()
	}
	kind := a.Tok.String()
	f.report(pos, end, fmt.Sprintf(
		"%s block can be merged into the %s block on line %d",
		kind, kind, f.Line(a.Pos()),
	), "Merge the blocks", edits)
}

// blockMergeEdits absorbs block b into block a: b's specs join directly after
// a's last spec, one per line, and a doc-commented spec keeps its comment
// behind the blank line the comment-spacing rule enforces. Doc and trailing
// comments ride with their specs. A comment attached to nothing marks
// deliberate structure and yields no fix, as does a rewrite already claiming
// either block.
func (f *file) blockMergeEdits(a, b *ast.GenDecl) []analysis.TextEdit {
	var (
		_, ownedA = f.owned[a]
		_, ownedB = f.owned[b]
	)
	if ownedA || ownedB {
		return nil
	}
	last := a.Lparen
	if len(a.Specs) > 0 {
		last = a.Specs[len(a.Specs)-1].End()
	}
	if f.Line(a.Rparen) == f.Line(last) {
		return nil
	}
	var sb strings.Builder
	indent := f.indent(a.Pos())
	pos := f.lineEnd(last) + 1
	blank := []span{
		{pos, a.Rparen},
		{a.Rparen + 1, f.lineStart(b.Pos())},
		{b.Lparen + 1, f.lineEnd(b.Lparen)},
	}
	for _, s := range blank {
		if strings.TrimSpace(f.text(s.pos, s.end)) != "" {
			return nil
		}
	}
	for _, spec := range b.Specs {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok || !f.trailOK(vs.End()) {
			return nil
		}
		if vs.Doc != nil {
			if sb.Len() > 0 || len(a.Specs) > 0 {
				sb.WriteString("\n")
			}
			for _, c := range vs.Doc.List {
				fmt.Fprintf(&sb, "%s\t%s\n", indent, f.lineText(c))
			}
		}
		fmt.Fprintf(&sb, "%s\t%s\n", indent, f.lineText(vs))
	}
	text := sb.String() + indent + ")"
	if !parses(a.Tok.String() + " (\n" + text) {
		return nil
	}
	return []analysis.TextEdit{{
		Pos:     pos,
		End:     b.Rparen + 1,
		NewText: []byte(text),
	}}
}
