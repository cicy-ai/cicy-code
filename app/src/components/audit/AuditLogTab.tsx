// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Activity, ArrowDown, ArrowUp, RefreshCw, ShieldX, EyeOff, Bell, FileText, Check, Flag, CircleAlert, Terminal, ShieldCheck, Loader2, ChevronLeft, ChevronRight, ListChecks, Trash2, X } from 'lucide-react';
import type { TFunction } from 'i18next';
import apiService from '../../services/api';
import { loadHandled, serverAckedIds, resolveStatus, setHandled, type HandledMap, type ResolvedStatus } from './auditHandled';

// Local audit log — master/detail UI backed by GET /api/audit/events.
interface Span { start?: number; end?: number; preview?: string; path?: string; context?: string }
interface Finding { rule_id?: string; rule_version?: string; severity?: string; category?: string; match_count?: number; spans?: Span[] }
interface AuditEvent {
  id: string;
  ts: string;
  identity?: { machine_id?: string; agent_id?: string; agent_type?: string; user_id?: string; session_id?: string; source_channel?: string };
  subject?: { turn_id?: string; conversation_id?: string; provider?: string; model?: string; direction?: string; payload_size?: number; payload_ref?: string; payload_sha256?: string };
  findings?: Finding[];
  decision?: { action?: string; applied?: boolean; evaluated_inline?: boolean; evaluated_async?: boolean; fail_mode?: string };
  meta?: { allowlisted_by?: string; allowlist_match?: string; notify_suppressed_by?: string; pre_redact_ref?: string; snapshot_ref?: string; pipeline_error?: string; policy_hash?: string; scanner_duration_ms?: number; category?: string; ack_event_id?: string };
}

const POLL_MS = 8000;
const FETCH_LIMIT = 80;

// 整体暗灰,只有 critical 显红;其余严重度只用中性灰(语义靠文字,不靠颜色刺激)。
const severityColor: Record<string, string> = {
  critical: 'bg-red-500/15 text-red-400 border-red-500/30',
  high: 'bg-zinc-500/10 text-zinc-300 border-zinc-500/25',
  medium: 'bg-zinc-500/10 text-zinc-400 border-zinc-500/20',
  low: 'bg-zinc-500/10 text-zinc-500 border-zinc-500/20',
};
const sevAccent: Record<string, string> = {
  critical: 'bg-red-500', high: 'bg-zinc-600', medium: 'bg-zinc-600', low: 'bg-zinc-700',
};

function ActionChip({ action, applied }: { action?: string; applied?: boolean }) {
  const { t } = useTranslation('audit');
  if (!action || action === 'none') return null;
  const chip = 'bg-zinc-500/10 text-zinc-400 border-zinc-500/20';
  const map: Record<string, { cls: string; icon: any; label: string }> = {
    block: { cls: chip, icon: ShieldX, label: t('guardBlocked') },
    redact: { cls: chip, icon: EyeOff, label: t('guardRedacted') },
    notify: { cls: chip, icon: Bell, label: t('logActionNotify', 'Notify') },
    log: { cls: chip, icon: FileText, label: t('logActionLog', 'Log') },
  };
  const m = map[action] || map.log;
  const Icon = m.icon;
  return (
    <span className={`inline-flex items-center gap-1 rounded border px-1.5 py-0.5 text-[10px] font-medium ${m.cls} ${applied ? '' : 'opacity-50'}`}>
      <Icon size={10} />{m.label}
    </span>
  );
}

function StatusBadge({ status }: { status: ResolvedStatus }) {
  const { t } = useTranslation('audit');
  if (status === 'done') {
    return <span className="inline-flex items-center gap-1 rounded border border-zinc-500/25 bg-zinc-500/10 px-1.5 py-0.5 text-[10px] font-medium text-zinc-500"><Check size={10} />{t('statusDone', '已处理')}</span>;
  }
  if (status === 'false_positive') {
    return <span className="inline-flex items-center gap-1 rounded border border-zinc-500/25 bg-zinc-500/10 px-1.5 py-0.5 text-[10px] font-medium text-zinc-500"><Flag size={10} />{t('statusFalsePositive', '误报')}</span>;
  }
  return <span className="inline-flex items-center gap-1 rounded border border-zinc-500/30 bg-zinc-500/10 px-1.5 py-0.5 text-[10px] font-medium text-zinc-300"><CircleAlert size={10} />{t('statusOpen', '未处理')}</span>;
}

const fmtTime = (ts: string) => {
  const d = new Date(ts);
  if (isNaN(d.getTime())) return '—';
  return d.toLocaleTimeString(undefined, { hour12: false, hour: '2-digit', minute: '2-digit', second: '2-digit' });
};

