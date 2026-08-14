// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { AlertTriangle, Check, Loader2, RotateCcw, ShieldAlert } from 'lucide-react'
import { useCallback, useEffect, useState } from 'react'
import { getApiBase } from '../../config'

// The factory board, in the one place you are already looking.
//
// A line is (mechanically) a chain of agent turns. What makes it a LINE and not
// just a chain is what this strip shows: the declared stations, the gate the
// engine will stop at, and the cost each station actually spent. Hide those and
// there is nothing here you couldn't do by hand — so they are the whole display.
//
// The approve button is the point. The engine parks the run; nothing downstream
// (i.e. nothing that touches the outside world) runs until someone named clicks
// it. Putting that button anywhere but in front of the operator would be theatre.

interface PlanStation { id: string; human: boolean }
interface StationRun {
  id: string
  status: string        // done | rework | failed | awaiting_approval | approved
  attempt: number
  cost_credit: number
  error?: string
}
interface LineMetrics {
  unit_cost_usd: number
  cycle_time_s: number
  yield: number
  attempts: number
  reworks: number
  bottleneck?: string
}
export interface LineRun {
  id: string
  line_id: string
  line_version: string
  agent_id: string
  status: string        // running | awaiting_approval | done | failed
  awaiting_station?: string
  error?: string
  plan?: PlanStation[]
  stations?: StationRun[]
  metrics?: LineMetrics
}

let runsCache: LineRun[] = []
let runsFetchedAt = 0
let runsRequest: Promise<LineRun[]> | null = null

async function fetchLineRuns(force = false): Promise<LineRun[]> {
  if (!force && runsFetchedAt > 0 && Date.now() - runsFetchedAt < 1000) return runsCache
  if (runsRequest) return runsRequest
  runsRequest = (async () => {
    const resp = await fetch(`${getApiBase()}/api/line/runs`)
    if (!resp.ok) throw new Error(`line runs: ${resp.status}`)
    const data = await resp.json()
    runsCache = Array.isArray(data?.runs) ? data.runs : []
    runsFetchedAt = Date.now()
    return runsCache
  })().finally(() => { runsRequest = null })
  return runsRequest
}

// stationState folds the attempt list down to one state per declared station.
// A station can appear several times (each rework is an attempt); the LAST one
// is its current state, and the count is what makes a rework loop visible.
function stationState(run: LineRun, id: string) {
  const attempts = (run.stations || []).filter((s) => s.id === id)
  const last = attempts[attempts.length - 1]
  const reworks = attempts.filter((s) => s.status === 'rework').length
  const cost = attempts.reduce((sum, s) => sum + (s.cost_credit || 0), 0)
  return { last, reworks, cost, ran: attempts.length > 0 }
}

