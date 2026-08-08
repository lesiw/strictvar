package strictvar

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

func TestSingleUseStrings(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), Analyzer, "singleuse")
}

func TestSuggestedFixes(t *testing.T) {
	analysistest.RunWithSuggestedFixes(t, analysistest.TestData(),
		Analyzer, "zero", "group", "block", "named", "makedecl", "preamble",
	)
}

// TestInlineBlock covers shapes gofmt erases, so they cannot live in
// testdata. A one-line var block is reported as mergeable, and the
// fix is withheld: its spec shares the Lparen line.
func TestInlineBlock(t *testing.T) {
	dir, cleanup, err := analysistest.WriteFiles(map[string]string{
		"inline/inline.go": `package inline

func work() int { return 1 }

var (gOne = work())

var (
	gTwo = work() // want "merged into the var block on line 5"
)
`,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	analysistest.Run(t, dir, Analyzer, "inline")
}
