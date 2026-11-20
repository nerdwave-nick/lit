// The pane's Board view: read-only, so it draws no Buttons. No `$` here.
import { board } from './board'
import { row, section, tone } from './pane-kit'
import type { Drawn } from './pane-kit'
import type { View } from '../types'

// Below this many body columns the milestone bars are drawn half as wide.
const WIDE_BARS = 64

export function Board(E: any, v: View, cols: number): Drawn {
  const { Box, Text } = E
  const b = board(v, cols >= WIDE_BARS ? 24 : 12)
  const content: any[] = [<Text key="summary" dimColor wrap="truncate-end">{b.summary}</Text>]
  if (b.rows.length > 0) content.push(
    <Box key="rows" flexDirection="column">
      {b.rows.map((r, i) => row(E, 'r-' + i,
        <Box key="main"><Text key="glyph" {...tone(r.tone)}>{`${r.glyph}  `}</Text>
          <Text key="title" dimColor={r.glyph === '✓'} wrap="truncate-end">{r.title}</Text></Box>, r.meta, cols))}
    </Box>)
  if (b.chain.length > 0) content.push(section(E, 'sec-chain', 'LONGEST CHAIN', [<Text key="chain" wrap="wrap">{b.chain.join(' → ')}</Text>]))
  if (b.milestones.length > 0) content.push(
    <Box key="milestones" flexDirection="column">
      {b.milestones.map(m => row(E, 'm-' + m.id,
        <Box key="main">
          <Text key="title" bold wrap="truncate-end">{m.title}</Text>
          <Text key="gap">{'  '}</Text>
          {m.done && <Text key="done" color="success">{m.done}</Text>}
          {m.todo && <Text key="todo" color="subtle">{m.todo}</Text>}
        </Box>, m.meta, cols))}
    </Box>)
  return { content, hints: 'h l b p views · Esc close' }
}
