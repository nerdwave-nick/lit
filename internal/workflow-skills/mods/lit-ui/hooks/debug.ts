// What `/lit-ui debug` prints. Pure: register.tsx gathers the parts.
import { CONFIG_FILE } from './config'
import type { Config, RefreshInfo, View } from '../types'

export function debugText(v: View | null, r: RefreshInfo | null, config: Config, problems: (string | null)[]): string {
  return [
    `endpoint: ${v?.endpoint ?? 'unknown'}`,
    `status mode: ${config.mode} (${config.source === 'file' ? CONFIG_FILE : 'development default'}; lit: ${config.lit})`,
    `lit session: ${r?.session ?? 'none'}`,
    `state: ${v?.state ?? 'unknown'}`,
    `stage: ${v?.stage ?? 'none'}`,
    `last refresh: ${r ? `${r.at}, ${r.ms} ms` : 'never'}`,
    ...[...(v?.errors ?? []), ...problems].filter(Boolean).map(err => `error: ${err}`),
  ].join('\n')
}
