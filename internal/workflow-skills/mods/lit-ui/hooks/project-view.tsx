// The pane's Project view: CHOOSE A PROJECT and NEW PROJECT. No `$` here.
import { digit } from './pane'
import type { PaneActs } from './pane-acts'
import { row, section } from './pane-kit'
import type { Drawn } from './pane-kit'
import { projectChoices, validateTitle } from './project'
import type { PaneUi, View } from '../types'

const META = { current: 'current', repo: 'this repository', other: '' } as const

export function ProjectView(E: any, v: View, ui: PaneUi, acts: PaneActs, cols: number): Drawn {
  const { Box, Text, Button, Input } = E
  const { rows, more } = projectChoices(v, ui.allProjects)
  // The first row takes the focus, so Enter selects it; nothing is selected without a press.
  const first = (n: number) => (n === 1 ? (true as const) : undefined)
  const choices = rows.map(({ project: p, group }, i) => {
    const current = group === 'current'
    return row(E, 'p-' + p.id,
      <Button key={'project-' + p.id} plain autoFocus={first(i + 1)} hotkey={digit(i + 1)} onPress={() => acts.selectProject(p.id)}>
        <Text color={current ? 'claude' : undefined} bold={current} wrap="truncate-end">{current ? '● ' : '  '}{p.title}</Text>
      </Button>, META[group], cols)
  })
  if (more > 0) choices.push(row(E, 'p-more',
    <Button key="more" plain dimColor autoFocus={first(rows.length + 1)} hotkey={digit(rows.length + 1)} label="  more…" onPress={() => acts.more()} />,
    `${more} other ${more === 1 ? 'project' : 'projects'}`, cols))
  const problem = ui.typed ? validateTitle(ui.draft.trim(), v.prefixes, v.projects) : null
  const content = [
    section(E, 'sec-choose', 'CHOOSE A PROJECT', choices.length > 0 ? choices : [<Text key="none" dimColor>No projects yet.</Text>]),
    section(E, 'sec-new', 'NEW PROJECT', [
      <Box key="new-row" gap={1}>
        <Button key="new" plain hotkey="n" label="new" onPress={() => acts.focusTitle()} />
        <Box key="field" flexGrow={1}>
          <Input key="new-title" placeholder={`${v.prefixes[0] ?? 'feat'}/my-effort`} value={ui.draft} submitLabel="create"
            onInput={(text: string) => acts.setDraft(text)} onSubmit={(text: string) => acts.create(text)} />
        </Box>
      </Box>,
      <Box key="new-note" paddingLeft={2}>
        {problem
          ? <Text key="new-problem" color="error" wrap="wrap">{problem}</Text>
          : <Text key="new-help" dimColor wrap="truncate-end">Creates it, selects it and puts /planner in the prompt.</Text>}
      </Box>,
    ]),
  ]
  const n = rows.length + (more > 0 ? 1 : 0)
  const select = n === 0 ? '' : n === 1 ? '1 select · ' : `1–${Math.min(n, 9)} select · `
  return { content, hints: `${select}n new project · Esc close` }
}
