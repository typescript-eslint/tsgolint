import * as process from 'process'
import structuredClone from '@ungap/structured-clone'

export const WeakMap = Map
export const WeakSet = Set

export { process, structuredClone }

export const console = {
  log: (...args) => _tsgolint_log(args.map(arg => String(arg)).join(', '))
}
console.warn = console.log
console.error = console.log
