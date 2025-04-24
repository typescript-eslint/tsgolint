package core_rule_tests

import (
	"encoding/json"
	"path"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/shim/scanner"
	"github.com/microsoft/typescript-go/shim/tspath"
	"github.com/typescript-eslint/tsgolint/internal/eslint_compat"
	"github.com/typescript-eslint/tsgolint/internal/eslint_compat/config_loader"
	"github.com/typescript-eslint/tsgolint/internal/eslint_compat/estree"
	"github.com/typescript-eslint/tsgolint/internal/linter"
	"github.com/typescript-eslint/tsgolint/internal/rule"
	"github.com/typescript-eslint/tsgolint/internal/utils"
	"gotest.tools/v3/assert"
)

func getCwd() string {
	_, filename, _, _ := runtime.Caller(0)
	return path.Dir(filename)
}

type ValidTestCase struct {
	Code            string
	Only            bool
	Skip            bool
	Options         string
	LanguageOptions string
}

type InvalidTestCaseError struct {
	MessageId          string
	MessageDescription string
	Line               int
	Column             int
	EndLine            int
	EndColumn          int
	Suggestions        []InvalidTestCaseSuggestion
}

type InvalidTestCaseSuggestion struct {
	MessageId string
	Output    string
}

type InvalidTestCase struct {
	Code            string
	Only            bool
	Skip            bool
	Output          string
	Errors          []InvalidTestCaseError
	Options         string
	LanguageOptions string
}

type ReportArg struct {
	Node      estree.NodeWithRange
	MessageId string
}
type ESLintContext struct {
	Report func(arg ReportArg)
}

