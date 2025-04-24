// I don't want to bundle this mess: https://npmgraph.js.org/?q=util
// Moreover, node:util is used only here...
// https://github.com/eslint/eslintrc/blob/556e80029f01d07758ab1f5801bc9421bca4b072/lib/shared/config-validator.js#L126
// export * from 'util'

export function inspect() {
  return '<...>'
}
