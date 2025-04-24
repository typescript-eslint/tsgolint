import { Linter } from 'eslint/universal'
import { analyze } from '@typescript-eslint/scope-manager'
import { visitorKeys } from '@typescript-eslint/visitor-keys'

let scopeManager
export function analyzeAst(ast) {
  // scopeManager =  analyze(ast, {
  //   sourceType: 'script',
  //   lib: [],
  // })
}

export function adapter(
  config,
  text,
  ast,
  onDiagnostic,
) {
  const linter = new Linter({
    configType: 'flat',
  })

  const messages = linter.verify(text, [
    ...config,
    {
      languageOptions: {
        parser: {
          parseForESLint(_, opts) {
            ast.setSourceType(opts.sourceType)
            const scopeManager = analyze(ast, {
              ecmaVersion: opts.ecmaVersion,
              sourceType: opts.sourceType,
              lib: [],
              globalReturn:
                opts.ecmaFeatures?.globalReturn ||
                opts.sourceType === 'commonjs',
              // TODO: looks like @typescript-eslint/parser do not propagate it. bug?
              impliedStrict: opts.ecmaFeatures?.impliedStrict,
            })
            return {
              ast,
              scopeManager: scopeManager,
              visitorKeys,
            }
          },
        },
      },
    },
  ])

  for (const msg of messages) {
    onDiagnostic(
      msg.ruleId,
      msg.line,
      msg.column,
      msg.endLine ?? msg.line,
      msg.endColumn ?? msg.column,
      msg.messageId ?? '',
      msg.message,
      msg.fix ? msg.fix.text : '',
      msg.fix ? msg.fix.range[0] : -1,
      msg.fix ? msg.fix.range[1] : -1,
      (msg.suggestions ?? []).map((s) =>
        _tsgolint_suggestion(
          s.messageId,
          s.desc,
          s.fix.text,
          s.fix.range[0],
          s.fix.range[1],
        ),
      ),
    )
  }
}
