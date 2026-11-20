// The pane's Launch view: the five workflows, then the chosen one's target step. No `$` here.
import { digit, flowRows, launchHints, runsNow, stepCommand, targetStep } from './pane'
import type { PaneActs } from './pane-acts'
import { row, section, tone } from './pane-kit'
import type { Drawn } from './pane-kit'
import { NARROW } from './pane'
import type { PaneUi, View } from '../types'

export function Launch(E: any, v: View, ui: PaneUi, acts: PaneActs, cols: number): Drawn {
  const { Box, Text, Button } = E
  const hints = launchHints(v, ui)
  if (!ui.step) {
    const rows = flowRows(v).map((f, i) => {
      const describe = cols >= NARROW ? f.describe : ''
      const main = f.enabled
        ? <Button key={'flow-' + f.flow} plain hotkey={digit(i + 1)} onPress={() => acts.pickFlow(f.flow)}>
            <Text bold={f.recommended}>{f.flow.padEnd(12)}</Text><Text dimColor>{describe}</Text>
          </Button>
        : <Text key={'flow-' + f.flow} color="subtle" wrap="truncate-end">{`${i + 1}: ${f.flow.padEnd(12)}${describe}`}</Text>
      return row(E, 'f-' + f.flow, main, f.meta, cols, f.recommended ? 'accent' : f.enabled ? 'dim' : 'faint')
    })
    return { content: [section(E, 'sec-flows', 'START A WORKFLOW', rows)], hints }
  }
  const flow = ui.step
  const step = targetStep(v, flow)
  const command = stepCommand(v, flow, ui.sel)
  const items = step.items.map((t, i) => {
    const chosen = i === ui.sel && t.enabled
    const text = [
      <Text key="pointer" color="claude">{chosen ? '❯ ' : '  '}</Text>,
      <Text key="glyph" {...tone(t.tone)}>{t.glyph} </Text>,
      <Text key="label" color={!t.enabled ? 'subtle' : chosen ? 'claude' : undefined} bold={chosen} wrap="truncate-end">{t.label}</Text>,
    ]
    const main = t.enabled
      ? <Button key={'target-' + i} plain hotkey={digit(i + 1)} onPress={() => acts.choose(i)}>{text}</Button>
      : <Box key={'target-' + i}><Text color="subtle">{`${i + 1}: `}</Text>{text}</Box>
    return row(E, 't-' + i, main, t.meta, cols, t.enabled ? 'dim' : 'faint')
  })
  const go = command
    ? <Box key="go" flexDirection="column">
        <Text key="command" color="claude" wrap="truncate-end">→ {command}</Text>
        <Box key="actions" gap={3}>
          <Button key="put" plain autoFocus label="put in prompt" onPress={() => acts.fill(command)} />
          {runsNow(flow) && <Button key="run" plain dimColor hotkey="r" label="run now" onPress={() => acts.run(command)} />}
          <Button key="back" plain dimColor label="back" onPress={() => acts.back()} />
        </Box>
      </Box>
    : <Text key="go" dimColor>Nothing here can start yet.</Text>
  return { content: [items.length > 0 ? section(E, 'sec-targets', step.question, items) : <Text key="targets" dimColor bold>{step.question}</Text>, go], hints }
}
