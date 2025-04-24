package linter

import (
	"slices"
	"strings"

	"github.com/typescript-eslint/tsgolint/internal/rule"
)

type LintMessage interface {
	Fixes() []rule.RuleFix
}

func ApplyRuleFixes[M LintMessage](code string, diagnostics []M) (string, []M, bool) {
	unapplied := []M{}
	withFixes := []M{}

	fixed := false

	for _, diagnostic := range diagnostics {
		if len(diagnostic.Fixes()) > 0 {
			slices.SortFunc(diagnostic.Fixes(), func(a rule.RuleFix, b rule.RuleFix) int {
				start := a.Range.Pos() - b.Range.Pos()
				if start == 0 {
					return a.Range.End() - b.Range.End()
				}
				return start
			})
			withFixes = append(withFixes, diagnostic)
		} else {
			unapplied = append(unapplied, diagnostic)
		}
	}

	slices.SortFunc(withFixes, func(a M, b M) int {
		aFixes, bFixes := a.Fixes(), b.Fixes()

		start := aFixes[0].Range.Pos() - bFixes[0].Range.Pos()
		if start == 0 {
			return aFixes[len(aFixes)-1].Range.End() - bFixes[len(bFixes)-1].Range.End()
		}
		return start
	})

	var builder strings.Builder

	lastFixEnd := 0
	for _, diagnostic := range withFixes {
		fixes := diagnostic.Fixes()
		if lastFixEnd != 0 && lastFixEnd >= fixes[0].Range.Pos() {
			unapplied = append(unapplied, diagnostic)
			continue
		}

		for _, fix := range fixes {
			fixed = true

			builder.WriteString(code[lastFixEnd:fix.Range.Pos()])
			builder.WriteString(fix.Text)

			lastFixEnd = fix.Range.End()
		}
	}

	builder.WriteString(code[lastFixEnd:])

	return builder.String(), unapplied, fixed
}
