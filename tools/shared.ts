import { Project } from 'ts-morph'
import * as fs from 'node:fs'
import * as path from 'node:path'
import { fileURLToPath } from 'node:url'

const dirname = path.dirname(fileURLToPath(import.meta.url))

export const repoRootPath = path.join(dirname, '..')
export const eslintCompatPkgPath = path.join(
  repoRootPath,
  './internal/eslint_compat',
)
export const estreePkgPath = path.join(eslintCompatPkgPath, './estree')

export function collectConvertableAstNodes() {
  const project = new Project({
    tsConfigFilePath: path.join(dirname, 'tsconfig.json'),
  })

  const src = fs.readFileSync(
    path.join(
      repoRootPath,
      'node_modules/@typescript-eslint/types/dist/generated/ast-spec.d.ts',
    ),
    'utf8',
  )
  const astSpec = project.createSourceFile('./ast-spec.ts', src)

  const aliases: Record<string, string> = {
    LetOrConstOrVarDeclarationBase: 'VariableDeclaration',
    TSAbstractAccessorPropertyComputedName: 'TSAbstractAccessorProperty',
    AccessorPropertyComputedName: 'AccessorProperty',
    TSAbstractPropertyDefinitionComputedName: 'TSAbstractPropertyDefinition',
    TSAbstractMethodDefinitionComputedName: 'TSAbstractMethodDefinition',
  }

  return astSpec
    .getInterfaces()
    .filter((decl) => {
      if (
        [
          'Position',
          'SourceLocation',
          'NodeOrTokenData',

          'MethodDefinitionComputedName',
          'MethodDefinitionNonComputedName',
          'MethodDefinitionComputedNameBase',
          'MethodDefinitionNonComputedNameBase',

          'PropertyDefinitionComputedName',
          'PropertyDefinitionNonComputedName',
          'PropertyDefinitionComputedNameBase',
          'PropertyDefinitionNonComputedNameBase',

          'UsingDeclarationBase',

          'TSAbstractAccessorPropertyNonComputedName',
          'AccessorPropertyNonComputedName',
          'TSAbstractPropertyDefinitionNonComputedName',
          'TSAbstractMethodDefinitionNonComputedName',

          'UnaryExpressionBase',
        ].includes(decl.getName()) ||
        decl.getName().endsWith('ToText')
      ) {
        return false
      }

      const interfaceExtends = decl.getExtends()
      if (
        interfaceExtends.length &&
        interfaceExtends[0].getText().endsWith('Base') &&
        !decl.getProperties().some((p) => p.getName() === 'type') &&
        !['BigIntLiteral', 'RegExpLiteral'].includes(decl.getName())
      ) {
        return false
      }

      return true
    })
    .map((decl) => {
      let interfaceName = decl.getName()
      if (interfaceName in aliases) {
        interfaceName = aliases[interfaceName]
      }

      if (interfaceName.endsWith('Base')) {
        interfaceName = interfaceName.slice(0, -4)
      }

      return {
        interfaceName,
        decl,
      }
    })
}
