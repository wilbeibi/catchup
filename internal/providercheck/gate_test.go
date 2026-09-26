package providercheck

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wilbeibi/catchup/internal/session"
)

// providerPackages maps every registered provider to its source directory under
// internal/. Adding a provider to session.Providers without adding it here, or
// without a test that calls Check, fails the gate below.
var providerPackages = map[string]string{
	session.ProviderAmp:      "amp",
	session.ProviderCodex:    "codex",
	session.ProviderClaude:   "claude",
	session.ProviderAgy:      "agy",
	session.ProviderCline:    "cline",
	session.ProviderCopilot:  "copilot",
	session.ProviderCursor:   "cursor",
	session.ProviderDeepSeek: "deepseek",
	session.ProviderGrok:     "grok",
	session.ProviderKimi:     "kimi",
	session.ProviderOpenCode: "opencode",
	session.ProviderPiAgent:  "piagent",
	session.ProviderZCode:    "zcode",
}

// TestEveryProviderRunsConformance is the gate: each provider must run the
// shared check from its own tests, which is what forces it to declare and
// demonstrate what its format records. The example that motivated it: a
// provider read a lossy file and shipped a handoff with no timestamps and no
// failures, invisible until someone read a real session.
func TestEveryProviderRunsConformance(t *testing.T) {
	if len(providerPackages) != len(session.Providers) {
		t.Fatalf("providerPackages covers %d providers, session.Providers lists %d", len(providerPackages), len(session.Providers))
	}
	for _, name := range session.Providers {
		pkg, ok := providerPackages[name]
		if !ok {
			t.Errorf("provider %q has no package mapping; add it here and a Check call in %s's tests", name, name)
			continue
		}
		if !callsCheck(t, filepath.Join("..", pkg)) {
			t.Errorf("provider %q never calls providercheck.Check in %s's tests; see docs/providers.md", name, pkg)
		}
	}
}

// sourceMarkers are the phrases a provider's package doc can use to name where
// its format came from. The convention already held in the corpus; this pins it
// so a parser cannot ship without saying what it is a parser of.
var sourceMarkers = []string{
	"source of truth", "format reference", "schema reference",
	"wire shapes", "derived from", "reverse-engineered from", "verified against",
}

// TestEveryProviderDocumentsItsSource requires each provider's package doc to
// name the format it reads and where that knowledge came from. A provider whose
// doc says nothing about its source is one whose parser was written from a
// guess, which is the failure this repository keeps paying for.
func TestEveryProviderDocumentsItsSource(t *testing.T) {
	for _, name := range session.Providers {
		pkg, ok := providerPackages[name]
		if !ok {
			t.Errorf("provider %q has no package mapping", name)
			continue
		}
		if !docHasSourceMarker(t, filepath.Join("..", pkg)) {
			t.Errorf("provider %q package doc names no format source; add a line such as \"Format reference: <where it was learned>\"", name)
		}
	}
}

// docHasSourceMarker reports whether any non-test file's package comment in dir
// carries one of the source markers.
func docHasSourceMarker(t *testing.T, dir string) bool {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}
		if f.Doc == nil {
			continue
		}
		doc := strings.ToLower(f.Doc.Text())
		for _, marker := range sourceMarkers {
			if strings.Contains(doc, marker) {
				return true
			}
		}
	}
	return false
}

// callsCheck parses a provider package's test files and reports whether any of
// them calls providercheck.Check.
func callsCheck(t *testing.T, dir string) bool {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, file := range files {
		f, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == "providercheck" && sel.Sel.Name == "Check" {
				found = true
			}
			return true
		})
	}
	return found
}
