import { parse } from '@typescript-eslint/typescript-estree'
import * as fs from 'node:fs'
import * as path from 'node:path'
import { estreePkgPath } from './shared.ts'

function stringify(v: unknown): string {
  return JSON.stringify(
    v,
    (k, v) => {
      if (v instanceof RegExp) {
        return 'RegExp:' + v.toString()
      } else if (typeof v === 'bigint') {
        return 'bigint:' + v.toString()
      }

      return v
    },
    2,
  )
}

for (const fixturePath of fs.globSync(
  path.join(estreePkgPath, './fixtures/**/fixtures/*/fixture.{ts,tsx}'),
)) {
  const { tokens, comments, ...ast } = parse(
    fs.readFileSync(fixturePath, 'utf8'),
    {
      loc: true,
      range: true,
      tokens: true,
      comment: true,
      jsx: fixturePath.endsWith('.tsx'),
    },
  )

  const snapshotsDir = path.join(path.dirname(fixturePath), 'snapshots')
  if (!fs.existsSync(snapshotsDir)) {
    fs.mkdirSync(snapshotsDir)
  }

  fs.writeFileSync(path.join(snapshotsDir, 'tsestree.snap'), stringify(ast))
  fs.writeFileSync(
    path.join(snapshotsDir, 'tsestree-tokens.snap'),
    stringify(tokens),
  )
  fs.writeFileSync(
    path.join(snapshotsDir, 'tsestree-comments.snap'),
    stringify(comments),
  )
}
