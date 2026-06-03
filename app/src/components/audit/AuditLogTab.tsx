import { useEffect, useRef, useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Activity, ArrowDown, ArrowUp, RefreshCw, ShieldX, EyeOff, Bell, FileText, Check, Flag, CircleAlert, ChevronRight, ChevronLeft, Info, Sparkles } from 'lucide-react';
import type { TFunction } from 'i18next';
import apiService from '../../services/api';
import { loadHandled, serverAckedIds, resolveStatus, setHandled, type HandledMap, type ResolvedStatus } from './auditHandled';

// Local audit log — backed by the REST endpoint GET /api/audit/events that the
// on-host mgr server actually serves. (The legacy LiveTab streams from the
// cloud gateway's /api/audit/live SSE, which is not registered locally, so it
// renders "not connected" on a desktop install. This tab is the local truth.)
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

const severityColor: Record<string, string> = {
  critical: 'bg-red-500/15 text-red-400 border-red-500/30',
  high: 'bg-orange-500/15 text-orange-400 border-orange-500/30',
  medium: 'bg-amber-500/15 text-amber-300 border-amber-500/30',
  low: 'bg-zinc-500/15 text-zinc-400 border-zinc-500/30',
};

function ActionChip({ action, applied }: { action?: string; applied?: boolean }) {
  const { t } = useTranslation('audit');
  if (!action || action === 'none') return null;
  const map: Record<string, { cls: string; icon: any; label: string }> = {
    block: { cls: 'bg-red-500/15 text-red-400 border-red-500/30', icon: ShieldX, label: t('guardBlocked') },
    redact: { cls: 'bg-amber-500/15 text-amber-300 border-amber-500/30', icon: EyeOff, label: t('guardRedacted') },
    notify: { cls: 'bg-blue-500/15 text-blue-400 border-blue-500/30', icon: Bell, label: t('logActionNotify', 'Notify') },
    log: { cls: 'bg-zinc-500/10 text-zinc-400 border-zinc-500/20', icon: FileText, label: t('logActionLog', 'Log') },
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
    return <span className="inline-flex items-center gap-1 rounded border border-emerald-500/30 bg-emerald-500/10 px-1.5 py-0.5 text-[10px] font-medium text-emerald-400"><Check size={10} />{t('statusDone', '已处理')}</span>;
  }
  if (status === 'false_positive') {
    return <span className="inline-flex items-center gap-1 rounded border border-zinc-500/25 bg-zinc-500/10 px-1.5 py-0.5 text-[10px] font-medium text-zinc-400"><Flag size={10} />{t('statusFalsePositive', '误报')}</span>;
  }
  return <span className="inline-flex items-center gap-1 rounded border border-amber-500/35 bg-amber-500/10 px-1.5 py-0.5 text-[10px] font-medium text-amber-300"><CircleAlert size={10} />{t('statusOpen', '未处理')}</span>;
}

const fmtTime = (ts: string) => {
  const d = new Date(ts);
  if (isNaN(d.getTime())) return '—';
  return d.toLocaleTimeString(undefined, { hour12: false, hour: '2-digit', minute: '2-digit', second: '2-digit' });
};

// ── Plain-language helpers: turn raw audit fields into something a human reads.
function sevLabel(t: TFunction, s?: string) {
  return t(`sev_${s}` as any, { defaultValue: s || '' }) as string;
}
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
  if (s.includes('api_key') || s.includes('apikey')) return t('ruleApiKey', 'API 密钥');
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