function sevLabel(t: TFunction, s?: string) { return t(`sev_${s}` as any, { defaultValue: s || '' }) as string; }
function kindLabel(t: TFunction, cat?: string) {
  if (cat === 'secret') return t('kindSecret', '密钥/凭证');
  if (cat === 'pii') return t('kindPii', '个人信息');
  return t('kindGeneric', '敏感内容');
}
function actionPhrase(t: TFunction, action?: string) {
  switch (action) {
    case 'block': return t('actBlock', '已拦截并阻止发送');
    case 'redact': return t('actRedact', '已自动脱敏后放行');
    case 'notify': return t('actNotify', '已告警(未拦截)');
    default: return t('actLog', '已记录');
  }
}
function suppressReason(t: TFunction, r?: string) {
  switch (r) {
    case 'cooldown': return t('supCooldown', '冷却期');
    case 'rate_limit': return t('supRate', '限频');
    case 'suspended': return t('supSuspended', '已暂停');
    case 'below_min_severity': return t('supBelow', '低于告警阈值');
    default: return r || '';
  }
}
function sourceLabel(t: TFunction, src?: string) {
  if (src === 'mitm') return t('srcMitm', '流量代理(MITM 抓包)');
  if (src === 'audit_system') return t('srcSystem', '审计系统');
  return src || '';
}
function ruleFriendly(t: TFunction, id?: string, cat?: string) {
  const s = id || '';
  if (s.includes('high_entropy')) return t('ruleHighEntropy', '疑似密钥/令牌(高随机度串)');
  if (s.includes('jwt')) return t('ruleJwt', 'JWT 令牌');
  if (s.includes('bearer')) return t('ruleBearer', 'Bearer 令牌');
  if (s.includes('api_key') || s.includes('apikey') || s.includes('cicy_token')) return t('ruleApiKey', 'API 密钥');
  if (s.includes('id_card')) return t('ruleIdCard', '身份证号');
  if (s.includes('bank_card')) return t('ruleBankCard', '银行卡号');
  if (s.includes('phone')) return t('rulePhone', '手机号');
  if (s.includes('email')) return t('ruleEmail', '邮箱地址');
  return kindLabel(t, cat);
}

function KV({ label, value, mono }: { label: string; value?: ReactNode; mono?: boolean }) {
  if (value === undefined || value === null || value === '') return null;
  return (
    <div className="flex gap-2">
      <span className="w-24 shrink-0 text-[10px] uppercase tracking-wide text-[var(--vsc-text-muted)]">{label}</span>
      <span className={`min-w-0 break-all text-[var(--vsc-text-secondary)] ${mono ? 'font-mono text-[11px]' : ''}`}>{value}</span>
    </div>
  );
}