export default function LineStrip({ paneId, active }: { paneId: string; active: boolean }) {
  const [run, setRun] = useState<LineRun | null>(null)
  const [approving, setApproving] = useState(false)

  const load = useCallback(async (force = false) => {
    if (!active) return
    try {
      const runs = await fetchLineRuns(force)
      // The newest run on THIS agent. /api/line/runs is already newest-first.
      setRun(runs.find((r) => r.agent_id === paneId) || null)
    } catch {
      // The engine is loopback-only and may simply not be there. A board that
      // throws when there is nothing to show is worse than one that shows nothing.
    }
  }, [active, paneId])

  useEffect(() => {
    if (!active) return
    void load()
    // Poll while a run is live; back off to a slow tick once it settles, so a
    // finished run still updates if someone approves it from the CLI.
    const live = run?.status === 'running'
    const id = window.setInterval(() => { void load() }, live ? 1500 : 8000)
    return () => window.clearInterval(id)
  }, [active, load, run?.status])

  const approve = useCallback(async () => {
    if (!run) return
    setApproving(true)
    try {
      await fetch(`${getApiBase()}/api/line/approve`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        // The record has to name a person. "operator" is who is sitting here.
        body: JSON.stringify({ run: run.id, by: 'operator', note: 'approved from the line strip' }),
      })
    } finally {
      setApproving(false)
      void load(true)
    }
  }, [run, load])

  if (!run) return null

  const m = run.metrics
  const parked = run.status === 'awaiting_approval'
  const plan = run.plan || []

  return (
    <div
      data-id={`line-strip-${paneId}`}
      className="shrink-0 border-b border-white/[0.06] bg-black/[0.35] px-4 py-2.5"
    >
      {/* header: which line, and what it has cost so far */}
      <div data-id={`line-strip-header-${paneId}`} className="flex items-center gap-2 text-[11px]">
        <span data-id={`line-strip-name-${paneId}`} className="font-semibold text-zinc-300">
          🏭 {run.line_id}
          <span className="ml-1 font-normal text-zinc-600">v{run.line_version}</span>
        </span>
        <span className="flex-1" />
        {m && (
          <span data-id={`line-strip-metrics-${paneId}`} className="flex items-center gap-2.5 tabular-nums text-zinc-500">
            {/* Measured, never estimated — the only number worth showing. */}
            <span data-id={`line-strip-cost-${paneId}`} className="font-semibold text-zinc-300">
              ${m.unit_cost_usd.toFixed(4)}
            </span>
            <span>良率 {Math.round((m.yield || 0) * 100)}%</span>
            <span>{m.cycle_time_s.toFixed(0)}s</span>
            {m.bottleneck && <span>瓶颈 {m.bottleneck}</span>}
          </span>
        )}
      </div>

      {/* the line itself */}
      <div data-id={`line-strip-stations-${paneId}`} className="mt-2 flex flex-wrap items-center gap-1">
        {plan.map((st, i) => {
          const { last, reworks, cost, ran } = stationState(run, st.id)
          const isCurrent = run.status === 'running' && !ran &&
            plan.slice(0, i).every((p) => stationState(run, p.id).ran)
          const failed = last?.status === 'failed'
          const done = last?.status === 'done' || last?.status === 'approved'
          const waiting = last?.status === 'awaiting_approval'

          let tone = 'border-white/[0.06] bg-white/[0.02] text-zinc-600'   // not yet run
          if (done) tone = 'border-emerald-500/25 bg-emerald-500/[0.08] text-emerald-300'
          if (isCurrent) tone = 'border-blue-500/30 bg-blue-500/[0.10] text-blue-200'
          if (waiting) tone = 'border-amber-500/40 bg-amber-500/[0.12] text-amber-200'
          if (failed) tone = 'border-rose-500/40 bg-rose-500/[0.12] text-rose-200'

          return (
            <div key={st.id} className="flex items-center gap-1">
              {i > 0 && <span className="text-zinc-700">→</span>}
              <span
                data-id={`line-strip-station-${st.id}-${paneId}`}
                title={failed ? last?.error : undefined}
                className={`inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-[11px] leading-none ${tone}`}
              >
                {/* A human gate LOOKS different from a working station — it is a
                    different kind of thing, and the operator must never confuse
                    "this is running" with "this is waiting for me". */}
                {st.human && <ShieldAlert className="h-3 w-3" />}
                {isCurrent && <Loader2 className="h-3 w-3 animate-spin" />}
                {done && !st.human && <Check className="h-3 w-3" />}
                {failed && <AlertTriangle className="h-3 w-3" />}
                <span>{st.id}</span>
                {reworks > 0 && (
                  <span
                    data-id={`line-strip-rework-${st.id}-${paneId}`}
                    className="inline-flex items-center gap-0.5 text-amber-300"
                    title={`${reworks} 次返工`}
                  >
                    <RotateCcw className="h-3 w-3" />
                    {reworks}
                  </span>
                )}
                {cost > 0 && (
                  <span className="tabular-nums text-zinc-500">${cost.toFixed(3)}</span>
                )}
              </span>
            </div>
          )
        })}
      </div>

      {/* the gate */}
      {parked && (
        <div
          data-id={`line-strip-gate-${paneId}`}
          className="mt-2 flex items-center gap-3 rounded-md border border-amber-500/25 bg-amber-500/[0.06] px-3 py-2"
        >
          <ShieldAlert className="h-4 w-4 shrink-0 text-amber-300" />
          <span className="flex-1 text-[12px] leading-snug text-amber-100/90">
            停在人工门 <b className="font-semibold">{run.awaiting_station}</b> —— 它下游的工位不会跑,
            <b className="font-semibold">没有任何对外动作发生</b>。
          </span>
          <button
            data-id={`line-strip-approve-${paneId}`}
            type="button"
            disabled={approving}
            onClick={() => void approve()}
            className="inline-flex shrink-0 items-center gap-1.5 rounded-md bg-amber-500/90 px-3 py-1.5 text-[12px] font-semibold text-black transition-colors hover:bg-amber-400 disabled:opacity-50"
          >
            {approving ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Check className="h-3.5 w-3.5" />}
            批准
          </button>
        </div>
      )}

      {run.status === 'failed' && run.error && (
        <div
          data-id={`line-strip-error-${paneId}`}
          className="mt-2 rounded-md border border-rose-500/25 bg-rose-500/[0.06] px-3 py-2 text-[12px] leading-snug text-rose-200/90"
        >
          {run.error}
        </div>
      )}
    </div>
  )
}
