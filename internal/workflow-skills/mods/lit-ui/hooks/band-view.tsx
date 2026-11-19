// The band's drawing: one truncating line of facts, plain buttons at the right. Elements and the
// press handler come in; no `$` here (register.tsx owns every engine call).
import type { BandAction, BandModel, Tone } from './band'

const COLOR: Record<Tone, string | undefined> = { text: undefined, dim: undefined, warning: 'warning', error: 'error' }
const tone = (t: Tone) => (t === 'dim' ? { dimColor: true } : COLOR[t] ? { color: COLOR[t] } : {})

// `working`: a turn runs, so the buttons hide and the facts stay.
export function BandView(E: any, m: BandModel, working: boolean, press: (does: BandAction) => void) {
  const { Box, Text, Button } = E
  const parts = [...(m.lead ? [{ text: m.lead, tone: 'text' as Tone, bold: m.leadBold }] : []), ...m.facts.map(f => ({ ...f, bold: false }))]
  return (
    <Box justifyContent="space-between">
      <Box flexShrink={1}>
        {m.glyph && <Text key="glyph" {...tone(m.glyphTone)}>{m.glyph} </Text>}
        {parts.map((p, i) => (
          <Box key={'fact' + i} flexShrink={i === 0 ? 1 : 0}>
            {i > 0 && <Text dimColor> · </Text>}
            <Text {...tone(p.tone)} bold={p.bold} wrap="truncate-end">{p.text}</Text>
          </Box>
        ))}
      </Box>
      {!working && (
        <Box flexShrink={0} gap={2} marginLeft={2}>
          {m.buttons.map(b => <Button key={'band-' + b.key} plain hotkey={b.key} label={b.label} onPress={() => press(b.does)} />)}
        </Box>
      )}
    </Box>
  )
}
