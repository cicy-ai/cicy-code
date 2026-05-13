# Skill UI Design

The cicy-code app (React/Vite SPA at `~/projects/cicy-code/app/`) gets a
new top-level page: **Skills**. Users see every available skill, what's
installed, and what they can install with one click.

This document specifies the page structure, components, behavior, and
data flow. Implementer follows it; design has no surprises.

---

## 1. Navigation entry

Add an icon to the existing left-rail nav (likely lucide `Package` icon):

```
[Logo]
─────
 Team
 Skills      ← new
 Settings
─────
```

Route: `/skills`. React Router entry already exists; add it.

The icon shows a small dot badge when any skill has `needs_attention > 0`.

---

## 2. Page layout

```
┌────────────────────────────────────────────────────────────────────────┐
│ Skills                                              [⟳ Sync]  [↻ All]  │
│                                                                        │
│ ┌────────────────────────────────────────────────────────────────────┐ │
│ │ 🔍  Search skills…                                                  │ │
│ └────────────────────────────────────────────────────────────────────┘ │
│                                                                        │
│ Network ────────────────────────────────────────────── 4 skills        │
│ ┌───────────────┐  ┌───────────────┐  ┌───────────────┐                │
│ │ 🌐            │  │ ⚡            │  │ 🔗            │                │
│ │ Cloudflare    │  │ cping         │  │ FRP Server    │                │
│ │ Tunnel        │  │               │  │               │                │
│ │ Manage routes │  │ Quick ping    │  │ Run frps in   │                │
│ │ and DNS…      │  │ check…        │  │ background…   │                │
│ │ [Installed ✓] │  │ [Installed ✓] │  │ [Install]     │                │
│ └───────────────┘  └───────────────┘  └───────────────┘                │
│                                                                        │
│ AI & Agents ──────────────────────────────────────── 3 skills          │
│ ┌───────────────┐  ┌───────────────┐  ┌───────────────┐                │
│ │ ✉️             │  │ 📝            │  │ 🌐            │                │
│ │ Google        │  │ Agent Summary │  │ Agent Webpage │                │
│ │ Workspace     │  │               │  │               │                │
│ │ Gmail/Sheets/ │  │ Generate      │  │ Talk to live  │                │
│ │ Drive/Cal     │  │ summaries…    │  │ webpage…      │                │
│ │ [Re-auth ⚠]   │  │ [Installed ✓] │  │ [Installed ✓] │                │
│ └───────────────┘  └───────────────┘  └───────────────┘                │
│                                                                        │
│ (more categories below…)                                               │
└────────────────────────────────────────────────────────────────────────┘
```

---

## 3. Component tree

```
SkillsPage
├── SkillsHeader
│   ├── Title
│   ├── SyncAllButton    → POST /api/skills/sync-agent/<each>
│   └── InstallAllButton → POST /api/skills/install-all
├── SkillsSearch         (input that filters local state)
├── SkillsCategoryList
│   └── SkillsCategorySection (one per category)
│       ├── SectionHeader (label, count)
│       └── SkillsGrid
│           └── SkillCard (one per skill)
│               ├── Icon
│               ├── Title
│               ├── Description (clamped to 2 lines)
│               ├── StatusBadge (Installed / Needs Attention / Available)
│               └── ActionButton (Install / Uninstall / Re-auth)
│
└── SkillDetailDrawer    (slide-in panel on card click)
    ├── DetailHeader (icon, title, version, description)
    ├── DetailStatus (config_present, requires_met grid)
    ├── DetailCommands (binary_aliases list, click-to-copy)
    ├── DetailMarkdown (SKILL.md / help.md preview, react-markdown)
    └── DetailActions (Install / Uninstall / Custom UIActions)
```

---

## 4. Data flow

```
SkillsPage
  ↓ on mount + on visibility tab
  ↓
  fetch GET /api/skills           (full list, single call)
  ↓ stores in useState skills[]
  ↓
  render categories from skills[]
  ↓
  user clicks Install on a card
  ↓
  POST /api/skills/<name>/install
  ↓
  optimistically set card to "Installing…" spinner
  ↓
  on response, replace card status with returned status
  ↓
  toast: "Installed cf-tunnel ✓"
```

