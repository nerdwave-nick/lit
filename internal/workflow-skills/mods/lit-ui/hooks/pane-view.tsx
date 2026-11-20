// The lit pane's drawing: the shell (header, tabs, failure row, key hints) and the Home view.
// Elements and the acts come in; no `$` here (register.tsx owns every engine call).
import { band, left } from './band'
import { Launch } from './launch-view'
import { digit, home, NARROW, TABS, tabOf } from './pane'
import type { PaneActs } from './pane-acts'
import { row, section, tone } from './pane-kit'
import type { Drawn } from './pane-kit'
import type { PaneUi, StatusMode, View } from '../types'

export function PaneView(E: any, v: View | null, ui: PaneUi, mode: StatusMode, acts: PaneActs, cols: number) {
  const { Box, Text, Button } = E
  if (!v) return <Text dimColor>lit · loading…</Text>
  if (v.state !== 'ready' && v.state !== 'noproject') {
    // Not connected or not answering: the band's line and its one action.
    const m = band(v, mode)
    const fix = m.buttons[0]!
    return (
      <Box flexDirection="column" gap={1}>
        <Text key="state" {...tone(m.glyphTone)} wrap="truncate-end">{`${m.glyph} ${[m.lead, ...m.facts.map(f => f.text)].join(' · ')}`}</Text>
        <Button key="fix" plain autoFocus hotkey={fix.key} label={fix.label} onPress={() => acts.fix(fix.does)} />
        <Text key="hints" dimColor>{`Enter ${fix.label} · Esc close`}</Text>
      </Box>
    )
  }
  const tab = tabOf(v, ui)
  const tabs = (
    <Box key="tabs" gap={2}>
      {TABS.map(t => (v.state === 'noproject' && t.tab !== 'project'
        ? <Text key={'tab-' + t.tab} color="subtle">{t.label}</Text>
        : <Button key={'tab-' + t.tab} plain hotkey={t.key} onPress={() => acts.tab(t.tab)}>
            <Text bold={t.tab === tab} underline={t.tab === tab} dimColor={t.tab !== tab}>{t.label}</Text>
          </Button>))}
    </Box>
  )
  const body: Drawn = tab === 'home' ? Home(E, v, ui, acts, cols)
    : tab === 'launch' ? Launch(E, v, ui, acts, cols)
    : placeholder(E, tab === 'board' ? 'The board comes with a later version of lit-ui.'
      : 'Choosing and creating projects comes with a later version of lit-ui; lit session set --project selects one.')
  return (
    <Box flexDirection="column" gap={1}>
      <Box key="head" flexDirection={cols < NARROW ? 'column' : 'row'} justifyContent="space-between">
        <Box key="title" flexShrink={1}><Text dimColor>lit · </Text><Text bold wrap="truncate-end">{v.project?.title ?? 'no project'}</Text></Box>
        {tabs}
      </Box>
      {ui.failure && <Text key="failure" color="error" wrap="truncate-end">⚠ {ui.failure}</Text>}
      {body.content}
      <Text key="hints" dimColor wrap="truncate-end">{body.hints}</Text>
    </Box>
  )
}

const placeholder = (E: any, text: string): Drawn => ({ content: [<E.Text key="later" dimColor>{text}</E.Text>], hints: 'Esc close' })

function Home(E: any, v: View, ui: PaneUi, acts: PaneActs, cols: number): Drawn {
  const { Box, Text, Button } = E
  const m = home(v, ui)
  const command = m.next.command
  const parts: any[] = [section(E, 'sec-next', 'NEXT STEP', command ? [
    <Box key="command" gap={2}>
      <Box key="fill" flexShrink={1}>
        <Button key="next" plain autoFocus onPress={() => acts.fill(command)}><Text color="claude" bold wrap="truncate-end">❯ {command}</Text></Button>
      </Box>
      {m.next.runNow && <Button key="run" plain dimColor hotkey="r" label="run now" onPress={() => acts.run(command)} />}
    </Box>,
    <Box key="why" paddingLeft={2}><Text dimColor wrap="truncate-end">{m.next.reason}</Text></Box>,
  ] : [<Text key="why" dimColor>{m.next.reason}</Text>])]
  if (m.claims.length > 0) parts.push(section(E, 'sec-claims', 'MY CLAIMS', m.claims.flatMap(({ n, claim, warn, selected }) => {
    const id = claim.issueId
    const rows = [row(E, 'c-' + id,
      <Button key={'claim-' + id} plain hotkey={digit(n)} onPress={() => acts.selectClaim(id)}>
        <Text color={warn ? 'warning' : undefined} bold={selected}>◷ {claim.title}</Text>
      </Button>, left(claim.remainingMs), cols, warn ? 'warning' : 'dim')]
    if (selected) rows.push(
      <Box key={'ca-' + id} paddingLeft={4} gap={3}>
        <Button key={'renew-' + id} plain hotkey="r" label="renew" onPress={() => acts.renew(id)} />
        <Button key={'release-' + id} plain hotkey="x" label={ui.confirm === id ? 'press x again to release' : 'release'}
          onPress={() => acts.release(id)} />
      </Box>)
    return rows
  })))
  if (m.waiting.length > 0) parts.push(section(E, 'sec-waiting', 'WAITING FOR YOU', m.waiting.map(({ n, item }) => row(E, 'w-' + item.issue.id,
    <Button key={'wait-' + item.issue.id} plain hotkey={digit(n)} onPress={() => acts.fill(item.command)}>
      <Text color="warning">⚑ </Text><Text>{item.issue.title}</Text>
    </Button>, item.kind, cols))))
  return { content: parts, hints: m.hints }
}
