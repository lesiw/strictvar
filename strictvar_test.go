package strictvar

import (
	"embed"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

//go:embed testdata
var testdata embed.FS

// testdataDir materializes the embedded testdata into a fresh temp
// directory, restoring the .go extensions the fixtures are stored
// without. Fixtures live as .input in the repo so the formatter never
// touches them, but analysistest needs real .go files to parse. The
// returned dir holds the same src/ layout analysistest expects.
func testdataDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	walk := func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		content, err := testdata.ReadFile(path)
		if err != nil {
			return err
		}
		// Drop the testdata/ prefix; the embedded tree already carries src/.
		repoPath, _ := strings.CutPrefix(path, "testdata/")
		// Restore the .go extension: .input -> .go and
		// .input.golden -> .go.golden.
		name, ok := strings.CutSuffix(repoPath, ".input.golden")
		if ok {
			name += ".go.golden"
		} else {
			name, _ = strings.CutSuffix(repoPath, ".input")
			name += ".go"
		}
		out := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(out), 0777); err != nil {
			return err
		}
		return os.WriteFile(out, content, 0666)
	}
	err := fs.WalkDir(testdata, ".", walk)
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestSingleUseStrings(t *testing.T) {
	analysistest.Run(t, testdataDir(t), Analyzer, "singleuse")
}

func TestSuggestedFixes(t *testing.T) {
	analysistest.RunWithSuggestedFixes(t, testdataDir(t),
		Analyzer, "zero", "group", "block", "named", "makedecl", "preamble",
	)
}

// TestInlineBlock covers a shape gofmt erases, which is why it lives
// in testdata as .input: a one-line var block is reported as
// mergeable, and the fix is withheld because its spec shares the
// Lparen line.
func TestInlineBlock(t *testing.T) {
	analysistest.Run(t, testdataDir(t), Analyzer, "inline")
}