function EventDetail({ e }: { e: AuditEvent }) {
  const { t } = useTranslation('audit');
  const short = (s?: string, n = 16) => (s && s.length > n ? `${s.slice(0, n)}…` : s || '');

  const [snap, setSnap] = useState<string | null>(null);
  const [snapLoading, setSnapLoading] = useState(false);
  const loadSnap = async () => {
    if (!e.meta?.snapshot_ref) return;
    setSnapLoading(true);
    try {
      const res: any = await apiService.getAuditSnapshot(e.meta.snapshot_ref);
      const data = res?.data;
      setSnap(typeof data === 'string' ? data : JSON.stringify(data, null, 2));
    } catch {
      setSnap(t('snapFailed', '快照读取失败'));
    } finally {
      setSnapLoading(false);
    }
  };

  const [triage, setTriage] = useState<any | null>(null);
  const [triaging, setTriaging] = useState(false);
  const runTriage = async () => {
    setTriaging(true);
    try {
      const f = e.findings?.[0];
      const sp = f?.spans?.find((s) => s.path) || f?.spans?.[0];
      const res: any = await apiService.auditTriage({
        rule_id: f?.rule_id || '', severity: f?.severity || '', category: f?.category || '',
        path: sp?.path || '', context: sp?.context || '',
        agent_id: e.identity?.agent_id || '', agent_type: e.identity?.agent_type || '',
        provider: e.subject?.provider || '', model: e.subject?.model || '',
        direction: e.subject?.direction || '', action: e.decision?.action || '',
        payload_size_kb: Math.round((e.subject?.payload_size || 0) / 1024),
      });
      setTriage(res?.data || null);
    } catch {
      setTriage({ verdict: '', conclusion: t('triageFailed', 'AI 研判失败,请重试'), action: '', confidence: '', source: '' });
    } finally {
      setTriaging(false);
    }
  };
  const verdictColor: Record<string, string> = {
    false_positive: 'border-emerald-500/30 bg-emerald-500/10 text-emerald-400',
    low: 'border-zinc-500/25 bg-zinc-500/10 text-zinc-300',
    medium: 'border-amber-500/30 bg-amber-500/10 text-amber-300',
    high: 'border-orange-500/30 bg-orange-500/10 text-orange-400',
    critical: 'border-red-500/40 bg-red-500/10 text-red-400',
  };

  // Build a one-paragraph plain-language read of what happened.
  const f0 = e.findings?.[0];
  const count = (e.findings || []).reduce((n, f) => n + (f.match_count || 1), 0) || (e.findings?.length || 0);
  const target = e.subject?.provider || t('sumTargetFallback', '外部服务');
  const agent = e.identity?.agent_id || t('sumAgentFallback', '某 agent');
  const ctx = e.subject?.direction === 'inbound'
    ? t('sumCtxIn', '{{target}} 返回给 {{agent}} 的内容里', { target, agent })
    : t('sumCtxOut', '{{agent}} 发往 {{target}} 的请求里', { agent, target });
  const summary = e.meta?.allowlisted_by
    ? t('sumAllowlisted', '{{ctx}}虽命中规则,但该来源在白名单中,已直接放行、不告警。', { ctx })
    : count > 0
      ? t('sumFound', '{{ctx}}检测到 {{count}} 处{{kind}}({{sev}}风险),系统{{act}}。', {
          ctx, count, kind: kindLabel(t, f0?.category), sev: sevLabel(t, f0?.severity), act: actionPhrase(t, e.decision?.action),
        })
      : t('sumClean', '{{ctx}}未发现敏感内容,正常放行。', { ctx });
  const suppressNote = e.meta?.notify_suppressed_by
    ? t('sumSuppressNote', ' 本次通知因「{{reason}}」被抑制(避免重复打扰),但事件已记录。', { reason: suppressReason(t, e.meta.notify_suppressed_by) })
    : '';
  // Heuristic: high-entropy matches inside a large payload are almost always
  // base64-encoded images/blobs (e.g. screenshots), not real secrets.
  const looksLikeBlob = (e.findings || []).some((f) => (f.rule_id || '').includes('high_entropy')) && (e.subject?.payload_size || 0) > 50 * 1024;
  const blobHint = looksLikeBlob
    ? t('sumImageHint', ' 提示:大请求里的高熵串通常是图片/base64 编码内容,多为误报 — 可「标记误报」或加白名单。')
    : '';
  const loc = f0?.spans?.find((s) => s.path)?.path;
  const locNote = loc ? t('sumLocation', ' 命中位置:{{loc}}。', { loc }) : '';

  return (
    <div data-id={`audit-log-detail-${e.id}`} className="mt-1 space-y-3 rounded-md border border-[var(--vsc-border-subtle)] bg-black/20 px-3 py-3 text-xs">
      {/* Plain-language summary */}
      <div className="flex gap-2 rounded-md border border-blue-500/20 bg-blue-500/[0.06] px-3 py-2 text-[12px] leading-relaxed text-[var(--vsc-text-secondary)]">
        <Info size={14} className="mt-0.5 shrink-0 text-blue-400" />
        <span>{summary}{locNote}{suppressNote}{blobHint && <span className="text-amber-300/90">{blobHint}</span>}</span>
      </div>

      {/* AI triage verdict */}
      <div data-id={`audit-log-triage-${e.id}`}>
        {!triage ? (
          <button
            onClick={runTriage}
            disabled={triaging}
            className="inline-flex items-center gap-1.5 rounded-md border border-blue-500/30 bg-blue-500/10 px-2.5 py-1 text-[11px] font-medium text-blue-300 transition-colors hover:bg-blue-500/15 disabled:opacity-50"
          >
            <Sparkles size={12} className={triaging ? 'animate-pulse' : ''} />
            {triaging ? t('triageRunning', 'AI 研判中…') : t('triageBtn', 'AI 研判')}
          </button>
        ) : (
          <div className={`rounded-md border px-3 py-2 ${verdictColor[triage.verdict] || verdictColor.low}`}>
            <div className="flex items-center gap-2 text-[12px] font-semibold">
              <Sparkles size={12} />
              {triage.verdict ? String(t(`verdict_${triage.verdict}` as any, { defaultValue: triage.verdict })) : t('triageFailedShort', '研判失败')}
              {triage.confidence && <span className="text-[10px] font-normal opacity-70">· {t('triageConfidence', '置信度')} {String(t(`conf_${triage.confidence}` as any, { defaultValue: triage.confidence }))}</span>}
              <button onClick={runTriage} disabled={triaging} className="ml-auto text-[10px] font-normal opacity-60 hover:opacity-100">{t('triageRedo', '重判')}</button>
              <span className="text-[9px] font-normal opacity-50">{triage.source === 'llm' ? t('triageSrcAi', 'AI') : triage.source === 'heuristic' ? t('triageSrcRule', '规则') : ''}</span>
            </div>
            {triage.conclusion && <div className="mt-1 text-[11px] text-[var(--vsc-text-secondary)]">{triage.conclusion}</div>}
            {triage.action && <div className="mt-1 text-[11px] text-[var(--vsc-text)]"><span className="opacity-60">{t('triageAction', '建议')}: </span>{triage.action}</div>}
          </div>
        )}
      </div>

      {/* Redacted snapshot (notify alerts) */}
      {e.meta?.snapshot_ref && (
        <div data-id={`audit-log-snapshot-${e.id}`}>
          {!snap ? (
            <button
              onClick={loadSnap}
              disabled={snapLoading}
              className="inline-flex items-center gap-1.5 rounded-md border border-[var(--vsc-border-subtle)] bg-[var(--vsc-bg-hover)] px-2.5 py-1 text-[11px] text-[var(--vsc-text-secondary)] transition-colors hover:text-white disabled:opacity-50"
            >
              <FileText size={12} />{snapLoading ? t('snapLoading', '加载中…') : t('snapView', '查看脱敏快照')}
            </button>
          ) : (
            <div>
              <div className="mb-1 flex items-center gap-3 text-[10px] text-[var(--vsc-text-muted)]">
                <span>{t('snapTitle', '脱敏快照(密钥已打码)')}</span>
                <button onClick={() => downloadText(snap, `${e.id}.json`)} className="hover:text-white">{t('snapDownload', '下载完整')}</button>
                <button onClick={() => setSnap(null)} className="hover:text-white">{t('snapClose', '收起')}</button>
              </div>
              <pre className="max-h-64 overflow-auto whitespace-pre-wrap break-all rounded bg-black/40 p-2 text-[10px] text-[var(--vsc-text-secondary)]">{snap.length > 20000 ? snap.slice(0, 20000) + '\n…(' + t('snapTruncated', '已截断,完整请下载') + ')' : snap}</pre>
            </div>
          )}
        </div>
      )}

      {/* Findings + matched snippets */}
      {(e.findings?.length || 0) > 0 && (
        <div>
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
                        {s.path && <div className="font-mono text-[9px] text-blue-300/70 break-all">{s.path}</div>}
                        {s.context
                          ? <div className="font-mono text-[10px] text-[var(--vsc-text-secondary)] break-all"><span className="text-zinc-500">…</span>{s.context}<span className="text-zinc-500">…</span></div>
                          : <div className="font-mono text-[10px] text-amber-300/90 break-all">{s.preview}</div>}
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
      {(e.meta?.allowlisted_by || e.meta?.notify_suppressed_by || e.meta?.pre_redact_ref || e.meta?.pipeline_error) && (
        <div className="flex flex-wrap gap-1.5">
          {e.meta?.allowlisted_by && <span className="rounded border border-zinc-500/20 bg-zinc-500/10 px-1.5 py-0.5 text-[10px] text-zinc-400">{t('logAllowlisted', '已放行')}: {e.meta.allowlisted_by}{e.meta.allowlist_match ? ` (${short(e.meta.allowlist_match)})` : ''}</span>}
          {e.meta?.notify_suppressed_by && <span className="rounded border border-zinc-500/20 bg-zinc-500/10 px-1.5 py-0.5 text-[10px] text-zinc-400">{t('detailNotifySuppressed', '告警抑制')}: {suppressReason(t, e.meta.notify_suppressed_by)}</span>}
          {e.meta?.pre_redact_ref && <span className="rounded border border-amber-500/20 bg-amber-500/10 px-1.5 py-0.5 text-[10px] text-amber-300/80">{t('detailPreRedact', '原文已归档')}</span>}
          {e.meta?.pipeline_error && <span className="rounded border border-red-500/30 bg-red-500/10 px-1.5 py-0.5 text-[10px] text-red-400">{e.meta.pipeline_error}</span>}
        </div>
      )}

      {/* Context */}
      <div className="space-y-1">
        <KV label={t('detailEventId', '事件ID')} value={e.id} mono />
        <KV label={t('detailAgent', 'Agent')} value={[e.identity?.agent_id, e.identity?.agent_type].filter(Boolean).join(' · ')} mono />
        <KV label={t('detailUser', '用户/会话')} value={[e.identity?.user_id, short(e.identity?.session_id)].filter(Boolean).join(' · ')} mono />
        <KV label={t('detailProvider', '目标')} value={[e.subject?.provider, e.subject?.model].filter(Boolean).join(' · ')} mono />
        <KV label={t('detailDirection', '方向')} value={e.subject?.direction === 'inbound' ? t('dirInbound', '接收(模型返回)') : e.subject?.direction === 'outbound' ? t('dirOutbound', '外发(发给模型)') : e.subject?.direction} />
        <KV label={t('detailPayload', '负载')} value={typeof e.subject?.payload_size === 'number' ? `${(e.subject.payload_size / 1024).toFixed(1)} KB` : undefined} />
        <KV label={t('detailDecision', '决策')} value={[actionPhrase(t, e.decision?.action), e.decision?.evaluated_inline ? t('decInline', '实时') : t('decAsync', '旁路'), e.decision?.fail_mode === 'open' ? t('failOpen', '失败放行') : e.decision?.fail_mode === 'closed' ? t('failClosed', '失败阻断') : ''].filter(Boolean).join(' · ')} />
        <KV label={t('detailScanner', '扫描耗时')} value={typeof e.meta?.scanner_duration_ms === 'number' ? `${e.meta.scanner_duration_ms} ms` : undefined} />
        <KV label={t('detailPolicyHash', '策略哈希')} value={short(e.meta?.policy_hash, 20)} mono />
        <KV label={t('detailSource', '来源')} value={sourceLabel(t, e.identity?.source_channel)} />
      </div>
    </div>
  );
}

export default function AuditLogTab() {
  const { t } = useTranslation('audit');
  const [events, setEvents] = useState<AuditEvent[]>([]);
  const [ok, setOk] = useState(false);
  const [loadedOnce, setLoadedOnce] = useState(false);
  // Default to alerts-only — clean pass-through traffic is noise. Toggle off to
  // see everything (useful to confirm traffic is flowing at all).
  const [onlyHits, setOnlyHits] = useState(true);
  const [handled, setHandledState] = useState<HandledMap>(() => loadHandled());
  const [expandedId, setExpandedId] = useState<string | null>(null);
  const [page, setPage] = useState(1);
  const aliveRef = useRef(true);
  const sigRef = useRef('');

  const mark = (id: string, status: 'done' | 'false_positive') => {
    setHandledState(setHandled(id, status, Date.now()));
  };

  const load = async () => {
    try {
      const res: any = await apiService.getAuditEvents({ limit: FETCH_LIMIT });
      if (!aliveRef.current) return;
      const list: AuditEvent[] = Array.isArray(res?.data?.events) ? res.data.events : [];
      // newest first
      list.sort((a, b) => Date.parse(b.ts || '') - Date.parse(a.ts || ''));
      // Only push a new array into state when the data actually changed —
      // otherwise the 4s poll re-renders the whole list (incl. detail panels)
      // every tick and the tab feels frozen.
      const sig = `${list.length}:${list[0]?.id || ''}:${list[list.length - 1]?.id || ''}`;
      if (sig !== sigRef.current) {
        sigRef.current = sig;
        setEvents(list);
      }
      setOk(true);
    } catch {
      if (aliveRef.current) setOk(false);
    } finally {
      if (aliveRef.current) setLoadedOnce(true);
    }
  };

  useEffect(() => {
    aliveRef.current = true;
    let timer: ReturnType<typeof setTimeout> | null = null;
    const tick = async () => {
      // Skip the round-trip while the tab is backgrounded — no point parsing
      // events nobody is looking at, and it keeps the main thread free.
      if (typeof document === 'undefined' || !document.hidden) await load();
      if (aliveRef.current) timer = setTimeout(tick, POLL_MS);
    };
    tick();
    return () => { aliveRef.current = false; if (timer) clearTimeout(timer); };
  }, []);

  // Reset to the first page whenever the filter changes.
  useEffect(() => { setPage(1); }, [onlyHits]);

  // An "alert" = the pipeline acted (block / redact) OR flagged (notify). These
  // are what a human needs to triage, so they get a handled status + feedback.
  // (The FAB red badge stays block/redact-only per the configured threshold.)
  const isAlert = (e: AuditEvent) => e.decision?.applied && ['block', 'redact', 'notify'].includes(e.decision?.action || '');
  // The ack ledger lives in the same stream; hide ack meta-events from the log
  // and instead use them to resolve each alert's handled status.
  const acked = serverAckedIds(events);
  const visible = events.filter((e) => e.meta?.category !== 'meta_alert_ack');
  const rows = onlyHits ? visible.filter(isAlert) : visible;
  const openHitCount = visible.filter((e) => isAlert(e) && resolveStatus(e.id, handled, acked) === 'open').length;

  const PAGE_SIZE = 25;
  const totalPages = Math.max(1, Math.ceil(rows.length / PAGE_SIZE));
  const safePage = Math.min(page, totalPages);
  const pageRows = rows.slice((safePage - 1) * PAGE_SIZE, safePage * PAGE_SIZE);

  return (
    <div data-id="audit-log" className="space-y-3">
      <div data-id="audit-log-header" className="flex items-center gap-3">
        <div className={`flex items-center gap-1.5 text-xs ${!loadedOnce ? 'text-[var(--vsc-text-muted)]' : ok ? 'text-emerald-400' : 'text-red-400'}`}>
          <div className={`w-2 h-2 rounded-full ${!loadedOnce ? 'bg-amber-400 animate-pulse' : ok ? 'bg-emerald-400 animate-pulse' : 'bg-red-400'}`} />
          {!loadedOnce ? t('logConnecting', '连接中…') : ok ? t('connected') : t('disconnected')}
        </div>
        <span className="text-xs text-[var(--vsc-text-muted)]">{t('eventsCount', { n: rows.length })}</span>
        {openHitCount > 0 && (
          <span className="inline-flex items-center gap-1 rounded-full border border-amber-500/35 bg-amber-500/10 px-2 py-0.5 text-[11px] font-medium text-amber-300">
            <CircleAlert size={11} />{t('logUnhandledCount', '{{n}} 条待处理', { n: openHitCount })}
          </span>
        )}
        <div className="flex-1" />
        <button
          data-id="audit-log-only-hits"
          onClick={() => setOnlyHits((v) => !v)}
          className={`px-2.5 py-1 text-[11px] rounded-md border transition-colors ${onlyHits ? 'bg-red-500/15 text-red-400 border-red-500/30' : 'bg-[var(--vsc-bg-hover)] text-[var(--vsc-text-secondary)] border-[var(--vsc-border-subtle)] hover:text-white'}`}
        >
          {t('logOnlyHits', '只看命中')}
        </button>
        <button data-id="audit-log-refresh" onClick={load} className="p-1.5 rounded-md text-[var(--vsc-text-secondary)] hover:text-white hover:bg-[var(--vsc-bg-hover)] transition-colors" title={t('logRefresh', 'Refresh')}>
          <RefreshCw size={13} />
        </button>
      </div>

      <div data-id="audit-log-list" className="space-y-1">
        {pageRows.map((e) => {
          const dir = e.subject?.direction;
          const hit = isAlert(e);
          const status = resolveStatus(e.id, handled, acked);
          const expanded = expandedId === e.id;
          return (
            <div key={e.id} data-id={`audit-log-item-${e.id}`}>
            <div
              data-id={`audit-log-row-${e.id}`}
              onClick={() => setExpandedId((cur) => (cur === e.id ? null : e.id))}
              className={`flex items-center gap-2.5 px-3 py-2 rounded-md border text-xs transition-colors cursor-pointer ${hit && status === 'open' ? 'bg-amber-500/[0.05] border-amber-500/25 hover:border-amber-500/40' : 'bg-[var(--vsc-bg-secondary)] border-[var(--vsc-border-subtle)] hover:border-[var(--vsc-border)]'}`}
            >
              <ChevronRight size={13} className={`shrink-0 text-[var(--vsc-text-muted)] transition-transform ${expanded ? 'rotate-90' : ''}`} />
              <span className="text-[var(--vsc-text-muted)] w-16 shrink-0">{fmtTime(e.ts)}</span>
              <span className="shrink-0 text-[var(--vsc-text-muted)]" title={dir}>
                {dir === 'inbound' ? <ArrowDown size={12} className="text-blue-400" /> : <ArrowUp size={12} className="text-purple-400" />}
              </span>
              <span className="shrink-0 font-mono text-[11px] text-[var(--vsc-text-secondary)] truncate max-w-[110px]" title={`${e.identity?.agent_id || ''} ${e.identity?.agent_type || ''}`}>
                {e.identity?.agent_id || '—'}
              </span>
              <span className="text-[var(--vsc-text-muted)] truncate flex-1 min-w-0" title={`${e.subject?.provider || ''} ${e.subject?.model || ''}`}>
                {e.subject?.provider ? <span className="text-blue-400/80">{e.subject.provider}</span> : null}
                {e.subject?.model ? <span className="ml-1 font-mono">{e.subject.model}</span> : null}
              </span>
              <div className="flex items-center gap-1 shrink-0">
                {e.meta?.allowlisted_by ? (
                  <span className="rounded border border-zinc-500/20 bg-zinc-500/10 px-1.5 py-0.5 text-[10px] text-zinc-400">{t('logAllowlisted', '已放行')}</span>
                ) : (
                  (e.findings || []).slice(0, 3).map((f, i) => (
                    <span key={i} className={`rounded border px-1.5 py-0.5 text-[10px] font-medium ${severityColor[f.severity || 'low'] || severityColor.low}`} title={`${f.category || ''} ×${f.match_count || 1}`}>
                      {f.rule_id}
                    </span>
                  ))
                )}
                {(e.findings?.length || 0) > 3 && <span className="text-[10px] text-[var(--vsc-text-muted)]">+{(e.findings!.length - 3)}</span>}
              </div>
              <ActionChip action={e.decision?.action} applied={e.decision?.applied} />
              {hit && (
                <>
                  <StatusBadge status={status} />
                  {status === 'open' && (
                    <div className="flex items-center gap-0.5 shrink-0">
                      <button
                        data-id={`audit-log-mark-done-${e.id}`}
                        onClick={(ev) => { ev.stopPropagation(); mark(e.id, 'done'); }}
                        title={t('markDone', '标记已处理')}
                        className="rounded p-1 text-[var(--vsc-text-muted)] hover:bg-emerald-500/15 hover:text-emerald-400 transition-colors"
                      >
                        <Check size={13} />
                      </button>
                      <button
                        data-id={`audit-log-mark-fp-${e.id}`}
                        onClick={(ev) => { ev.stopPropagation(); mark(e.id, 'false_positive'); }}
                        title={t('markFalsePositive', '标记误报')}
                        className="rounded p-1 text-[var(--vsc-text-muted)] hover:bg-zinc-500/20 hover:text-zinc-300 transition-colors"
                      >
                        <Flag size={13} />
                      </button>
                    </div>
                  )}
                </>
              )}
            </div>
            {expanded && <EventDetail e={e} />}
            </div>
          );
        })}

        {loadedOnce && rows.length === 0 && (
          <div data-id="audit-log-empty" className="text-center py-16 text-[var(--vsc-text-muted)]">
            <Activity size={32} className="mx-auto mb-3 opacity-30" />
            <p>{onlyHits ? t('logNoHits', '暂无命中') : t('waitingTraffic')}</p>
            <p className="text-[10px] mt-1">{t('eventsLive')}</p>
          </div>
        )}
      </div>

      {totalPages > 1 && (
        <div data-id="audit-log-pager" className="flex items-center justify-center gap-2 pt-1 text-xs">
          <button
            data-id="audit-log-prev"
            onClick={() => setPage((p) => Math.max(1, p - 1))}
            disabled={safePage <= 1}
            className="flex items-center gap-1 rounded-md border border-[var(--vsc-border-subtle)] bg-[var(--vsc-bg-hover)] px-2.5 py-1 text-[var(--vsc-text-secondary)] transition-colors hover:text-white disabled:cursor-not-allowed disabled:opacity-40"
          >
            <ChevronLeft size={13} />{t('pagerPrev', '上一页')}
          </button>
          <span className="text-[var(--vsc-text-muted)]">{t('pagerOf', '{{cur}} / {{total}}', { cur: safePage, total: totalPages })}</span>
          <button
            data-id="audit-log-next"
            onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
            disabled={safePage >= totalPages}
            className="flex items-center gap-1 rounded-md border border-[var(--vsc-border-subtle)] bg-[var(--vsc-bg-hover)] px-2.5 py-1 text-[var(--vsc-text-secondary)] transition-colors hover:text-white disabled:cursor-not-allowed disabled:opacity-40"
          >
            {t('pagerNext', '下一页')}<ChevronRight size={13} />
          </button>
        </div>
      )}
    </div>
  );
}
