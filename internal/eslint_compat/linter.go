package eslint_compat

import (
	"fmt"
	"os"

	"github.com/microsoft/typescript-go/shim/ast"
	"github.com/microsoft/typescript-go/shim/compiler"
	"github.com/microsoft/typescript-go/shim/core"
	"github.com/typescript-eslint/tsgolint/internal/eslint_compat/config_loader"
	"github.com/typescript-eslint/tsgolint/internal/eslint_compat/estree"
	"github.com/typescript-eslint/tsgolint/internal/rule"

	goja "github.com/typescript-eslint/tsgolint/internal/eslint_compat/goju"
)

func CreateJsVm(script string) (*goja.Runtime, error) {
	vm := goja.New()
	vm.SetFieldNameMapper(goja.UncapFieldNameMapper())

	vm.Set("_tsgolint_log", vm.ToValue(func(message string) {
		fmt.Printf("Log from JS: %v\n", message)
	}))
	vm.Set("_tsgolint_suggestion", vm.ToValue(func(messageId, messageDescription, fixText string, fixRangeStart, fixRangeEnd int) rule.RuleSuggestion {
		return rule.RuleSuggestion{
			Message: rule.RuleMessage{
				Id:          messageId,
				Description: messageDescription,
			},
			FixesArr: []rule.RuleFix{
				{
					Text:  fixText,
					Range: core.NewTextRange(fixRangeStart, fixRangeEnd),
				},
			},
		}
	}))

	_, err := vm.RunString(script)
	return vm, err
}

func aaanalyze(ast *estree.Program, vm *goja.Runtime) {
	// a, ok := goja.AssertFunction(vm.Get("_tsgolint_analyze"))
	// if !ok {
	// 	panic("couldn't find _tsgolint_analyze global function")
	// }
	// _, err := a(
	// 	goja.Undefined(),
	// 	vm.ToValue(ast),
	// )
	// if err != nil {
	// 	panic(err)
	// }
}

func LintFile(vm *goja.Runtime, file *ast.SourceFile, onDiagnostic func(diagnostic rule.RuleDiagnostic)) error {
	run, ok := goja.AssertFunction(vm.Get("_tsgolint_run"))
	if !ok {
		panic("couldn't find _tsgolint_run global function")
	}

	ast := estree.ConvertSourceFileToESTree(file, vm)
	aaanalyze(ast, vm)
	text := file.Text
	if len(ast.Comments) >= 1 && ast.Comments[0].GetType() == estree.ESTreeTokenTypeShebang {
		text = "//" + text[ast.Comments[0].GetRange()[0]:]
	}

	lineMap := file.LineMap()

	_, err := run(
		goja.Undefined(),
		vm.ToValue(text),
		vm.ToValue(ast),
		vm.ToValue(func(
			ruleId string,
			line, column int,
			endLine, endColumn int,
			messageId, messageDescription string,
			fixText string,
			fixRangeStart, fixRangeEnd int,
			suggestions []rule.RuleSuggestion,
		) {
			// TODO: column is codepoint index, not byte index
			rangeStart := min(max(int(lineMap[min(max(line-1, 0), len(lineMap)-1)])+column-1, 0), len(file.Text))
			rangeEnd := min(max(int(lineMap[min(max(endLine-1, 0), len(lineMap)-1)])+endColumn-1, 0), len(file.Text))

			d := rule.RuleDiagnostic{
				RuleName: ruleId,
				Range:    core.NewTextRange(rangeStart, rangeEnd),
				Message: rule.RuleMessage{
					Id:          messageId,
					Description: messageDescription,
				},
				SourceFile: file,
			}
			if fixRangeStart != -1 {
				d.FixesPtr = &[]rule.RuleFix{
					{
						Text:  fixText,
						Range: core.NewTextRange(fixRangeStart, fixRangeEnd),
					},
				}
			}
			if len(suggestions) != 0 {
				d.Suggestions = suggestions
			}
			onDiagnostic(d)
		}))

	return err
}
func RunLinter(entryCode string, jsProfile string, program *compiler.Program, singleThreaded bool, files []*ast.SourceFile, onDiagnostic func(diagnostic rule.RuleDiagnostic)) error {
	bundle, err := config_loader.LoadESLintConfig(entryCode)
	if err != nil {
		return err
	}

	queue := make(chan *ast.SourceFile, len(files))
	for _, file := range files {
		queue <- file
	}
	close(queue)

	firstVm, err := CreateJsVm(bundle)
	if err != nil {
		return err
	}

	if jsProfile != "" {
		f, err := os.Create(jsProfile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error creating js profile file: %v\n", err)
		}
		defer f.Close()
		err = goja.StartProfile(f)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error starting js profiling: %v\n", err)
		}
		defer goja.StopProfile()
	}

	wg := core.NewWorkGroup(singleThreaded)
	for i := range program.GetTypeCheckers() {
		wg.Queue(func() {
			var vm *goja.Runtime
			if i == 0 {
				vm = firstVm
			} else {
				vm, err = CreateJsVm(bundle)
				if err != nil {
					// TODO: graceful exit
					panic(err)
				}
			}

			for file := range queue {
				err := LintFile(vm, file, onDiagnostic)
				if err != nil {
					// TODO: graceful exit
					panic(err)
				}
			}
		})
	}

	wg.RunAndWait()

	return nil
}
