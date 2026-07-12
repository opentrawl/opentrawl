package federation

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

func TestFederationDefinesNoParallelResponseTypes(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), entry.Name(), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, declaration := range file.Decls {
			generic, ok := declaration.(*ast.GenDecl)
			if !ok || generic.Tok != token.TYPE {
				continue
			}
			for _, specification := range generic.Specs {
				typeSpec := specification.(*ast.TypeSpec)
				if strings.HasSuffix(typeSpec.Name.Name, "Response") || typeSpec.Name.Name == "SourceStatus" || typeSpec.Name.Name == "SearchHit" || typeSpec.Name.Name == "SourceFailure" || typeSpec.Name.Name == "SkippedSource" {
					t.Fatalf("hand-written federation fact type %s in %s", typeSpec.Name.Name, entry.Name())
				}
			}
		}
	}
}
