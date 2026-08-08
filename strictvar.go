// Package strictvar provides an analysis.Analyzer that reduces the number of
// ways a variable may be declared.
//
// strictvar is a formatter with type information. Whether a declaration
// carries a zero value is invisible to syntax alone, so these rules cannot
// live in a formatter, but running the fixes is meant to feel like running
// one.
//
// Two ideas underlie every check. Zero values stay visually distinct, with no
// right-hand side and new(T) in place of &T{}, so declaring one reads as a
// deliberate act, and setting variables up ahead of use becomes the natural
// style. And where Go permits many spellings of one declaration, whether var
// or :=, typed or inferred, standalone or grouped, a single spelling is
// chosen.
//
// strictvar is deliberately opinionated and likely over-strict. Whether it
// makes real code clearer can only be learned by running it against real code,
// so it ships as an experiment, expected to loosen as results come in.
//
// Fixes are only suggested when there is exactly one obvious rewrite and no
// information is lost. A fix is withheld when it would remove a comment
// attached to nothing. Applying fixes can expose further fixes, so the tool
// may need to run more than once until all fixes apply cleanly. The README
// catalogs every check, exemption, and fix.
package strictvar

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
)

var Analyzer = &analysis.Analyzer{
	Name: "strictvar",
	Doc:  "reports variable declarations that deviate from strict form",
	Run:  run,
}

func run(pass *analysis.Pass) (any, error) {
	for _, syntax := range pass.Files {
		tf := pass.Fset.File(syntax.FileStart)
		src, err := pass.ReadFile(tf.Name())
		if err != nil {
			return nil, fmt.Errorf("failed to open file: %w", err)
		}
		f := &file{pass: pass, File: tf, syntax: syntax, src: src}
		f.check()
	}
	checkSingleUseStrings(pass)
	return nil, nil
}

// file carries per-file state shared by the checks.
type file struct {
	*token.File

	pass   *analysis.Pass
	syntax *ast.File
	src    []byte

	// owned holds declarations another rule is rewriting this pass.
	// Later rules leave them alone, and spec-level fixes inside them
	// wait for the following pass.
	owned map[*ast.GenDecl]struct{}
}

func (f *file) check() {
	f.owned = make(map[*ast.GenDecl]struct{})
	// checkGroups runs first. It claims declarations in owned, and
	// every later check stands down inside a claimed span.
	f.checkGroups()
	f.checkZeroDecls()
	f.checkIIFE()
	f.checkNewExprs()
	f.checkNamedReturns()
	f.checkMakeDecls()
}

// text returns the source text between two positions.
func (f *file) text(pos, end token.Pos) string {
	return string(f.src[f.Offset(pos):f.Offset(end)])
}

// Line returns pos's line in the raw line table, consistent with
// lineStart and lineEnd. It stands in front of the embedded File's
// Line, which would apply //line directives the rest of the
// position math never sees.
func (f *file) Line(pos token.Pos) int {
	return f.PositionFor(pos, false).Line
}

// indent returns the leading whitespace of the line containing pos.
func (f *file) indent(pos token.Pos) string {
	var (
		start = f.Offset(f.lineStart(pos))
		end   = f.Offset(pos)
	)
	for i := start; i < end; i++ {
		if f.src[i] != ' ' && f.src[i] != '\t' {
			end = i
			break
		}
	}
	return string(f.src[start:end])
}

func (f *file) lineStart(pos token.Pos) token.Pos {
	return f.LineStart(f.Line(pos))
}

// lineEnd returns the position of the newline ending the line holding pos, or
// the end of the file on the final line.
func (f *file) lineEnd(pos token.Pos) token.Pos {
	line := f.Line(pos)
	if line == f.LineCount() {
		return token.Pos(f.Base() + f.Size())
	}
	return f.LineStart(line+1) - 1
}

// deleteLineSpan returns the span that removes the lines holding node,
// including the trailing newline. ok is false when the node shares its lines
// with anything else, including a comment: comments are never removed by a
// fix.
func (f *file) deleteLineSpan(node ast.Node) (pos, end token.Pos, ok bool) {
	pos = f.lineStart(node.Pos())
	end = f.lineEnd(node.End())
	for _, b := range f.src[f.Offset(pos):f.Offset(node.Pos())] {
		if b != ' ' && b != '\t' {
			return 0, 0, false
		}
	}
	for _, b := range f.src[f.Offset(node.End()):f.Offset(end)] {
		if b != ' ' && b != '\t' && b != '\r' {
			return 0, 0, false
		}
	}
	if f.Line(end) < f.LineCount() {
		end++ // the trailing newline
	}
	return pos, end, true
}

// trailOK reports whether everything after pos on its line is blank or a line
// comment.
func (f *file) trailOK(pos token.Pos) bool {
	s := strings.TrimSpace(f.text(pos, f.lineEnd(pos)))
	return s == "" || strings.HasPrefix(s, "//")
}

// strayComments reports whether a comment in [lo, hi] starts outside every
// kept range.
func (f *file) strayComments(lo, hi token.Pos, kept [][2]token.Pos) bool {
	for _, cg := range f.syntax.Comments {
		if cg.End() < lo {
			continue
		}
		if cg.Pos() > hi {
			break
		}
		var ok bool
		for _, k := range kept {
			if cg.Pos() >= k[0] && cg.Pos() <= k[1] {
				ok = true
				break
			}
		}
		if !ok {
			return true
		}
	}
	return false
}

// hasComment reports whether any comment overlaps [pos, end].
func (f *file) hasComment(pos, end token.Pos) bool {
	for _, cg := range f.syntax.Comments {
		if cg.End() < pos {
			continue
		}
		if cg.Pos() > end {
			break
		}
		return true
	}
	return false
}

// stmtLists visits every statement list in the file: block bodies and the
// bodies of case and comm clauses.
func (f *file) stmtLists(fn func(list []ast.Stmt)) {
	ast.Inspect(f.syntax, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.BlockStmt:
			fn(node.List)
		case *ast.CaseClause:
			fn(node.Body)
		case *ast.CommClause:
			fn(node.Body)
		}
		return true
	})
}

// report emits a diagnostic. fix names the edit as a capitalized imperative
// when edits carry one.
func (f *file) report(pos, end token.Pos, msg, fix string, edits []analysis.TextEdit) {
	d := analysis.Diagnostic{Pos: pos, End: end, Message: msg}
	if len(edits) > 0 {
		d.SuggestedFixes = []analysis.SuggestedFix{{
			Message:   fix,
			TextEdits: edits,
		}}
	}
	f.pass.Report(d)
}

// ownedSpan reports whether pos falls inside a declaration another rule is
// rewriting: an edit there would conflict with the rewrite, so its fix waits
// for the following pass.
func (f *file) ownedSpan(pos token.Pos) bool {
	for gd := range f.owned {
		if pos >= gd.Pos() && pos < gd.End() {
			return true
		}
	}
	return false
}

// shadowed reports whether name resolves to something other than the
// predeclared identifier at pos.
func (f *file) shadowed(name string, pos token.Pos) bool {
	var (
		inner *types.Scope
		info  = f.pass.TypesInfo
	)
	for _, scope := range info.Scopes {
		if scope == nil || !scope.Contains(pos) {
			continue
		}
		if inner == nil || scope.Pos() > inner.Pos() {
			inner = scope
		}
	}
	if inner == nil {
		return false
	}
	_, obj := inner.LookupParent(name, pos)
	if obj == nil {
		return false
	}
	_, isBuiltin := obj.(*types.Builtin)
	return !isBuiltin
}
