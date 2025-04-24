//go:build ignore

package main

import (
	"fmt"
	"os"

	"github.com/evanw/esbuild/pkg/api"
)

func bundle(entry, out string, external ...string) {
	result := api.Build(api.BuildOptions{
		EntryPoints: []string{entry},
		Outfile:     out,
		Bundle:      true,
		Write:       true,
		External:    external,
		TreeShaking: api.TreeShakingTrue,
		Format:      api.FormatESModule,
	})
	if len(result.Errors) > 0 {
		for _, msg := range api.FormatMessages(result.Errors, api.FormatMessagesOptions{
			Kind: api.ErrorMessage,
		}) {
			fmt.Fprintln(os.Stderr, msg)
		}
		fmt.Fprintf(os.Stderr, "\nFound %v errors\n\n", len(result.Errors))
		os.Exit(1)
	}
}

func main() {
	bundle("./inject/path.js", "./inject/path_generated.js")
	bundle("./inject/process.js", "./inject/process_generated.js")
	bundle("./inject/inject_shim.js", "./inject/inject_shim_generated.js", "process")
}
