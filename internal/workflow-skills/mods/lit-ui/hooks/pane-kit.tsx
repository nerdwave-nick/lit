// Drawing pieces the pane's views share: the design's tones, sections and rows. No `$` here.
import { NARROW } from './pane'
import type { PaneTone } from './pane'

const COLOR: Record<PaneTone, string | undefined> = {
  text: undefined, dim: undefined, faint: 'subtle', accent: 'claude', success: 'success', warning: 'warning', error: 'error',
}
// Text props for a tone of the design's vocabulary.
export const tone = (t: PaneTone) => (t === 'dim' ? { dimColor: true } : COLOR[t] ? { color: COLOR[t] } : {})

// What a view draws: its body, and the key hints that end it.
export type Drawn = { content: any[]; hints: string }

// A section: a dim bold label over its rows. A view leaves out a section with no rows.
export const section = (E: any, key: string, label: string, rows: any[]) =>
  <E.Box key={key} flexDirection="column"><E.Text dimColor bold>{label}</E.Text>{rows}</E.Box>

// A row: what it is on the left, meta on the right, dropped below NARROW body columns.
export const row = (E: any, key: string, main: any, meta: string, cols: number, metaTone: PaneTone = 'dim') =>
  <E.Box key={key} justifyContent="space-between">
    <E.Box flexShrink={1}>{main}</E.Box>
    {meta && cols >= NARROW && <E.Text key="meta" {...tone(metaTone)} wrap="truncate-end">  {meta}</E.Text>}
  </E.Box>
