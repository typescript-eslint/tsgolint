package config_loader

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/evanw/esbuild/pkg/api"
)

var unhandledNodeBuiltinModulesRe = "^(node:)?(assert|assert/strict|async_hooks|buffer|child_process|cluster|console|constants|crypto|dgram|diagnostics_channel|dns|dns/promises|domain|events|fs|fs/promises|http|http2|https|inspector|inspector/promises|module|net|os|perf_hooks|punycode|querystring|readline|readline/promises|repl|stream|stream/consumers|stream/promises|stream/web|string_decoder|sys|timers|timers/promises|tls|trace_events|tty|url|util/types|v8|vm|wasi|worker_threads|zlib)$"

//go:generate go run generate_bundle.go

//go:embed adapter.js
var adapter string

//go:embed inject/inject_shim_generated.js
var injectShim string

//go:embed inject/path_generated.js
var pathPolyfill string

//go:embed inject/process_generated.js
var processPolyfill string

//go:embed inject/util.js
var utilPolyfill string

func LoadESLintConfig(entryCode string) (string, error) {
	result := api.Build(api.BuildOptions{
		EntryPoints: []string{"virtual:entry"},
		Outfile:     "tsgolint-bundle.js",
		Bundle:      true,
		Write:       false,
		MinifyWhitespace: true,
		MinifyIdentifiers: true,
		MinifySyntax: true,
		KeepNames: true,
		MangleQuoted:api.MangleQuotedTrue,
		TreeShaking: api.TreeShakingTrue,
		External:    []string{"typescript"},
		Inject:      []string{"virtual:inject_shim"},
		Supported: map[string]bool{
			"async-await":     false,
			"async-generator": false,
			"class-field":     false,
		},
		Plugins: []api.Plugin{
			{
				Name: "tsgolint-main",
				Setup: func(pb api.PluginBuild) {
					// TODO: https://github.com/dop251/goja/issues/659
					configCommentParserShim := `
class ConfigCommentParser extends _ConfigCommentParser {
	parseListConfig(string) {
		const items = {};

		string.split(",").forEach(name => {
			const trimmedName = name
				.trim()
				.replace(/^"(.+)"$/, '$1').replace(/^'(.+)'$/, '$1');

			if (trimmedName) {
				items[trimmedName] = true;
			}
		});

		return items;
	}
}
`
					redirectImportResolution := func(from, to string) {
						pb.OnResolve(api.OnResolveOptions{Filter: from}, func(args api.OnResolveArgs) (api.OnResolveResult, error) {
							resolved := pb.Resolve(to, api.ResolveOptions{
								Kind:       api.ResolveJSImportStatement,
								ResolveDir: ".",
							})
							if len(resolved.Errors) != 0 {
								return api.OnResolveResult{}, errors.New(resolved.Errors[0].Text)
							}
							return api.OnResolveResult{
								Path:      resolved.Path,
								Namespace: "redirected",
							}, nil
						})
					}
					redirectLoad := func(filter api.OnLoadOptions, contents string) {
						pb.OnLoad(filter, func(args api.OnLoadArgs) (api.OnLoadResult, error) {
							return api.OnLoadResult{
								ResolveDir: ".",
								Contents:   &contents,
							}, nil
						})
					}
					modifyTextOnLoad := func(filter api.OnLoadOptions, modify func(code string) string) {
						pb.OnLoad(filter, func(args api.OnLoadArgs) (api.OnLoadResult, error) {
							f, err := os.ReadFile(args.Path)
							if err != nil {
								return api.OnLoadResult{}, err
							}
							contents := modify(string(f))
							return api.OnLoadResult{
								Contents: &contents,
							}, nil
						})
					}

					modifyTextOnLoad(api.OnLoadOptions{Filter: "@eslint/plugin-kit/dist/cjs/index.cjs$"}, func(code string) string {
						return strings.ReplaceAll(code, "ConfigCommentParser", "_ConfigCommentParser") +
							configCommentParserShim +
							"\nexports.ConfigCommentParser = ConfigCommentParser;\n"
					})
					modifyTextOnLoad(api.OnLoadOptions{Filter: "@eslint/plugin-kit/dist/esm/index.js$"}, func(code string) string {
						return strings.ReplaceAll(code, "ConfigCommentParser", "_ConfigCommentParser") +
							configCommentParserShim +
							"\nexport {ConfigCommentParser}\n"
					})
					modifyTextOnLoad(api.OnLoadOptions{Filter: "eslint/lib/languages/js/source-code/source-code.js$"}, func(code string) string {
						return strings.Replace(code, "node.parent = parent;", "node.setParent(parent);", 1)
					})

					pb.OnLoad(api.OnLoadOptions{Filter: "virtual:entry"}, func(args api.OnLoadArgs) (api.OnLoadResult, error) {
						// // path is resolved to eslint/lib/api.js
						// eslintEntry := pb.Resolve("eslint", api.ResolveOptions{
						// 	ResolveDir: ".",
						// 	Kind:       api.ResolveJSImportStatement,
						// })
						// if len(eslintEntry.Errors) > 0 {
						// 	msg := ""
						// 	for _, e := range eslintEntry.Errors {
						// 		msg += e.Text + "\n"
						// 	}
						// 	return api.OnLoadResult{}, errors.New(msg)
						// }
						// rulesPath := path.Join(eslintEntry.Path, "../rules/index.js")
						// TODO(perf): treeshaking -- import only used rules

						return api.OnLoadResult{
							ResolveDir: ".",
							Contents:   &entryCode,
						}, nil
					})

					// ESLint exports rules from eslint/lib/rules/index.js via
					// `module.exports = new LazyLoadingRuleMap(... rules ...)`
					// esbuild wraps this default export in __toESM + __copyProps,
					// this breaks LazyLoadingRuleMap prototype chain, so rules.get(...)
					// fails even in Node.js. To avoid mess with prototype chains, we just
					// replace `class LazyLoadingRuleMap extends Map` with `function LazyLoadingRuleMap`
					redirectLoad(api.OnLoadOptions{Filter: "/eslint/lib/rules/utils/lazy-loading-rule-map.js$"}, `
						export function LazyLoadingRuleMap(l) {
							const mapping = Object.fromEntries(l.map(([id, load]) => [id, load()]))
							return {
								mapping,
								get(ruleId) {
									return mapping[ruleId]
								},
							}
						}
					`)

					pb.OnResolve(api.OnResolveOptions{Filter: "virtual:.+"}, func(args api.OnResolveArgs) (api.OnResolveResult, error) {
						return api.OnResolveResult{
							Path:      args.Path,
							Namespace: "virtual",
						}, nil
					})

					pb.OnResolve(api.OnResolveOptions{Filter: unhandledNodeBuiltinModulesRe}, func(args api.OnResolveArgs) (api.OnResolveResult, error) {
						return api.OnResolveResult{}, fmt.Errorf("Unsupported module %v (imported from %v)", args.Path, args.Importer)
					})

					redirectImportResolution("^(node:)?path(/posix|/win32)?$", "virtual:path")
					redirectImportResolution("^(node:)?util$", "virtual:util")
					redirectImportResolution("^(node:)?process$", "virtual:process")

					redirectLoad(api.OnLoadOptions{Filter: "virtual:path"}, pathPolyfill)
					redirectLoad(api.OnLoadOptions{Filter: "virtual:util"}, utilPolyfill)
					redirectLoad(api.OnLoadOptions{Filter: "virtual:process"}, processPolyfill)

					redirectLoad(api.OnLoadOptions{Filter: "virtual:inject_shim"}, injectShim)
					redirectLoad(api.OnLoadOptions{Filter: "virtual:adapter"}, adapter)
				},
			},
		},
	})

	if len(result.Errors) > 0 {
		msg := ""
		for _, e := range api.FormatMessages(result.Errors, api.FormatMessagesOptions{Kind: api.ErrorMessage}) {
			msg += e
		}
		return "", errors.New(msg)
	}

	if len(result.OutputFiles) == 0 {
		return "", errors.New("bundled ESLint config is not present in outputs")
	}

	bundle := result.OutputFiles[0].Contents

	return string(bundle), nil
}