Polling: when a skill is in "Installing…" state from a previous fetch
(server crashed mid-install?), poll `GET /api/skills/<name>/status` every
2 seconds until status stabilizes.

---

## 5. Skill card

Width: 280px. Height: ~180px (auto for description).

```tsx
function SkillCard({ skill }: { skill: SkillView }) {
  return (
    <div className="skill-card" onClick={() => openDetail(skill.name)}>
      <div className="icon"><LucideIcon name={skill.icon} /></div>
      <div className="title">{skill.title}</div>
      <div className="description">{skill.description}</div>
      <div className="footer">
        <StatusBadge status={skill.status} />
        <ActionButton skill={skill} />
      </div>
    </div>
  );
}
```

### StatusBadge variants

| Variant | Conditions | Color | Label |
| --- | --- | --- | --- |
| Installed | `installed=true && config_present=true && last_error=""` | green | `Installed ✓` |
| Available | `installed=false` | grey | `Available` |
| Needs config | `installed=true && config_present=false` | yellow | `Needs config` |
| Requires unmet | `installed=true && any requires_met=false` | yellow | `Missing dep` |
| Error | `last_error != ""` | red | `Error` (with tooltip) |

### ActionButton

- **Available** → `[Install]` primary
- **Installed** → `[Uninstall]` ghost
- **Needs attention** → `[Fix]` warning (re-runs install)

Clicking opens a confirm dialog for uninstall (with "Also delete config"
checkbox).

---

## 6. Detail drawer

Opens from the right side, 480px wide, slide-in. Built with
existing Drawer component if available.

```
┌───────────────────────────────────────────────────────┐
│ ← Back                                            ✕   │
│                                                       │
│ 🌐 Cloudflare Tunnel                       v1.0.0    │
│                                                       │
│ Manage Cloudflare Tunnel routes and DNS records      │
│ on this host.                                         │
│                                                       │
│ Status                                                │
│ ──────                                                │
│ Installed       ✓                                     │
│ Config          ~/cicy-ai/db/skills/cf.yaml     ✓ exists    │
│ State dir       ~/.local/state/cicy-skills/cf-tunnel  │
│ cloudflared     ✓ /usr/local/bin/cloudflared          │
│                                                       │
│ Commands                                              │
│ ────────                                              │
│ cf-tunnel              [📋]                          │
│ cf-tunnel-py           [📋]                          │
│ cf-tunnel.py           [📋]                          │
│                                                       │
│ SKILL.md                                              │
│ ────────                                              │
│ [↓ rendered markdown of skill_body field]            │
│                                                       │
│ Help                                                  │
│ ────                                                  │
│ [↓ rendered markdown of help_body field]             │
│                                                       │
│ ─────────────────────────────────────                │
│ [Reinstall]    [Uninstall]    [Re-authorize ⚠]       │
└───────────────────────────────────────────────────────┘
```

The bottom action row also surfaces `UIAction` items from the manifest as
extra buttons (e.g. "Re-authorize" for the google skill).

---

## 7. Empty/loading/error states

### Loading
- Skeleton cards (grey blocks) while `fetch /api/skills` is in flight.

### Empty (no skills installed yet)
- Single message: "No skills installed. [Install all] to get started."
- Empty state's [Install all] button triggers `POST /api/skills/install-all`.

### API error
- Centered banner: "Couldn't load skills: <error>" with [Retry] button.

### Per-card install error
- Card shows red border + StatusBadge "Error".
- Tooltip on hover: full error message from `status.last_error`.
- Clicking [Fix] retries the install and shows last log lines in a toast.

---

## 8. Real-time updates

When the user installs a skill in another tab or via the CLI, this page
should reflect the change. Options:

- **Polling** (simple): refetch `/api/skills` every 30s while page is
  focused. Reasonable for a low-frequency page.
- **WebSocket / SSE** (later): server pushes status change events.

Start with polling. Upgrade if it becomes a UX problem.