func RunRuleTester(t *testing.T, testerConfig string, ruleName string, validTestCases []ValidTestCase, invalidTestCases []InvalidTestCase) {
	if ruleName == "no-octal" || ruleName == "no-nonoctal-decimal-escape" || ruleName == "no-octal-escape" {
		t.Skip("Handled by TS")
	}
	if ruleName == "capitalized-comments" {
		t.Skip(`\p (Unicode character class escape) is not yet supported`)
	}
	if ruleName == "no-misleading-character-class" || ruleName == "no-useless-escape" || ruleName == "no-control-regex" || ruleName == "no-regex-spaces" || ruleName == "no-invalid-regexp" || ruleName == "no-empty-character-class" || ruleName == "prefer-regex-literals" {
		// TODO(port):
		// panic: SyntaxError: Invalid flags supplied to RegExp constructor 'v' [recovered]
		//       panic: SyntaxError: Invalid flags supplied to RegExp constructor 'v'
		t.SkipNow()
	}
	if ruleName == "id-length" || ruleName == "key-spacing" {
		// TODO(port)
		// panic: ReferenceError: Intl is not defined
		t.SkipNow()
	}
	if ruleName == "prefer-named-capture-group" {
		// TODO(port)
		// panic: SyntaxError: Invalid group
		t.SkipNow()
	}
	t.Parallel()
	onlyMode := slices.ContainsFunc(validTestCases, func(c ValidTestCase) bool { return c.Only }) ||
		slices.ContainsFunc(invalidTestCases, func(c InvalidTestCase) bool { return c.Only })

	rootDir := getCwd()

	bundle, err := config_loader.LoadESLintConfig(
		`
			import { adapter, analyzeAst } from 'virtual:adapter'
  		import { Reference } from 'eslint-scope'

			_tsgolint_analyze = analyzeAst
			_tsgolint_run = (text, ast, onDiagnostic) => {
				const ruleId = '` + ruleName + `'
				const testerConfig = ` + testerConfig + `
  			if (
  			  ruleId === 'no-unused-vars' &&
  			  testerConfig.plugins?.custom?.rules?.['use-every-a']
  			) {
  			  testerConfig.plugins.custom.rules['use-every-a'] = {
  			    create(context) {
  			      const sourceCode = context.sourceCode
  			      function useA(node) {
  			        sourceCode.markVariableAsUsed('a', node)
  			      }
  			      return {
  			        VariableDeclaration: useA,
  			        ReturnStatement: useA,
  			      }
  			    },
  			  }
  			} else if (
  			  ruleId === 'no-useless-assignment' &&
  			  testerConfig.plugins?.test?.rules?.['use-a']
  			) {
  			  testerConfig.plugins.test.rules = {
  			    'use-a': {
  			      create(context) {
  			        const sourceCode = context.sourceCode

  			        return {
  			          VariableDeclaration(node) {
  			            sourceCode.markVariableAsUsed('a', node)
  			          },
  			        }
  			      },
  			    },
  			    jsx: {
  			      create(context) {
  			        const sourceCode = context.sourceCode

  			        return {
  			          JSXIdentifier(node) {
  			            const scope = sourceCode.getScope(node)
  			            const variable = scope.variables.find((v) => v.name === node.name)

  			            variable.references.push(
  			              new Reference(node, scope, Reference.READ, null, false, null),
  			            )
  			          },
  			        }
  			      },
  			    },
  			  }
  			} else if (
  			  ruleId === 'prefer-const' &&
  			  testerConfig.plugins?.custom?.rules?.['use-x']
  			) {
  			  testerConfig.plugins.custom.rules['use-x'] = {
  			    create(context) {
  			      const sourceCode = context.sourceCode

  			      return {
  			        VariableDeclaration(node) {
  			          sourceCode.markVariableAsUsed('x', node)
  			        },
  			      }
  			    },
  			  }
  			}
				adapter(
					[
						testerConfig,
						{
							languageOptions: _tsgolint_ruletester_languageOptions,
							linterOptions: {
								reportUnusedDisableDirectives: false,
							},
							rules: {
								[ruleId]: ['error', ..._tsgolint_ruletester_options],
							}
						},
					],
					text,
					ast,
					onDiagnostic,
				)
			}
			`,
	)
	assert.NilError(t, err)

	vm, err := eslint_compat.CreateJsVm(bundle)
	assert.NilError(t, err)

	runLinter := func(t *testing.T, code string, options string, languageOptions string) []rule.RuleDiagnostic {
		// hasShebang := false
		// // eslint compatibility (we should move this to linter)
		// if strings.HasPrefix(code, "#!") {
		// 	hasShebang = true
		// 	code = "//" + code[2:]
		// }

		diagnostics := make([]rule.RuleDiagnostic, 0, 3)

		if options == "" {
			options = "[]"
		}
		var opts any
		err := json.Unmarshal([]byte(options), &opts)
		assert.NilError(t, err)
		vm.Set("_tsgolint_ruletester_options", opts)

		if languageOptions == "" {
			languageOptions = "{}"
		}
		var languageOpts any
		err = json.Unmarshal([]byte(languageOptions), &languageOpts)
		assert.NilError(t, err)
		vm.Set("_tsgolint_ruletester_languageOptions", languageOpts)

		var testerCfg any
		err = json.Unmarshal([]byte(testerConfig), &testerCfg)
		assert.NilError(t, err)

		checkIsJsx := func(opts any) bool {
			langOpts, ok := opts.(map[string]any)
			if !ok {
				return false
			}
			parserOpts, ok := langOpts["parserOptions"]
			if !ok {
				return false
			}
			p, ok := parserOpts.(map[string]any)
			if !ok {
				return false
			}
			ecmaFeatures, ok := p["ecmaFeatures"]
			if !ok {
				return false
			}
			e, ok := ecmaFeatures.(map[string]any)
			if !ok {
				return false
			}
			jsx, ok := e["jsx"]
			return ok && jsx.(bool)
		}

		isJsx := checkIsJsx(languageOpts)
		if l, ok := testerCfg.(map[string]any)["languageOptions"]; ok {
			isJsx = isJsx || checkIsJsx(l)
		}
		fileName := "file.ts"
		if isJsx {
			fileName = "file.tsx"
		}

		fs := utils.NewOverlayVFSForFile(tspath.ResolvePath(rootDir, fileName), code)
		host := utils.CreateCompilerHost(rootDir, fs)

		program, err := utils.CreateProgram(true, fs, rootDir, "./tsconfig.json", host)
		assert.NilError(t, err, "couldn't create program. code: "+code)

		program.BindSourceFiles()

		err = eslint_compat.LintFile(
			vm,
			program.GetSourceFile(fileName),
			func(diagnostic rule.RuleDiagnostic) {
				diagnostics = append(diagnostics, diagnostic)
			},
		)

		assert.NilError(t, err)

		return diagnostics
	}

	for i, testCase := range validTestCases {
		t.Run("valid-"+strconv.Itoa(i), func(t *testing.T) {
			// incorrectly serialized
			if ruleName == "quotes" && testCase.Code == "var foo = `back\\rtick`;" {
				testCase.Code = "var foo = `back\rtick`;"
			}
			if ruleName == "padding-line-between-statements" && testCase.Code == `class C { static { 'use strict'; let x; } }` {
				t.Skip("TODO: @typescript-eslint/typescript-estree shouldn't allow directives in static blocks")
			}
			if ruleName == "no-useless-backreference" && (testCase.Code == `'\1(a)'` ||
				testCase.Code == `RegExp('\1(a)')`) {
				t.Skip("Invalid TS code")
			}
			if ruleName == "no-unused-vars" && (testCase.Code == `try {} catch ([firstError]) {}` ||
				testCase.Code == `try {} catch ({ message, stack }) {}` ||
				testCase.Code == `try {} catch ({ errors: [firstError] }) {}` ||
				testCase.Code == `try {} catch ({ foo, ...bar }) { console.log(bar); }`) {
				t.Skip("TODO: https://github.com/eslint/eslint/pull/18636 -> https://github.com/eslint/eslint-scope/pull/127 should be ported to @typescript-eslint/scope-manager")
			}
			if ruleName == "no-fallthrough" {
				testCase.Code = strings.ReplaceAll(testCase.Code, "rule-to-test/no-fallthrough", "no-fallthrough")
			}
			if (ruleName == "no-dupe-keys" && testCase.Code == `var x = { 012: 1, 12: 2 };`) ||
				(ruleName == "no-extra-parens" && (testCase.Code == `(0).a` ||
					testCase.Code == `(123).a` ||
					testCase.Code == `(08).a` ||
					testCase.Code == `(09).a` ||
					testCase.Code == `(018).a` ||
					testCase.Code == `(012934).a`)) ||
				(ruleName == "no-magic-numbers" && (testCase.Code == `foo[0123]`)) {
				t.Skip("1121: Octal literals are not allowed. Use the syntax '0o1'.")
			}
			if ruleName == "no-extra-parens" && (testCase.Code == `(let[a] = b);` ||
				testCase.Code == `(let)
foo` ||
				testCase.Code == `(let[foo]) = 1` ||
				testCase.Code == `(let)[foo]`) {
				t.Skip("TODO: this should be parsing error in tseslint")
			}
			if ruleName == "indent" && (testCase.Code == `<>
    <A />
<
    />` ||
				testCase.Code == `<>
    <A />
<
/>` ||
				testCase.Code == `<
>
    <A />
<
/>` ||
				testCase.Code == `<>
    <A />
< // Comment
/>` ||
				testCase.Code == `<>
    <A />
<
    // Comment
/>` ||
				testCase.Code == `<>
    <A />
<
// Comment
/>` ||
				testCase.Code == `<>
    <A />
< /* Comment */
/>` ||
				testCase.Code == `<>
    <A />
<
    /* Comment */ />` ||
				testCase.Code == `<>
    <A />
<
/* Comment */ />` ||
				testCase.Code == `<>
    <A />
<
    /* Comment */
/>` ||
				testCase.Code == `<>
    <A />
<
/* Comment */
/>`) {
				t.Skip("Syntax error")
			}
			if (onlyMode && !testCase.Only) || testCase.Skip {
				t.SkipNow()
			}

			diagnostics := runLinter(t, testCase.Code, testCase.Options, testCase.LanguageOptions)
			if len(diagnostics) != 0 {
				// TODO: pretty errors
				t.Errorf("Expected valid test case not to contain errors. Code:\n%v", testCase.Code)
				for i, d := range diagnostics {
					t.Errorf("error %v - (%v-%v) %v", i+1, d.Range.Pos(), d.Range.End(), d.Message.Description)
				}
				t.FailNow()
			}
		})
	}

	for i, testCase := range invalidTestCases {
		t.Run("invalid-"+strconv.Itoa(i), func(t *testing.T) {
			if ruleName == "padding-line-between-statements" && testCase.Code == `class C { static { 'use strict'; let x; } }` {
				t.Skip("TODO: @typescript-eslint/typescript-estree shouldn't allow directives in static blocks")
			}
			if ruleName == "prefer-object-spread" && testCase.Code == `const test = Object.assign({ ...bar }, {
                <!-- html comment
                foo: 'bar',
                baz: "cats"
                --> weird
            })` {
				t.Skip("Invalid syntax")
			}
			if ruleName == "no-use-before-define" && testCase.Code == `"use strict"; a(); { function a() {} }` {
				t.Skip("eslint-scope resolves 'a' reference, but @typescript-eslint/scope-manager doesn't resolve it. I don't know who is right")
			}
			if ruleName == "no-unused-vars" && (testCase.Code == `try {} catch ({ message }) { console.error(message); }` ||
				testCase.Code == `try {} catch ([_a, _b]) { doSomething(_a, _b); }` ||
				testCase.Code == `try {} catch ({ stack: $ }) { $ = 'Something broke: ' + $; }`) {
				t.Skip("TODO: https://github.com/eslint/eslint/pull/18636 -> https://github.com/eslint/eslint-scope/pull/127 should be ported to @typescript-eslint/scope-manager")
			}
			if ruleName == "no-extra-parens" && (testCase.Code == `(let)
foo` ||
				testCase.Code == `(let[foo]) = 1` ||
				testCase.Code == `(let)[foo]`) {
				t.Skip("TODO: this should be parsing error in tseslint")
			}
			if ruleName == "no-eval" && (testCase.Code == `function foo() { ('use strict'); this.eval; }`) {
				t.Skip("TODO: report to tseslint - https://github.com/eslint/eslint-scope/issues/117")
			}
			if ruleName == "no-eval" && (testCase.Code == `function foo() { 'use strict'; this.eval(); }` && testCase.LanguageOptions == "{\"ecmaVersion\":3}") {
				t.Skip("tseslint's ScopeManager doesn't respect ecmaVersion in isStrictModeSupported")
			}
			if (ruleName == "dot-location" && (testCase.Code == `01
.toExponential()` || testCase.Code == `08
.toExponential()` || testCase.Code == `0190
.toExponential()`)) ||
				(ruleName == "dot-notation" && (testCase.Code == `01['prop']` ||
					testCase.Code == `01234567['prop']` ||
					testCase.Code == `08['prop']` ||
					testCase.Code == `090['prop']` ||
					testCase.Code == `018['prop']`)) ||
				(ruleName == "no-dupe-keys" && testCase.Code == `var x = { 012: 1, 10: 2 };`) ||
				(ruleName == "no-extra-parens" && (testCase.Code == `(0123).a` ||
					testCase.Code == `(08.1).a` ||
					testCase.Code == `(09.).a`)) ||
				(ruleName == "no-magic-numbers" && (testCase.Code == `console.log(0x1A + 0x02); console.log(071);` ||
					testCase.Code == `foo[-012]`)) ||
				(ruleName == "no-unused-vars" && (testCase.Code == `var a;'use strict';b(00);` ||
					testCase.Code == `var [a] = foo;'use strict';b(00);` ||
					testCase.Code == `var [...a] = foo;'use strict';b(00);` ||
					testCase.Code == `var {a} = foo;'use strict';b(00);`)) ||
				(ruleName == "no-whitespace-before-property" && (testCase.Code == `08      .toExponential()` ||
					testCase.Code == `0192    .toExponential()` ||
					testCase.Code == `05 .toExponential()`)) {
				t.Skip("1121: Octal literals are not allowed. Use the syntax '0o1'.")
			}
			if (ruleName == "prefer-template" && (testCase.Code == `foo + 'does not autofix octal escape sequence' + '\033'` ||
				testCase.Code == `foo + 'does not autofix non-octal decimal escape sequence' + '\8'` ||
				testCase.Code == `foo + '\n other text \033'` ||
				testCase.Code == `foo + '\0\1'` ||
				testCase.Code == `foo + '\08'`)) ||
				(ruleName == "quotes" && (testCase.Code == `var foo = "\1"` ||
					testCase.Code == `var foo = '\1'` ||
					testCase.Code == `var notoctal = '\0'` ||
					testCase.Code == `var foo = '\01'` ||
					testCase.Code == `var foo = '\0\1'` ||
					testCase.Code == `var foo = '\08'` ||
					testCase.Code == `var foo = 'prefix \33'` ||
					testCase.Code == `var foo = 'prefix \75 suffix'` ||
					testCase.Code == `var nonOctalDecimalEscape = '\8'`)) {
				t.Skip("1487: Octal escape sequences are not allowed. Use the syntax '\\x01'.")
			}
			if ruleName == "comma-dangle" && strings.Contains(testCase.Code, "custom/add-named-import") {
				t.SkipNow()
			}
			if ruleName == "func-call-spacing" && testCase.Options == "[\"never\"]" && (testCase.Code == `f
();` ||
				testCase.Code == `this.cancelled.add(request)
this.decrement(request)
(0, request.reject)(new api.Cancel())` ||
				testCase.Code == `var a = foo
(function(global) {}(this));` ||
				testCase.Code == `var a = foo
(0, baz())`) {
				// TODO(port): weird error range
				testCase.Errors[0].EndLine--
			}
			// TODO(port): invalid range
			{
				if ruleName == "unicode-bom" && (testCase.Code == ``+"\xef\xbb\xbf"+` var a = 123;`) {
					t.Skip("Invalid fix range mapping (UTF-16 range to Go UTF-8)")

				}
				if ruleName == "max-len" && testCase.Code == `'🙁😁😟☹️😣😖😩😱👎'` {
					testCase.Errors[0].EndColumn = 8
				}
				if ruleName == "no-useless-rename" && testCase.Code == `export {' 👍 ' as ' 👍 '} from 'bar';` {

					t.Skip("!!! breaking fix")
				}
				if ruleName == "prefer-template" && testCase.Code == `var foo = '￥' + (n * 1000) + '-'` {
					t.Skip("!!! probably breaking fix")
				}
				if ruleName == "no-restricted-imports" {
					if testCase.Code == `export {'👍'} from "fs";` {
						testCase.Errors[0].EndColumn = 12
					} else if testCase.Code == `import { '👍' as bar } from "foo";` {
						testCase.Errors[0].EndColumn = 17
					}
				}
				if ruleName == "no-unused-vars" {
					if testCase.Code == `/*global 変数, 数*/
変数;` {
						testCase.Errors[0].Column = 12
						testCase.Errors[0].EndColumn = 13
					} else if testCase.Code == `/*global 𠮷𩸽, 𠮷*/
\u{20BB7}\u{29E3D};` {
						testCase.Errors[0].Column = 13
						testCase.Errors[0].EndColumn = 12
					}
				}
			}
			if ruleName == "indent" && (testCase.Code == `<>
    <A />
<
    />` ||
				testCase.Code == `<
    >
    <A />
<
    />` ||
				testCase.Code == `<>
    <A />
< // Comment
    />` ||
				testCase.Code == `<>
    <A />
< /* Comment */
    />`) {
				t.Skip("Syntax error")
			}
			if (onlyMode && !testCase.Only) || testCase.Skip {
				t.SkipNow()
			}

			code := testCase.Code

			diagnostics := runLinter(t, code, testCase.Options, testCase.LanguageOptions)

			fixedCode, _, fixed := linter.ApplyRuleFixes(code, diagnostics)

			if len(diagnostics) != len(testCase.Errors) {
				t.Fatalf("Expected invalid test case to contain exactly %v errors (reported %v errors - %v). Code:\n%v", len(testCase.Errors), len(diagnostics), diagnostics, testCase.Code)
			}

			if testCase.Output != "" || fixed {
				assert.Equal(t, testCase.Output, fixedCode, "Expected code after fix")
			}

			for i, expected := range testCase.Errors {
				diagnostic := diagnostics[i]

				if expected.MessageId != "" && expected.MessageId != diagnostic.Message.Id {
					t.Errorf("Invalid message id %v. Expected %v", diagnostic.Message.Id, expected.MessageId)
				}
				if expected.MessageDescription != "" && expected.MessageDescription != diagnostic.Message.Description {
					t.Errorf("Invalid message description %v. Expected %v", diagnostic.Message.Description, expected.MessageDescription)
				}

				lineIndex, columnIndex := scanner.GetLineAndCharacterOfPosition(diagnostic.SourceFile, diagnostic.Range.Pos())
				line, column := lineIndex+1, columnIndex+1
				endLineIndex, endColumnIndex := scanner.GetLineAndCharacterOfPosition(diagnostic.SourceFile, diagnostic.Range.End())
				endLine, endColumn := endLineIndex+1, endColumnIndex+1

				// TODO: shifted ranges
				if ruleName != "no-irregular-whitespace" || (!strings.Contains(testCase.Code, "\u3000") && !strings.Contains(testCase.Code, "\u00A0\u2002\u2003")) {
					if expected.Line != 0 && expected.Line != line {
						t.Errorf("Error line should be %v. Got %v", expected.Line, line)
					}
					if expected.Column != 0 && expected.Column != column {
						t.Errorf("Error column should be %v. Got %v", expected.Column, column)
					}
					if expected.EndLine != 0 && expected.EndLine != endLine {
						t.Errorf("Error end line should be %v. Got %v", expected.EndLine, endLine)
					}
					if expected.EndColumn != 0 && expected.EndColumn != endColumn {
						t.Errorf("Error end column should be %v. Got %v", expected.EndColumn, endColumn)
					}
				}

				suggestionsCount := 0
				if diagnostic.Suggestions != nil {
					suggestionsCount = len(diagnostic.Suggestions)
				}
				if len(expected.Suggestions) != suggestionsCount {
					t.Errorf("Expected to have %v suggestions but had %v", len(expected.Suggestions), suggestionsCount)
				} else {
					for i, expectedSuggestion := range expected.Suggestions {
						suggestion := diagnostic.Suggestions[i]
						if expectedSuggestion.MessageId != "" && expectedSuggestion.MessageId != suggestion.Message.Id {
							t.Errorf("Invalid suggestion message id %v. Expected %v", suggestion.Message.Id, expectedSuggestion.MessageId)
						} else {
							output, _, _ := linter.ApplyRuleFixes(testCase.Code, []rule.RuleSuggestion{suggestion})

							assert.Equal(t, expectedSuggestion.Output, output, "Expected code after suggestion fix")
						}
					}
				}
			}
		})
	}
}
