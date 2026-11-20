// What the Project view's presses do: select a project, list them all, and create one. The
// tracker commands go through the PaneIo built in register.tsx; this file decides what follows.
import type { PaneIo } from './pane-acts'
import { canonicalRepo, projectChoices, validateTitle } from './project'
import type { PaneUi } from '../types'

export type ProjectActs = {
  selectProject: (projectId: string) => void
  more: () => void
  focusTitle: () => void
  setDraft: (text: string) => void
  create: (text: string) => void
}

const home = (ui: PaneUi): PaneUi => ({ ...ui, tab: 'home', step: null, allProjects: false })

export function projectActs(io: PaneIo): ProjectActs {
  // Only the person's press selects: `session set` with the project's ID, then Home.
  const select = async (projectId: string) => {
    const project = (await io.view())?.projects.find(p => p.id === projectId)
    if (!project) return
    if (!(await io.tracker([{ what: 'select', args: ['session', 'set', '--project', project.id] }], `selected ${project.title}`))) return
    await io.patch(home)
    io.focus('next')
  }
  // Created with this checkout's repository when it has one, selected by its exact title, then
  // the planner is put in the prompt and the pane closes. A refused title stays in the field.
  const create = async (text: string) => {
    const title = text.trim()
    const v = await io.view()
    if (!v) return
    if (validateTitle(title, v.prefixes, v.projects) !== null) return io.patch(ui => ({ ...ui, draft: text, typed: true }))
    const repo = v.origin ? canonicalRepo(v.origin) : null
    const made = await io.tracker([
      { what: 'create project', args: ['projects', 'create', '--project-title', title, ...(repo ? ['--repository', repo] : [])] },
      { what: 'select', args: ['session', 'set', '--project', `title:${title}`] },
    ], `created and selected ${title}`)
    if (!made) return
    await io.patch(ui => ({ ...home(ui), draft: '', typed: false }))
    await io.close()
    await io.fill(`/planner ${title}`)
  }
  return {
    selectProject: projectId => void select(projectId),
    // The ring moves to the first project more… brought, where the more… row was.
    more: () => void io.patch(ui => ({ ...ui, allProjects: true })).then(async () => {
      const v = await io.view()
      const first = v && projectChoices(v, true).rows.find(r => r.group === 'other')
      if (first) io.focus(`project-${first.project.id}`)
    }),
    focusTitle: () => io.focus('new-title'),
    setDraft: text => void io.patch(ui => ({ ...ui, draft: text, typed: true })),
    create: text => void create(text),
  }
}