---

## 9. Filters and search

Search input at top: filters across `name`, `title`, `description`, `tags`
locally (no API call per keystroke).

Optional category filter chips above the grid:
`[All]  [Network 4]  [AI 3]  [Infra 3]  [Dev 2]  [Comms 1]  [Ops 2]`

Clicking a chip narrows to that category. URL updates to
`/skills?category=network`.

---

## 10. Routes

```
/skills                      → list
/skills?category=ai          → filtered
/skills?q=tunnel             → searched
/skills/cf-tunnel            → drawer open for cf-tunnel
/skills/cf-tunnel/help       → drawer open, scrolled to Help section
```

The drawer opening is a URL change so users can deep-link to a specific
skill.

---

## 11. API client

`app/src/services/api.ts` already has the `ApiService` class. Add:

```ts
class ApiService {
  // ... existing methods ...

  listSkills(query?: { category?: string; installed?: boolean; q?: string }) {
    return this.get<SkillListResponse>('/api/skills', { params: query });
  }
  getSkill(name: string) {
    return this.get<SkillDetailResponse>(`/api/skills/${name}`);
  }
  getSkillStatus(name: string) {
    return this.get<InstallStatus>(`/api/skills/${name}/status`);
  }
  installSkill(name: string, opts?: { force?: boolean }) {
    return this.post<InstallResponse>(`/api/skills/${name}/install`, opts ?? {});
  }
  uninstallSkill(name: string, opts?: { purge_config?: boolean }) {
    return this.post<{ ok: boolean; log: string[] }>(`/api/skills/${name}/uninstall`, opts ?? {});
  }
  runSkillAction(name: string, actionId: string, body: any) {
    return this.post(`/api/skills/${name}/actions/${actionId}`, body);
  }
  installAllSkills(opts?: { force?: boolean }) {
    return this.post<InstallAllResponse>('/api/skills/install-all', opts ?? {});
  }
  syncAgentSkills(profile: 'codex' | 'claude' | 'opencode') {
    return this.post(`/api/skills/sync-agent/${profile}`, {});
  }
  getSkillsHealth() {
    return this.get<SkillsHealth>('/api/skills/health');
  }
  getSkillCategories() {
    return this.get<{ categories: Category[] }>('/api/skills/categories');
  }
}
```

Types live in `app/src/types/skills.ts`.

---

## 12. Visual style

- Card background: `bg-card` (matches existing components)
- Card hover: subtle scale + shadow
- Icons: lucide-react `<Globe />`, `<Activity />`, etc. by name from the
  `icon` field — fallback to `<Package />` if unknown
- Status badge: small rounded pill, color from `--success`, `--warning`,
  `--danger` CSS vars
- Section dividers: thin horizontal line with category label

Match the existing app's design system (already established in
`app/src/components/` — use shared Button, Dialog, Tooltip, etc.)

---

## 13. Accessibility

- All cards keyboard-navigable (Tab + Enter to open detail)
- StatusBadge has `aria-label="Installed"` etc.
- Detail drawer trap focus and ESC to close
- Action buttons have `aria-busy` during install

---

## 14. Telemetry (optional)

Each install / uninstall click emits a UI event:
- `skill_install_clicked` with `{ name, version }`
- `skill_install_succeeded` with `{ name, duration_ms }`
- `skill_install_failed` with `{ name, error_code }`

So we can see which skills are popular and which fail in the wild.

---

## 15. Acceptance checklist (frontend devs)

When a frontend dev wires this up:

- [ ] `/skills` route renders without error on a fresh container
- [ ] All categories render with correct counts
- [ ] Cards reflect real install status
- [ ] [Install] button triggers POST and updates card on response
- [ ] [Uninstall] button shows confirm dialog with "purge config" checkbox
- [ ] Detail drawer opens on card click and shows full SKILL.md
- [ ] Search filters cards in real time
- [ ] Empty state shown when no skills installed
- [ ] Error state shown when API unreachable
- [ ] Page works for a skill author's freshly registered new skill without
      any frontend changes (the registry-driven backend is the source)
