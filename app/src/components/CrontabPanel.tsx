import { useCallback, useEffect, useMemo, useState } from 'react'
import { Check, Clock3, Loader2, Plus, RefreshCw, Save, Trash2 } from 'lucide-react'
import apiService from '../services/api'
import { useDialogs } from './ui/Modal'

type Job = { id: string; schedule: string; command: string; enabled: boolean }
const DISABLED = '# cicy-disabled '
const makeId = () => `${Date.now()}-${Math.random().toString(36).slice(2)}`

function parse(content: string): { jobs: Job[]; preserved: string[] } {
  const jobs: Job[] = []
  const preserved: string[] = []
  for (const original of content.split(/\r?\n/)) {
    if (!original.trim()) continue
    const enabled = !original.startsWith(DISABLED)
    const line = enabled ? original : original.slice(DISABLED.length)
    const match = line.match(/^(@\S+|(?:\S+\s+){4}\S+)\s+(.+)$/)
    if (match && !line.trimStart().startsWith('#') && !/^\w+=/.test(line)) {
      jobs.push({ id: makeId(), schedule: match[1], command: match[2], enabled })
    } else preserved.push(original)
  }
  return { jobs, preserved }
}

const presets = [
  ['每分钟', '* * * * *'], ['每 5 分钟', '*/5 * * * *'], ['每小时', '0 * * * *'],
  ['每天 00:00', '0 0 * * *'], ['每周一', '0 0 * * 1'], ['启动时', '@reboot'],
]