function downloadText(text: string, name: string) {
  try {
    const blob = new Blob([text], { type: 'application/json' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url; a.download = name; a.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  } catch { /* ignore */ }
}

// ── Snapshot viewer — just show the archived snapshot JSON (full QA, real
// content). No bubble formatting; the JSON IS the forensic record.
function SnapshotView({ refStr, eventId }: { refStr: string; eventId: string }) {
  const { t } = useTranslation('audit');
  const [raw, setRaw] = useState<string>('');
  const [loading, setLoading] = useState(false);
  const [err, setErr] = useState('');
  const [open, setOpen] = useState(false);

  const load = async () => {
    setLoading(true); setErr('');
    try {
      const res: any = await apiService.getAuditSnapshot(refStr);
      const d = res?.data;
      setRaw(typeof d === 'string' ? d : JSON.stringify(d, null, 2));
      setOpen(true);
    } catch {
      setErr(t('snapFailed', '快照读取失败'));
    } finally { setLoading(false); }
  };

  if (!open) {
    return (
      <button
        data-id={`audit-log-snapshot-load-${eventId}`}
        onClick={load} disabled={loading}
        className="inline-flex items-center gap-1.5 rounded-md border border-[var(--vsc-border-subtle)] bg-[var(--vsc-bg-hover)] px-2.5 py-1.5 text-[11px] text-[var(--vsc-text-secondary)] transition-colors hover:text-white disabled:opacity-50"
      >
        {loading ? <Loader2 size={12} className="animate-spin" /> : <FileText size={12} />}
        {loading ? t('snapLoading', '加载中…') : t('snapView', '查看完整问答快照')}
      </button>
    );
  }
  if (err) return <div className="text-[11px] text-red-400">{err}</div>;

  return (
    <div data-id={`audit-log-snapshot-${eventId}`} className="space-y-1.5">
      <div className="flex items-center gap-3 text-[10px] text-[var(--vsc-text-muted)]">
        <span>{t('snapTitleFull', '完整问答快照(原文,供取证)')}</span>
        <button onClick={() => downloadText(raw, `${eventId}.json`)} className="hover:text-white">{t('snapDownload', '下载')}</button>
        <button onClick={() => setOpen(false)} className="hover:text-white">{t('snapClose', '收起')}</button>
      </div>
      <pre className="max-h-96 overflow-auto whitespace-pre-wrap break-all rounded bg-black/40 p-2 font-mono text-[10px] leading-relaxed text-[var(--vsc-text-secondary)]">{raw}</pre>
    </div>
  );
}

function DetailPanel({ e, status, onMark, onFalsePositive }: {
  e: AuditEvent;
  status: ResolvedStatus;
  onMark: (id: string, status: 'done' | 'false_positive') => void;
  onFalsePositive: (e: AuditEvent) => Promise<{ ok: boolean; msg: string }>;
}) {
  const { t } = useTranslation('audit');
  const short = (s?: string, n = 16) => (s && s.length > n ? `${s.slice(0, n)}…` : s || '');
  const [fpState, setFpState] = useState<'idle' | 'busy' | 'done' | 'err'>('idle');
  const [fpMsg, setFpMsg] = useState('');

  const handleFP = async () => {
    setFpState('busy'); setFpMsg('');
    const r = await onFalsePositive(e);
    setFpState(r.ok ? 'done' : 'err'); setFpMsg(r.msg);
  };

  const dir = e.subject?.direction;
  const hasSha = !!e.subject?.payload_sha256;

  return (
    <div data-id={`audit-log-detail-${e.id}`} className="flex h-full min-w-0 flex-col">
      {/* Detail header: verdict + status */}
      <div className="shrink-0 border-b border-[var(--vsc-border-subtle)] px-4 py-3">
        <div className="flex flex-wrap items-center gap-2">
          <span className="text-[13px] font-semibold text-[var(--vsc-text)]">{actionPhrase(t, e.decision?.action)}</span>
          <StatusBadge status={status} />
          <ActionChip action={e.decision?.action} applied={e.decision?.applied} />
          <span className="ml-auto text-[10px] text-[var(--vsc-text-muted)]">{fmtTime(e.ts)}</span>
        </div>
        <div className="mt-1 flex flex-wrap items-center gap-1.5">
          {(e.findings || []).map((f, i) => (
            <span key={i} className={`rounded border px-1.5 py-0.5 text-[10px] font-medium ${severityColor[f.severity || 'low'] || severityColor.low}`}>
              {ruleFriendly(t, f.rule_id, f.category)} · {sevLabel(t, f.severity)}
            </span>
          ))}
        </div>
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto px-4 py-3">
        {/* Action area — triage the alert */}
        {status === 'open' && (
          <div data-id={`audit-log-actions-${e.id}`} className="mb-3 flex flex-wrap items-center gap-2 rounded-md border border-[var(--vsc-border)] bg-[var(--vsc-bg-secondary)] px-3 py-2">
            <span className="text-[11px] text-[var(--vsc-text-secondary)]">{t('triagePrompt', '这条告警怎么处理?')}</span>
            <button
              data-id={`audit-log-mark-done-${e.id}`}
              onClick={() => onMark(e.id, 'done')}
              className="inline-flex items-center gap-1 rounded-md border border-zinc-500/30 bg-zinc-500/10 px-2.5 py-1 text-[11px] font-medium text-zinc-300 transition-colors hover:bg-zinc-500/20"
            >
              <Check size={12} />{t('markDone', '标记已处理')}
            </button>
            <button
              data-id={`audit-log-mark-fp-${e.id}`}
              onClick={handleFP}
              disabled={fpState === 'busy' || !hasSha}
              title={hasSha ? t('markFpAllowlistHint', '标为误报,并把这段内容加入白名单(以后相同内容不再告警)') : t('markFpNoSha', '该事件无内容哈希,无法加白名单')}
              className="inline-flex items-center gap-1 rounded-md border border-zinc-500/30 bg-zinc-500/10 px-2.5 py-1 text-[11px] font-medium text-zinc-300 transition-colors hover:bg-zinc-500/20 disabled:opacity-50"
            >
              {fpState === 'busy' ? <Loader2 size={12} className="animate-spin" /> : <ShieldCheck size={12} />}
              {t('markFpAllowlist', '误报(加白名单)')}
            </button>
            {fpMsg && <span className={`text-[10px] ${fpState === 'err' ? 'text-red-400' : 'text-[var(--vsc-text-secondary)]'}`}>{fpMsg}</span>}
          </div>
        )}

        {/* Conversation snapshot (full QA) */}
        {e.meta?.snapshot_ref && (
          <div className="mb-3">
            <SnapshotView refStr={e.meta.snapshot_ref} eventId={e.id} />
          </div>
        )}

        {/* Findings + matched snippets */}
        {(e.findings?.length || 0) > 0 && (
          <div className="mb-3">
            <div className="mb-1.5 text-[10px] font-semibold uppercase tracking-wide text-[var(--vsc-text-muted)]">{t('detailFindings', '命中规则')}</div>
            <div className="space-y-1.5">
              {e.findings!.map((f, i) => (
                <div key={i} className="rounded border border-[var(--vsc-border-subtle)] bg-[var(--vsc-bg-secondary)] px-2 py-1.5">
                  <div className="flex items-center gap-2">
                    <span className="text-[11px] font-medium text-[var(--vsc-text)]">{ruleFriendly(t, f.rule_id, f.category)}</span>
                    <span className={`rounded border px-1.5 py-0.5 text-[10px] ${severityColor[f.severity || 'low'] || severityColor.low}`}>{sevLabel(t, f.severity)}</span>
                    <span className="ml-auto text-[10px] text-[var(--vsc-text-muted)]">{t('detailMatchCount', '匹配 {{n}} 次', { n: f.match_count || 1 })}</span>
                  </div>
                  <div className="mt-0.5 font-mono text-[10px] text-[var(--vsc-text-muted)]">{f.rule_id}</div>
                  {(f.spans || []).filter((s) => s.preview).length > 0 && (
                    <div className="mt-1.5 space-y-1">
                      {(f.spans || []).filter((s) => s.preview).slice(0, 5).map((s, j) => (
                        <div key={j} className="rounded bg-black/40 px-2 py-1">
                          {s.path && <div className="font-mono text-[9px] text-[var(--vsc-text-muted)] break-all">{s.path}</div>}
                          {s.context
                            ? <div className="overflow-x-auto whitespace-nowrap font-mono text-[10px] text-[var(--vsc-text-secondary)]"><span className="text-zinc-500">…</span>{s.context}<span className="text-zinc-500">…</span></div>
                            : <pre className="max-h-40 overflow-auto whitespace-pre-wrap break-all font-mono text-[10px] text-[var(--vsc-text-secondary)]">{s.preview}</pre>}
                        </div>
                      ))}
                    </div>
                  )}
                </div>
              ))}
            </div>
          </div>
        )}

        {/* Meta flags */}
        {(e.meta?.allowlisted_by || e.meta?.notify_suppressed_by || e.meta?.pipeline_error) && (
          <div className="mb-3 flex flex-wrap gap-1.5">
            {e.meta?.allowlisted_by && <span className="rounded border border-zinc-500/20 bg-zinc-500/10 px-1.5 py-0.5 text-[10px] text-zinc-400">{t('logAllowlisted', '已放行')}: {e.meta.allowlisted_by}{e.meta.allowlist_match ? ` (${short(e.meta.allowlist_match)})` : ''}</span>}
            {e.meta?.notify_suppressed_by && <span className="rounded border border-zinc-500/20 bg-zinc-500/10 px-1.5 py-0.5 text-[10px] text-zinc-400">{t('detailNotifySuppressed', '告警抑制')}: {suppressReason(t, e.meta.notify_suppressed_by)}</span>}
            {e.meta?.pipeline_error && <span className="rounded border border-red-500/30 bg-red-500/10 px-1.5 py-0.5 text-[10px] text-red-400">{e.meta.pipeline_error}</span>}
          </div>
        )}

        {/* Context */}
        <div className="space-y-1">
          <KV label={t('detailEventId', '事件ID')} value={e.id} mono />
          <KV label={t('detailAgent', 'Agent')} value={[e.identity?.agent_id, e.identity?.agent_type].filter(Boolean).join(' · ')} mono />
          <KV label={t('detailProvider', '目标')} value={[e.subject?.provider, e.subject?.model].filter(Boolean).join(' · ')} mono />
          <KV label={t('detailDirection', '方向')} value={dir === 'inbound' ? t('dirInbound', '接收(模型返回)') : dir === 'outbound' ? t('dirOutbound', '外发(发给模型)') : dir === 'behavior' ? t('dirBehavior', '行为(工具调用)') : dir} />
          <KV label={t('detailPayload', '负载')} value={typeof e.subject?.payload_size === 'number' ? `${(e.subject.payload_size / 1024).toFixed(1)} KB` : undefined} />
          <KV label={t('detailDecision', '决策')} value={[actionPhrase(t, e.decision?.action), e.decision?.evaluated_inline ? t('decInline', '实时') : t('decAsync', '旁路'), e.decision?.fail_mode === 'open' ? t('failOpen', '失败放行') : e.decision?.fail_mode === 'closed' ? t('failClosed', '失败阻断') : ''].filter(Boolean).join(' · ')} />
          <KV label={t('detailScanner', '扫描耗时')} value={typeof e.meta?.scanner_duration_ms === 'number' ? `${e.meta.scanner_duration_ms} ms` : undefined} />
          <KV label={t('detailSource', '来源')} value={sourceLabel(t, e.identity?.source_channel)} />
        </div>
      </div>
    </div>
  );
}

// 白名单管理面板 — 列出 allow_list 三类条目,逐条可移除。
// content_hashes 是「误报(加白名单)」写入的;paths/agents 多为手工/自治配置。
type AllowlistData = { content_hashes: string[]; paths: string[]; agents: string[] };
type AllowCat = 'content_hash' | 'path' | 'agent';

function AllowlistPanel({ onClose }: { onClose: () => void }) {
  const { t } = useTranslation('audit');
  const [data, setData] = useState<AllowlistData | null>(null);
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);

  const refresh = async () => {
    setLoading(true); setErr(null);
    try {
      const res: any = await apiService.getAllowlist();
      const d = res?.data || {};
      setData({
        content_hashes: Array.isArray(d.content_hashes) ? d.content_hashes : [],
        paths: Array.isArray(d.paths) ? d.paths : [],
        agents: Array.isArray(d.agents) ? d.agents : [],
      });
    } catch {
      setErr(t('allowlistLoadErr', '白名单加载失败'));
    } finally { setLoading(false); }
  };
  useEffect(() => { void refresh(); /* eslint-disable-next-line react-hooks/exhaustive-deps */ }, []);

  const remove = async (category: AllowCat, value: string) => {
    setBusy(value);
    try {
      await apiService.removeAllowlist(category, value);
      await refresh();
    } catch {
      setErr(t('allowlistRemoveErr', '移除失败,请重试'));
    } finally { setBusy(null); }
  };

  const total = data ? data.content_hashes.length + data.paths.length + data.agents.length : 0;
  const sections: { key: AllowCat; label: string; hint: string; items: string[] }[] = data ? [
    { key: 'content_hash', label: t('allowlistContentHashes', '内容哈希'), hint: t('allowlistContentHashesHint', '由「误报(加白名单)」写入,相同内容不再告警'), items: data.content_hashes },
    { key: 'path', label: t('allowlistPaths', '路径'), hint: t('allowlistPathsHint', '匹配 payload 路径的放行'), items: data.paths },
    { key: 'agent', label: t('allowlistAgents', 'Agent'), hint: t('allowlistAgentsHint', '该 agent 的命中整体放行'), items: data.agents },
  ] : [];

  return (
    <div data-id="audit-log-allowlist-panel" className="absolute inset-0 z-20 flex flex-col bg-[var(--vsc-bg)]">
      <div data-id="audit-log-allowlist-header" className="flex shrink-0 items-center gap-2 border-b border-[var(--vsc-border-subtle)] px-3 py-2">
        <ListChecks size={14} className="text-[var(--vsc-text-secondary)]" />
        <span className="text-xs font-medium text-[var(--vsc-text-primary)]">{t('allowlistTitle', '白名单')}</span>
        <span className="text-[11px] text-[var(--vsc-text-muted)]">{t('allowlistTotal', '{{n}} 条', { n: total })}</span>
        <div className="flex-1" />
        <button data-id="audit-log-allowlist-refresh" onClick={refresh} className="rounded-md p-1.5 text-[var(--vsc-text-secondary)] transition-colors hover:bg-[var(--vsc-bg-hover)] hover:text-white" title={t('logRefresh', 'Refresh')}>
          <RefreshCw size={13} />
        </button>
        <button data-id="audit-log-allowlist-close" onClick={onClose} className="rounded-md p-1.5 text-[var(--vsc-text-secondary)] transition-colors hover:bg-[var(--vsc-bg-hover)] hover:text-white" title={t('close', '关闭')}>
          <X size={14} />
        </button>
      </div>
      <div data-id="audit-log-allowlist-body" className="min-h-0 flex-1 overflow-y-auto px-3 py-3">
        {err && <div data-id="audit-log-allowlist-error" className="mb-2 rounded border border-red-500/30 bg-red-500/10 px-2 py-1 text-[11px] text-red-400">{err}</div>}
        {loading && !data ? (
          <div className="flex items-center gap-2 text-[11px] text-[var(--vsc-text-muted)]"><Loader2 size={12} className="animate-spin" />{t('loading', '加载中…')}</div>
        ) : total === 0 ? (
          <div data-id="audit-log-allowlist-empty" className="py-8 text-center text-[11px] text-[var(--vsc-text-muted)]">{t('allowlistEmpty', '白名单为空。在告警详情里点「误报(加白名单)」可加入内容。')}</div>
        ) : (
          <div className="flex flex-col gap-4">
            {sections.map((s) => (
              <div key={s.key} data-id={`audit-log-allowlist-section-${s.key}`}>
                <div className="mb-1 flex items-baseline gap-2">
                  <span className="text-[11px] font-medium text-[var(--vsc-text-secondary)]">{s.label}</span>
                  <span className="text-[10px] text-[var(--vsc-text-muted)]">{s.items.length}</span>
                  <span className="text-[10px] text-[var(--vsc-text-muted)]">· {s.hint}</span>
                </div>
                {s.items.length === 0 ? (
                  <div className="rounded border border-dashed border-[var(--vsc-border-subtle)] px-2 py-1.5 text-[10px] text-[var(--vsc-text-muted)]">{t('allowlistSectionEmpty', '(空)')}</div>
                ) : (
                  <div className="flex flex-col gap-1">
                    {s.items.map((v) => (
                      <div key={v} data-id="audit-log-allowlist-item" className="group flex items-center gap-2 rounded border border-[var(--vsc-border-subtle)] bg-[var(--vsc-bg-elevated)] px-2 py-1.5">
                        <code className="min-w-0 flex-1 truncate font-mono text-[11px] text-[var(--vsc-text-secondary)]" title={v}>{v}</code>
                        <button
                          data-id="audit-log-allowlist-remove"
                          onClick={() => remove(s.key, v)}
                          disabled={busy === v}
                          className="inline-flex shrink-0 items-center gap-1 rounded border border-zinc-500/25 px-1.5 py-0.5 text-[10px] text-zinc-400 transition-colors hover:border-red-500/40 hover:bg-red-500/10 hover:text-red-400 disabled:opacity-50"
                          title={t('allowlistRemove', '从白名单移除')}
                        >
                          {busy === v ? <Loader2 size={10} className="animate-spin" /> : <Trash2 size={10} />}
                          {t('allowlistRemoveBtn', '移除')}
                        </button>
                      </div>
                    ))}
                  </div>
                )}
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

export default function AuditLogTab() {
  const { t } = useTranslation('audit');
  const [events, setEvents] = useState<AuditEvent[]>([]);
  const [loadedOnce, setLoadedOnce] = useState(false);
  const [onlyHits, setOnlyHits] = useState(true);
  const onlyHitsRef = useRef(true);
  const [handled, setHandledState] = useState<HandledMap>(() => loadHandled());
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [page, setPage] = useState(1);
  const [showAllowlist, setShowAllowlist] = useState(false);
  const aliveRef = useRef(true);
  const sigRef = useRef('');

  const mark = (id: string, status: 'done' | 'false_positive') => {
    setHandledState(setHandled(id, status, Date.now()));
  };

  // 误报一键加白名单: mark FP locally AND add the content hash to the server
  // allow_list so future identical content is suppressed everywhere.
  const falsePositive = async (e: AuditEvent): Promise<{ ok: boolean; msg: string }> => {
    const sha = e.subject?.payload_sha256;
    if (!sha) { mark(e.id, 'false_positive'); return { ok: false, msg: t('fpNoSha', '无内容哈希,仅本地标记') }; }
    try {
      await apiService.allowlistContent(sha, `false-positive via audit log: ${e.id}`);
      mark(e.id, 'false_positive');
      return { ok: true, msg: t('fpAllowlisted', '已加入白名单') };
    } catch {
      mark(e.id, 'false_positive');
      return { ok: false, msg: t('fpAllowlistErr', '本地已标,白名单写入失败') };
    }
  };

  const load = async () => {
    try {
      const res: any = await apiService.getAuditEvents(
        onlyHitsRef.current ? { limit: FETCH_LIMIT, severity: 'low,medium,high,critical' } : { limit: FETCH_LIMIT }
      );
      if (!aliveRef.current) return;
      const list: AuditEvent[] = Array.isArray(res?.data?.events) ? res.data.events : [];
      list.sort((a, b) => Date.parse(b.ts || '') - Date.parse(a.ts || ''));
      const sig = `${list.length}:${list[0]?.id || ''}:${list[list.length - 1]?.id || ''}`;
      if (sig !== sigRef.current) { sigRef.current = sig; setEvents(list); }
    } catch { /* poll failure non-fatal */ }
    finally { if (aliveRef.current) setLoadedOnce(true); }
  };

  useEffect(() => {
    aliveRef.current = true;
    let timer: ReturnType<typeof setTimeout> | null = null;
    const tick = async () => {
      if (typeof document === 'undefined' || !document.hidden) await load();
      if (aliveRef.current) timer = setTimeout(tick, POLL_MS);
    };
    tick();
    return () => { aliveRef.current = false; if (timer) clearTimeout(timer); };
  }, []);

  useEffect(() => {
    onlyHitsRef.current = onlyHits;
    sigRef.current = '';
    setPage(1);
    void load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [onlyHits]);

  const isAlert = (e: AuditEvent) => e.decision?.applied && ['block', 'redact', 'notify'].includes(e.decision?.action || '');
  const acked = useMemo(() => serverAckedIds(events), [events]);
  const visible = events.filter((e) => e.meta?.category !== 'meta_alert_ack');
  const rows = onlyHits ? visible.filter(isAlert) : visible;
  const openHitCount = visible.filter((e) => isAlert(e) && resolveStatus(e.id, handled, acked) === 'open').length;

  // The log can be long — paginate the master list.
  const PAGE_SIZE = 50;
  const totalPages = Math.max(1, Math.ceil(rows.length / PAGE_SIZE));
  const safePage = Math.min(page, totalPages);
  const pageRows = rows.slice((safePage - 1) * PAGE_SIZE, safePage * PAGE_SIZE);

  // Keep a valid selection; default to the first row.
  useEffect(() => {
    if (rows.length === 0) { if (selectedId !== null) setSelectedId(null); return; }
    if (!selectedId || !rows.some((r) => r.id === selectedId)) setSelectedId(rows[0].id);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [rows.map((r) => r.id).join(','), ]);

  const selected = rows.find((r) => r.id === selectedId) || null;

  return (
    <div data-id="audit-log" className="relative flex h-full flex-col">
      {showAllowlist && <AllowlistPanel onClose={() => setShowAllowlist(false)} />}
      {/* Header */}
      <div data-id="audit-log-header" className="flex shrink-0 items-center gap-3 border-b border-[var(--vsc-border-subtle)] px-3 py-2">
        <span className="text-xs text-[var(--vsc-text-muted)]">{t('eventsCount', { n: rows.length })}</span>
        {openHitCount > 0 && (
          <span data-id="audit-log-open-count" className="inline-flex items-center gap-1 rounded-full border border-zinc-500/30 bg-zinc-500/10 px-2 py-0.5 text-[11px] font-medium text-zinc-300">
            <CircleAlert size={11} />{t('logUnhandledCount', '{{n}} 条待处理', { n: openHitCount })}
          </span>
        )}
        <div className="flex-1" />
        <button
          data-id="audit-log-filter-toggle"
          onClick={() => setOnlyHits((v) => !v)}
          className={`rounded-md border px-2 py-1 text-[11px] transition-colors ${onlyHits ? 'border-zinc-500/30 bg-zinc-500/15 text-zinc-200' : 'border-[var(--vsc-border-subtle)] text-[var(--vsc-text-secondary)] hover:text-white'}`}
        >
          {onlyHits ? t('logOnlyHits', '只看告警') : t('logAll', '全部流量')}
        </button>
        <button
          data-id="audit-log-allowlist-btn"
          onClick={() => setShowAllowlist(true)}
          className="inline-flex items-center gap-1 rounded-md border border-[var(--vsc-border-subtle)] px-2 py-1 text-[11px] text-[var(--vsc-text-secondary)] transition-colors hover:text-white"
          title={t('allowlistOpen', '查看并管理白名单')}
        >
          <ListChecks size={12} />{t('allowlistTitle', '白名单')}
        </button>
        <button data-id="audit-log-refresh" onClick={load} className="rounded-md p-1.5 text-[var(--vsc-text-secondary)] transition-colors hover:bg-[var(--vsc-bg-hover)] hover:text-white" title={t('logRefresh', 'Refresh')}>
          <RefreshCw size={13} />
        </button>
      </div>

      {/* Master / detail — overflow-x-auto so narrow widths scroll instead of squeezing */}
      <div data-id="audit-log-body" className="flex min-h-0 flex-1 overflow-x-auto">
        {/* Master list */}
        <div data-id="audit-log-list" className="flex w-[300px] shrink-0 flex-col border-r border-[var(--vsc-border-subtle)]">
         <div data-id="audit-log-list-scroll" className="min-h-0 flex-1 overflow-y-auto">
          {pageRows.map((e) => {
            const dir = e.subject?.direction;
            const isBehavior = dir === 'behavior';
            const hit = isAlert(e);
            const status = resolveStatus(e.id, handled, acked);
            const sev = (e.findings || []).map((f) => f.severity || 'low').sort((a, b) => ['critical', 'high', 'medium', 'low'].indexOf(a) - ['critical', 'high', 'medium', 'low'].indexOf(b))[0] || 'low';
            const isOpen = hit && status === 'open';
            const sel = e.id === selectedId;
            return (
              <button
                key={e.id}
                data-id={`audit-log-row-${e.id}`}
                onClick={() => setSelectedId(e.id)}
                className={`flex w-full items-stretch gap-0 border-b border-[var(--vsc-border-subtle)] text-left transition-colors ${sel ? 'bg-white/[0.06]' : 'hover:bg-white/[0.03]'}`}
              >
                <span className={`w-[3px] shrink-0 ${isOpen ? sevAccent[sev] || sevAccent.low : 'bg-transparent'}`} />
                <span className="min-w-0 flex-1 px-2.5 py-2">
                  <span className="flex items-center gap-1.5">
                    <span className="shrink-0 text-[var(--vsc-text-muted)]" title={isBehavior ? t('dirBehavior', '行为(工具调用)') : dir}>
                      {isBehavior ? <Terminal size={11} className="text-[var(--vsc-text-muted)]" /> : dir === 'inbound' ? <ArrowDown size={11} className="text-[var(--vsc-text-muted)]" /> : <ArrowUp size={11} className="text-[var(--vsc-text-muted)]" />}
                    </span>
                    <span className="truncate font-mono text-[11px] text-[var(--vsc-text-secondary)]" title={e.identity?.agent_id || ''}>{e.identity?.agent_id || '—'}</span>
                    <span className="ml-auto shrink-0 text-[10px] text-[var(--vsc-text-muted)]">{fmtTime(e.ts)}</span>
                  </span>
                  <span className="mt-1 flex flex-wrap items-center gap-1">
                    {hit && <StatusBadge status={status} />}
                    {(e.findings || []).slice(0, 2).map((f, i) => (
                      <span key={i} className={`rounded border px-1 py-0.5 text-[9px] font-medium ${severityColor[f.severity || 'low'] || severityColor.low}`}>{ruleFriendly(t, f.rule_id, f.category)}</span>
                    ))}
                    {(e.findings?.length || 0) > 2 && <span className="text-[9px] text-[var(--vsc-text-muted)]">+{e.findings!.length - 2}</span>}
                    <ActionChip action={e.decision?.action} applied={e.decision?.applied} />
                  </span>
                </span>
              </button>
            );
          })}
          {loadedOnce && rows.length === 0 && (
            <div data-id="audit-log-empty" className="px-4 py-16 text-center text-[var(--vsc-text-muted)]">
              <Activity size={28} className="mx-auto mb-3 opacity-30" />
              <p className="text-xs">{onlyHits ? t('logNoHits', '暂无命中') : t('waitingTraffic')}</p>
            </div>
          )}
         </div>
          {totalPages > 1 && (
            <div data-id="audit-log-pager" className="flex shrink-0 items-center justify-between border-t border-[var(--vsc-border-subtle)] px-2 py-1.5 text-[11px]">
              <button data-id="audit-log-prev" onClick={() => setPage((p) => Math.max(1, p - 1))} disabled={safePage <= 1} className="rounded p-1 text-[var(--vsc-text-secondary)] transition-colors hover:bg-[var(--vsc-bg-hover)] hover:text-white disabled:cursor-not-allowed disabled:opacity-40">
                <ChevronLeft size={14} />
              </button>
              <span className="text-[var(--vsc-text-muted)]">{t('pagerOf', '{{cur}} / {{total}}', { cur: safePage, total: totalPages })}</span>
              <button data-id="audit-log-next" onClick={() => setPage((p) => Math.min(totalPages, p + 1))} disabled={safePage >= totalPages} className="rounded p-1 text-[var(--vsc-text-secondary)] transition-colors hover:bg-[var(--vsc-bg-hover)] hover:text-white disabled:cursor-not-allowed disabled:opacity-40">
                <ChevronRight size={14} />
              </button>
            </div>
          )}
        </div>

        {/* Detail */}
        <div data-id="audit-log-detail-pane" className="min-w-[360px] flex-1">
          {selected
            ? <DetailPanel key={selected.id} e={selected} status={resolveStatus(selected.id, handled, acked)} onMark={mark} onFalsePositive={falsePositive} />
            : (
              <div className="flex h-full items-center justify-center px-6 text-center text-[var(--vsc-text-muted)]">
                <div>
                  <Activity size={28} className="mx-auto mb-3 opacity-30" />
                  <p className="text-xs">{t('logSelectHint', '选择左侧一条告警查看详情')}</p>
                </div>
              </div>
            )}
        </div>
      </div>
    </div>
  );
}
