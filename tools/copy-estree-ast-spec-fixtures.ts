import * as os from 'node:os'
import * as fs from 'node:fs'
import * as path from 'node:path'
import * as child_process from 'node:child_process'
import { estreePkgPath } from './shared.ts'

const tmpdir = path.join(os.tmpdir(), 'tsgolint-eslint-ast-spec')

if (!fs.existsSync(tmpdir)) {
  child_process.spawnSync(
    'git',
    [
      'clone',
      '--single-branch',
      '--depth',
      '1',
      '--branch',
      'v8.30.1',
      'https://github.com/typescript-eslint/typescript-eslint',
      tmpdir,
    ],
    {
      stdio: 'inherit',
    },
  )
}

const destDirPath = path.join(estreePkgPath, 'fixtures')
const astSpecBasePath = path.join(tmpdir, './packages/ast-spec/src')
for (const sourcePath of fs.globSync(
  path.join(astSpecBasePath, './**/fixtures/*/fixture.{ts,tsx}'),
)) {
  const targetPath = path.join(
    destDirPath,
    path.relative(astSpecBasePath, sourcePath),
  )
  const dir = path.dirname(targetPath)
  if (!fs.existsSync(dir)) {
    fs.mkdirSync(dir, { recursive: true })
  }

  fs.copyFileSync(sourcePath, targetPath)
}