export default function CrontabPanel({ active }: { active: boolean }) {
  const { confirm, node: dialogsNode } = useDialogs()
  const [jobs, setJobs] = useState<Job[]>([])
  const [preserved, setPreserved] = useState<string[]>([])
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [dirty, setDirty] = useState(false)
  const [message, setMessage] = useState('')

  const load = useCallback(async () => {
    setLoading(true); setMessage('')
    try {
      const resp: any = await apiService.getCrontab()
      const result = parse(String(resp?.data?.content || ''))
      setJobs(result.jobs); setPreserved(result.preserved); setDirty(false)
    } catch (error: any) {
      setMessage(error?.response?.data?.error || error?.message || '读取失败')
    } finally { setLoading(false) }
  }, [])

  useEffect(() => { if (active) void load() }, [active, load])
  const enabledCount = useMemo(() => jobs.filter((job) => job.enabled).length, [jobs])
  const update = (id: string, patch: Partial<Job>) => {
    setJobs((all) => all.map((job) => job.id === id ? { ...job, ...patch } : job)); setDirty(true)
  }
  const remove = async (job: Job, index: number) => {
    const ok = await confirm({
      title: '删除定时任务？',
      body: <div className="space-y-2"><div>确定删除任务 {index + 1}？此操作将在保存后应用。</div><div className="rounded-md bg-black/30 px-2.5 py-2 font-mono text-xs text-zinc-400">{job.schedule} {job.command}</div></div>,
      confirmLabel: '删除',
      cancelLabel: '取消',
      danger: true,
    })
    if (!ok) return
    setJobs((all) => all.filter((item) => item.id !== job.id)); setDirty(true)
  }
  const save = async () => {
    setSaving(true); setMessage('')
    try {
      const taskLines = jobs.filter((j) => j.schedule.trim() && j.command.trim()).map((j) =>
        `${j.enabled ? '' : DISABLED}${j.schedule.trim()} ${j.command.trim()}`)
      await apiService.saveCrontab([...preserved, ...taskLines].join('\n'))
      setDirty(false); setMessage('已保存并应用到系统 crontab')
    } catch (error: any) {
      setMessage(error?.response?.data?.error || error?.response?.data || error?.message || '保存失败')
    } finally { setSaving(false) }
  }

  return (
    <div data-id="crontab-panel" className="flex h-full flex-col bg-[#0b0b0d] text-zinc-200">
      <div className="flex shrink-0 items-center justify-between border-b border-white/[0.07] px-4 py-3">
        <div><div className="flex items-center gap-2 text-sm font-semibold"><Clock3 className="h-4 w-4 text-indigo-400" />定时任务</div>
          <div className="mt-1 text-[11px] text-zinc-500">{jobs.length} 个任务，{enabledCount} 个已启用</div></div>
        <div className="flex gap-2">
          <button onClick={() => void load()} disabled={loading} className="rounded-md p-2 text-zinc-400 hover:bg-white/[0.06] hover:text-white"><RefreshCw className={`h-4 w-4 ${loading ? 'animate-spin' : ''}`} /></button>
          <button onClick={() => void save()} disabled={saving || !dirty} className="inline-flex items-center gap-1.5 rounded-md bg-indigo-500 px-3 py-2 text-xs font-medium text-white disabled:opacity-40">{saving ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Save className="h-3.5 w-3.5" />}保存并应用</button>
        </div>
      </div>
      <div className="min-h-0 flex-1 overflow-auto p-4">
        <div className="mb-3 flex flex-wrap gap-1.5">{presets.map(([label, schedule]) => <button key={schedule} onClick={() => { setJobs((v) => [...v, { id: makeId(), schedule, command: '', enabled: true }]); setDirty(true) }} className="rounded-md border border-white/[0.08] bg-white/[0.03] px-2.5 py-1.5 text-[11px] text-zinc-400 hover:bg-white/[0.07] hover:text-zinc-100"><Plus className="mr-1 inline h-3 w-3" />{label}</button>)}</div>
        {jobs.length === 0 && !loading ? <button onClick={() => { setJobs([{ id: makeId(), schedule: '* * * * *', command: '', enabled: true }]); setDirty(true) }} className="flex h-36 w-full flex-col items-center justify-center rounded-xl border border-dashed border-white/[0.1] text-sm text-zinc-500 hover:border-indigo-500/40 hover:text-zinc-300"><Plus className="mb-2 h-5 w-5" />新增第一个定时任务</button> : null}
        <div className="space-y-2">{jobs.map((job, index) => <div key={job.id} data-id={`crontab-job-${index}`} className={`rounded-xl border p-3 ${job.enabled ? 'border-white/[0.08] bg-white/[0.025]' : 'border-white/[0.05] bg-black/20 opacity-60'}`}>
          <div className="mb-2 flex items-center justify-between"><span className="text-xs font-medium text-zinc-400">任务 {index + 1}</span><div className="flex items-center gap-2"><button onClick={() => update(job.id, { enabled: !job.enabled })} className={`inline-flex items-center gap-1 rounded-full px-2 py-1 text-[10px] ${job.enabled ? 'bg-emerald-500/15 text-emerald-300' : 'bg-white/[0.05] text-zinc-500'}`}>{job.enabled ? <Check className="h-3 w-3" /> : null}{job.enabled ? '已启用' : '已停用'}</button><button onClick={() => void remove(job, index)} className="rounded p-1.5 text-zinc-600 hover:bg-red-500/10 hover:text-red-400"><Trash2 className="h-3.5 w-3.5" /></button></div></div>
          <label className="mb-2 block text-[10px] text-zinc-500">执行周期<input value={job.schedule} onChange={(e) => update(job.id, { schedule: e.target.value })} placeholder="* * * * *" className="mt-1 block w-full rounded-md border border-white/[0.08] bg-black/30 px-2.5 py-2 font-mono text-xs text-zinc-200 outline-none focus:border-indigo-500/50" /></label>
          <label className="block text-[10px] text-zinc-500">执行命令<textarea value={job.command} onChange={(e) => update(job.id, { command: e.target.value })} rows={2} placeholder="/path/to/script.sh >> /path/to/log 2>&1" className="mt-1 block w-full resize-y rounded-md border border-white/[0.08] bg-black/30 px-2.5 py-2 font-mono text-xs leading-5 text-zinc-200 outline-none focus:border-indigo-500/50" /></label>
        </div>)}</div>
        {preserved.length > 0 ? <div className="mt-4 rounded-lg border border-white/[0.06] bg-black/20 p-3"><div className="mb-2 text-[10px] uppercase tracking-wider text-zinc-600">保留配置</div><pre className="whitespace-pre-wrap break-all text-[11px] text-zinc-500">{preserved.join('\n')}</pre></div> : null}
        {message ? <div className={`mt-3 rounded-md px-3 py-2 text-xs ${message.startsWith('已保存') ? 'bg-emerald-500/10 text-emerald-300' : 'bg-red-500/10 text-red-300'}`}>{message}</div> : null}
      </div>
      {dialogsNode}
    </div>
  )
}
