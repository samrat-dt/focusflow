// Pure builders for the Projects and Archive screens (no I/O: the server passes everything in).

export const STAGES = ['backlog', 'next', 'in-progress', 'paused', 'review', 'done'];
const PRI = { high: 0, medium: 1, low: 2 };
const NEWEST = (a, b) => String(b.updated_at).localeCompare(String(a.updated_at));

const isAgent = t => t.kind === 'project_agent';
const isWork = t => !isAgent(t) && (t.kind ?? 'regular') === 'regular';

export const emptyCounts = () => Object.fromEntries(STAGES.map(s => [s, 0]));

function sortWork(list) {
  return [...list].sort((a, b) => (PRI[a.priority] ?? 9) - (PRI[b.priority] ?? 9) || NEWEST(a, b));
}

// `tasks`: server views (with live/terminal_open/...). `projects`: focusflow projects (all, archived too).
// `owners`: scheduled coordinators.
export function buildOverview({ tasks, projects, owners = [] }) {
  const bySlug = new Map(tasks.map(t => [t.slug, t]));
  const rows = [];
  for (const p of projects.filter(x => !x.archived)) {
    const own = sortWork(tasks.filter(t => isWork(t) && !t.archived && t.project === p.slug));
    const counts = emptyCounts();
    for (const t of own) if (counts[t.status] !== undefined) counts[t.status] += 1;
    const agentTask = p.agent_task_slug ? bySlug.get(p.agent_task_slug) : null;
    const owner = owners.find(o => o.project === p.slug && o.status !== 'retired') ?? null;
    rows.push({
      slug: p.slug, name: p.name, priority: p.priority, work_dir: p.work_dir, counts, total: own.length, runs: p.runs ?? { on: true, limit_minutes: null },
      agent: agentTask
        ? { slug: agentTask.slug, has_session: !!agentTask.has_session, live: !!agentTask.live, terminal_open: !!agentTask.terminal_open, headless_running: !!agentTask.headless_running }
        : null,
      schedule: owner ? { slug: owner.slug, every: owner.every, status: owner.status, next_wake_at: owner.next_wake_at } : null,
      tasks: own,
    });
  }
  rows.sort((a, b) => (PRI[a.priority] ?? 9) - (PRI[b.priority] ?? 9) || (a.slug < b.slug ? -1 : 1));
  const known = new Set(projects.map(p => p.slug));
  const floating = sortWork(tasks.filter(t => isWork(t) && !t.archived && (!t.project || !known.has(t.project))));
  return { projects: rows, floating };
}

// Archived projects (with their tasks) and archived tasks that live outside an archived project.
export function buildArchive({ tasks, projects }) {
  const archivedProjects = projects.filter(p => p.archived);
  const archivedSlugs = new Set(archivedProjects.map(p => p.slug));
  return {
    projects: archivedProjects.map(p => ({
      slug: p.slug, name: p.name, work_dir: p.work_dir,
      tasks: sortWork(tasks.filter(t => isWork(t) && t.project === p.slug)),
    })),
    tasks: sortWork(tasks.filter(t => isWork(t) && t.archived && !archivedSlugs.has(t.project))),
  };
}
