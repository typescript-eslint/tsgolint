package estree

import (
	"encoding/json"
	"io/fs"
	"os"
	"path"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/shim/binder"
	"github.com/microsoft/typescript-go/shim/core"
	"github.com/microsoft/typescript-go/shim/parser"
	"github.com/microsoft/typescript-go/shim/scanner"
	"github.com/microsoft/typescript-go/shim/tspath"

	goja "github.com/typescript-eslint/tsgolint/internal/eslint_compat/goju"
	"gotest.tools/v3/assert"
)

//go:generate node --run tools:gen-baseline-ast-spec-snapshots

func unifyASTs(v any) any {
	switch val := v.(type) {
	case map[string]any:
		cleaned := map[string]any{}
		for k, v := range val {
			if k == "tokens" || k == "comments" {
				continue
			}
			if v, ok := v.(string); ok && k == "value" {
				if strings.HasPrefix(v, "RegExp:") {
					continue
				}
			}
			cleanedValue := unifyASTs(v)
			if cleanedValue != nil {
				cleaned[k] = cleanedValue
			}
		}
		return cleaned
	case []any:
		cleanedSlice := make([]any, 0, len(val))
		for _, item := range val {
			cleanedItem := unifyASTs(item)
			cleanedSlice = append(cleanedSlice, cleanedItem)
		}
		return cleanedSlice
	default:
		return val
	}
}

func JSONRemarshal(t *testing.T, bytes []byte) string {
	var raw any
	err := json.Unmarshal(bytes, &raw)
	assert.NilError(t, err)

	raw = unifyASTs(raw)

	res, err := json.MarshalIndent(raw, "", "  ")
	assert.NilError(t, err)
	return string(res)
}

func TestConverter(t *testing.T) {
	currentDirectory, err := os.Getwd()
	assert.NilError(t, err)
	currentDirectory = tspath.NormalizePath(currentDirectory)

	vm := goja.New()
	vm.SetFieldNameMapper(goja.UncapFieldNameMapper())
	_, err = vm.RunString(`stringifyAst = (ast) => JSON.stringify(ast, (k, v) => {
  if (v instanceof RegExp) {
    return "RegExp:" + v.toString()
  } else if (typeof v === 'bigint') {
    return "bigint:" + v.toString()
  }

  return v
}, 2)`)
	assert.NilError(t, err)

	// TODO: make it work with Windows paths
	fixturesFs := os.DirFS("./fixtures")
	fs.WalkDir(fixturesFs, ".", func(p string, d fs.DirEntry, err error) error {
		if !d.Type().IsRegular() || !strings.HasSuffix(p, "/tsestree.snap") {
			return nil
		}

		t.Run(strings.ReplaceAll(path.Join(p, "../.."), "/", "_"), func(t *testing.T) {
			baselineAst, err := fs.ReadFile(fixturesFs, p)
			assert.NilError(t, err)
			baselineTokens, err := fs.ReadFile(fixturesFs, path.Join(p, "../tsestree-tokens.snap"))
			assert.NilError(t, err)
			baselineComments, err := fs.ReadFile(fixturesFs, path.Join(p, "../tsestree-comments.snap"))
			assert.NilError(t, err)

			dir := path.Dir(path.Dir(p))
			fixturePath := path.Join(dir, "fixture.ts")
			if _, err := fs.Stat(fixturesFs, fixturePath); err != nil {
				fixturePath = path.Join(dir, "fixture.tsx")
			}

			fileName := tspath.GetNormalizedAbsolutePath(fixturePath, currentDirectory)
			filePath := tspath.ToPath(fileName, currentDirectory, true)

			fixture, err := fs.ReadFile(fixturesFs, fixturePath)
			assert.NilError(t, err, "couldn't read fixture")

			src := string(fixture)
			sourceFile := parser.ParseSourceFile(fileName, filePath, src, core.ScriptTargetESNext, scanner.JSDocParsingModeParseAll)
			binder.BindSourceFile(sourceFile, &core.CompilerOptions{})

			programAst := ConvertSourceFileToESTree(sourceFile, vm)

			tokens := programAst.Tokens
			comments := programAst.Comments
			programAst.Tokens = nil
			programAst.Comments = nil
			vm.Set("programAst", programAst)
			vm.Set("tokens", tokens)
			vm.Set("comments", comments)
			convertedAst, err := vm.RunString("stringifyAst(programAst)")
			assert.NilError(t, err)
			convertedTokens, err := vm.RunString("stringifyAst(tokens)")
			assert.NilError(t, err)
			convertedComments, err := vm.RunString("stringifyAst(comments)")
			assert.NilError(t, err)

			t.Run("ast", func(t *testing.T) {
				assert.Equal(t, JSONRemarshal(t, baselineAst), JSONRemarshal(t, []byte(convertedAst.String())))
			})
			t.Run("tokens", func(t *testing.T) {
				assert.Equal(t, JSONRemarshal(t, baselineTokens), JSONRemarshal(t, []byte(convertedTokens.String())))
			})
			t.Run("comments", func(t *testing.T) {
				assert.Equal(t, JSONRemarshal(t, baselineComments), JSONRemarshal(t, []byte(convertedComments.String())))
			})
		})

		return nil
	})
}
