import { useEffect, useMemo, useRef, useState } from 'react';
import {
  Building2, Send, X, CheckCircle2, Clock, Loader2, ClipboardCheck,
  ShieldCheck, AtSign, Circle, Eye, CircleDot, Megaphone, MessageSquare, Users, ArrowRight,
} from 'lucide-react';

/*
 * MeetingRoom — 「办公室 / 会议室」原型（data-id="meeting-room"）。
 *
 * 纯 UI 原型，先不接任何接口；派发 / 回报 / 互通都是本地 mock + setTimeout 模拟。
 *
 * 概念：办公室本身 = w-10001（总负责人 / 总控）。它只管理、不下场、不亲自验收。
 * 通信三通道：
 *   - 广播 total → 全体（公告/目标）
 *   - 单聊 total → 某成员（派任务）
 *   - 同事互通 成员 ↔ 成员（仅限花名册里声明的"协作对象"边，如 dev↔QA / dev↔架构师）
 * 花名册：每人一份档案（职责 + 协作对象）。
 * 派任务→成员在自己 agent 干活（过程不进办公室）→回报 work done→总控不亲自验，派 QA 验收。
 *
 * 接接口时替换标了 TODO(api) 的点（全是 cicy-agent msg / reply）。
 */

type MemberStatus = 'idle' | 'working' | 'done';

interface Member {
  id: string;            // w-xxxxx
  name: string;
  role: string;
  emoji: string;
  model: string;
  intro: string;
  duties: string[];
  collaborators: string[]; // 协作对象（可直接互通的成员 id）
}

type EntryKind = 'dispatch' | 'broadcast' | 'peer' | 'done' | 'review' | 'note';
type ReviewState = 'pending' | 'passed' | 'qa';

interface Entry {
  id: number;
  kind: EntryKind;
  from: string;          // 'w-10001' | member id
  to?: string;           // member id（dispatch / peer / qa）
  text: string;
  ts: string;
  review?: ReviewState;
}

const SELF = 'w-10001';

// ── mock 花名册（对应 cicy-team 角色；真实环境来自 w-10001 名下绑定的 worker）──
const MOCK_MEMBERS: Member[] = [
  {
    id: 'w-10010', name: '架构师 Aria', role: 'dev-senior', emoji: '🏛️', model: 'deepseek-v4-pro',
    intro: '把需求拆成可交付的技术任务，定接口与目录结构，做关键技术选型。',
    duties: ['需求 → 技术方案拆解', '定义接口 / 数据结构', '关键技术选型', 'code review 把关'],
    collaborators: ['w-10011', 'w-10014'],
  },
  {
    id: 'w-10011', name: '前端 Finn', role: 'dev-junior', emoji: '🎨', model: 'deepseek-v4-pro',
    intro: '按任务卡实现前端组件与页面，注重交互细节与可访问性。',
    duties: ['实现 React 组件 / 页面', '对齐设计稿与交互', '处理边界与加载态', '自测后提交'],
    collaborators: ['w-10010', 'w-10012'],
  },
  {
    id: 'w-10012', name: '测试 Quinn', role: 'qa', emoji: '🧪', model: 'deepseek-v4-pro',
    intro: '独立验收交付物：跑用例、核对验收标准、提 bug，给出 PASS / FAIL 结论。',
    duties: ['编写 / 执行测试用例', '核对验收标准', '提交 bug issue', '出验收结论'],
    collaborators: ['w-10011'],
  },
  {
    id: 'w-10013', name: '运维 Ops', role: 'ops', emoji: '🚀', model: 'deepseek-v4-pro',
    intro: '构建、部署、发布与线上监控，保证交付物可上线、可回滚。',
    duties: ['构建 / 部署', '发布与回滚', '线上监控告警', '环境与配置'],
    collaborators: ['w-10011', 'w-10012'],
  },
  {
    id: 'w-10014', name: '安全 Sage', role: 'reviewer', emoji: '🛡️', model: 'deepseek-v4-pro',
    intro: '从安全角度审查改动：密钥泄露、注入、权限、依赖风险。',
    duties: ['安全审查', '密钥 / PII 扫描', '权限与依赖风险', '出安全结论'],
    collaborators: ['w-10010', 'w-10011'],
  },
];

const STATUS_META: Record<MemberStatus, { label: string; dot: string; text: string }> = {
  idle:    { label: '空闲',   dot: 'text-zinc-600',  text: 'text-zinc-500' },
  working: { label: '工作中', dot: 'text-amber-400', text: 'text-amber-300' },
  done:    { label: '待验收', dot: 'text-emerald-400', text: 'text-emerald-300' },
};

function nowStamp(): string {
  const d = new Date();
  const p = (n: number) => String(n).padStart(2, '0');
  return `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`;
}

export default function MeetingRoom() {
  const [members] = useState<Member[]>(MOCK_MEMBERS);
  const [statuses, setStatuses] = useState<Record<string, MemberStatus>>(
    () => Object.fromEntries(MOCK_MEMBERS.map((m) => [m.id, 'idle'])),
  );
  const [detailId, setDetailId] = useState<string | null>(null);
  const [targetId, setTargetId] = useState<string | null>(null);
  const [mode, setMode] = useState<'single' | 'broadcast'>('single');
  const [text, setText] = useState('');
  const [mentionOpen, setMentionOpen] = useState(false);
  const [entries, setEntries] = useState<Entry[]>([
    { id: 1, kind: 'note', from: SELF, text: '办公室就绪。广播=对全体公告；单聊=给某成员派任务；成员之间按花名册的协作关系可直接互通。', ts: nowStamp() },
  ]);

  const seq = useRef(2);
  const timelineRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);

  const memberById = useMemo(() => Object.fromEntries(members.map((m) => [m.id, m])), [members]);
  const detail = detailId ? memberById[detailId] : null;
  const target = targetId ? memberById[targetId] : null;

  useEffect(() => {
    const node = timelineRef.current;
    if (node) node.scrollTop = node.scrollHeight;
  }, [entries]);

  const pushEntry = (e: Omit<Entry, 'id' | 'ts'> & { ts?: string }) => {
    const id = seq.current++;
    setEntries((prev) => [...prev, { id, ts: e.ts ?? nowStamp(), ...e }]);
    return id;
  };

  const selectMember = (m: Member) => {
    setDetailId(m.id);
    setTargetId(m.id);
    setMode('single');
    setMentionOpen(false);
    inputRef.current?.focus();
  };

  // work done 回报（mock 一个成员干完）
  const simulateDone = (who: string, doneText = '✅ work done') => {
    window.setTimeout(() => {
      pushEntry({ kind: 'done', from: who, text: doneText, review: 'pending' });
      setStatuses((s) => ({ ...s, [who]: 'done' }));
    }, 2600);
  };

  const send = () => {
    const body = text.trim();
    if (!body) return;

    if (mode === 'broadcast') {
      // 广播：总控 → 全体（公告，不产生 work done 回报）
      // TODO(api): 遍历所有 worker pane cicy-agent msg。
      pushEntry({ kind: 'broadcast', from: SELF, text: body });
      setText('');
      return;
    }

    if (!target) return;
    // 单聊派任务：总控 → @member（mock：cicy-agent msg <target> --callback）
    pushEntry({ kind: 'dispatch', from: SELF, to: target.id, text: body });
    setStatuses((s) => ({ ...s, [target.id]: 'working' }));
    setText('');
    setMentionOpen(false);
    simulateDone(target.id);
  };

  // 取回工作内容（mock：cicy-agent reply <member>）
  const fetchWork = (memberId: string) => {
    const m = memberById[memberId];
    pushEntry({
      kind: 'note', from: SELF,
      text: `📄 取回 ${m?.name ?? memberId} 的工作内容（cicy-agent reply ${memberId}）：已完成，产出已写入工作区。（原型占位）`,
    });
  };

  const passReview = (entryId: number, memberId: string) => {
    setEntries((prev) => prev.map((e) => (e.id === entryId ? { ...e, review: 'passed' } : e)));
    setStatuses((s) => ({ ...s, [memberId]: 'idle' }));
    pushEntry({ kind: 'review', from: SELF, text: `验收通过 ✓（${memberById[memberId]?.name ?? memberId}）` });
  };

  const assignQA = (entryId: number, memberId: string) => {
    const qa = members.find((m) => m.role === 'qa');
    setEntries((prev) => prev.map((e) => (e.id === entryId ? { ...e, review: 'qa' } : e)));
    if (qa) {
      pushEntry({ kind: 'dispatch', from: SELF, to: qa.id, text: `验收 ${memberById[memberId]?.name ?? memberId} 的交付物，核对验收标准后给结论。` });
      setStatuses((s) => ({ ...s, [qa.id]: 'working' }));
      simulateDone(qa.id, '✅ work done — 验收结论：PASS');
    }
  };

  // 同事互通（mock：成员 A → 协作对象 B，cicy-agent msg）
  const peerContact = (fromId: string, toId: string) => {
    const a = memberById[fromId]; const b = memberById[toId];
    pushEntry({ kind: 'peer', from: fromId, to: toId, text: `（就相关事宜与 ${b?.name ?? toId} 直接对接）` });
    pushEntry({ kind: 'note', from: SELF, text: `🔗 ${a?.name ?? fromId} ↔ ${b?.name ?? toId} 已建立直接通信（结论会汇报回办公室）` });
  };

  const onKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); send(); }
  };
  const onChange = (v: string) => {
    setText(v);
    if (mode === 'single') setMentionOpen(/(^|\s)@$/.test(v));
  };
  const pickMention = (m: Member) => {
    setTargetId(m.id); setMode('single');
    setText((v) => v.replace(/@$/, ''));
    setMentionOpen(false);
    inputRef.current?.focus();
  };

  const canSend = text.trim() && (mode === 'broadcast' || !!target);

  return (
    <div data-id="meeting-room" className="absolute inset-0 flex flex-col bg-[#0A0A0A] text-zinc-300">
      {/* header */}
      <div data-id="meeting-room-header" className="h-12 shrink-0 border-b border-[var(--vsc-border)] flex items-center gap-2.5 px-4 bg-[#0e0e0e]">
        <Building2 className="h-4 w-4 text-sky-400" />
        <span className="text-sm font-semibold text-zinc-100">办公室</span>
        <span className="text-[11px] text-zinc-500">· 总控 {SELF}</span>
        <span className="ml-auto text-[11px] text-zinc-600">广播 / 单聊派任务 · 成员按协作关系互通 · 完成回报 work done → 派 QA 验收</span>
      </div>

      <div className="flex min-h-0 flex-1">
        {/* 左：花名册 */}
        <aside data-id="meeting-room-roster" className="w-[244px] min-w-[244px] shrink-0 border-r border-[var(--vsc-border)] flex flex-col bg-[#0b0b0b]">
          <div className="flex items-center gap-1.5 px-3 py-2 text-[11px] font-medium uppercase tracking-wide text-zinc-600">
            <Users className="h-3 w-3" /> 花名册 · {members.length} 人
          </div>
          <div className="flex-1 overflow-auto px-2 pb-2 space-y-1">
            {members.map((m) => {
              const st = statuses[m.id];
              const meta = STATUS_META[st];
              const active = detailId === m.id;
              return (
                <button
                  key={m.id}
                  data-id={`meeting-room-member-${m.id}`}
                  onClick={() => selectMember(m)}
                  className={`group flex w-full items-center gap-2.5 rounded-lg px-2.5 py-2 text-left transition-colors ${active ? 'bg-white/[0.07]' : 'hover:bg-white/[0.04]'}`}
                >
                  <span className="grid h-8 w-8 shrink-0 place-items-center rounded-lg bg-white/[0.05] text-base">{m.emoji}</span>
                  <span className="min-w-0 flex-1">
                    <span className="flex items-center gap-1.5">
                      <span className="truncate text-[13px] font-medium text-zinc-200">{m.name}</span>
                      <CircleDot className={`h-2.5 w-2.5 shrink-0 ${meta.dot}`} />
                    </span>
                    <span className="flex items-center gap-1.5 text-[11px]">
                      <span className="font-mono text-zinc-500">{m.id}</span>
                      <span className={meta.text}>· {meta.label}</span>
                    </span>
                  </span>
                </button>
              );
            })}
          </div>
        </aside>

        {/* 中：办公室时间线 */}
        <section className="relative min-w-0 flex-1 flex flex-col">
          <div ref={timelineRef} data-id="meeting-room-timeline" className="flex-1 overflow-auto px-5 py-4 space-y-3">
            {entries.map((e) => (
              <TimelineRow key={e.id} entry={e} memberById={memberById}
                onFetch={fetchWork} onPass={passReview} onQA={assignQA} />
            ))}
          </div>

          {/* 底：prompt（单聊 / 广播） */}
          <div data-id="meeting-room-prompt" className="shrink-0 border-t border-[var(--vsc-border)] bg-[#0d0d0d] px-4 py-3">
            <div className="relative">
              {/* 模式切换 */}
              <div data-id="meeting-room-mode" className="mb-2 inline-flex items-center gap-1 rounded-lg bg-white/[0.04] p-0.5 text-[12px]">
                <button data-id="meeting-room-mode-single" onClick={() => setMode('single')}
                  className={`inline-flex items-center gap-1 rounded-md px-2 py-1 ${mode === 'single' ? 'bg-white/10 text-zinc-100' : 'text-zinc-500 hover:text-zinc-300'}`}>
                  <MessageSquare className="h-3.5 w-3.5" /> 单聊
                </button>
                <button data-id="meeting-room-mode-broadcast" onClick={() => { setMode('broadcast'); setMentionOpen(false); }}
                  className={`inline-flex items-center gap-1 rounded-md px-2 py-1 ${mode === 'broadcast' ? 'bg-amber-500/20 text-amber-200' : 'text-zinc-500 hover:text-zinc-300'}`}>
                  <Megaphone className="h-3.5 w-3.5" /> 广播
                </button>
              </div>

              {mentionOpen && mode === 'single' && (
                <div data-id="meeting-room-mention" className="absolute bottom-full left-0 mb-2 w-64 overflow-hidden rounded-xl border border-white/10 bg-[#141414] shadow-2xl">
                  {members.map((m) => (
                    <button key={m.id} data-id={`meeting-room-mention-${m.id}`} onClick={() => pickMention(m)}
                      className="flex w-full items-center gap-2 px-3 py-2 text-left hover:bg-white/[0.06]">
                      <span className="text-sm">{m.emoji}</span>
                      <span className="text-[13px] text-zinc-200">{m.name}</span>
                      <span className="ml-auto font-mono text-[11px] text-zinc-500">{m.id}</span>
                    </button>
                  ))}
                </div>
              )}

              {/* 目标 chip */}
              <div data-id="meeting-room-target" className="mb-2 flex items-center gap-1.5">
                {mode === 'broadcast' ? (
                  <span className="inline-flex items-center gap-1.5 rounded-full bg-amber-500/15 px-2.5 py-1 text-[12px] text-amber-200">
                    <Megaphone className="h-3 w-3" /> 广播 · 全体（{members.length} 人）
                  </span>
                ) : target ? (
                  <span className="inline-flex items-center gap-1.5 rounded-full bg-sky-500/15 py-1 pl-2 pr-1.5 text-[12px] text-sky-300">
                    <AtSign className="h-3 w-3" />
                    <span className="font-mono">{target.id}</span>
                    <span className="text-sky-200/70">{target.name}</span>
                    <button data-id="meeting-room-target-clear" onClick={() => setTargetId(null)} className="ml-0.5 rounded-full p-0.5 hover:bg-white/10">
                      <X className="h-3 w-3" />
                    </button>
                  </span>
                ) : (
                  <span className="text-[12px] text-zinc-600">输入 @ 选成员，或左侧点一位成员</span>
                )}
              </div>

              <div className="flex items-end gap-2 rounded-xl border border-white/10 bg-[#111] px-3 py-2 focus-within:border-white/20">
                <textarea
                  ref={inputRef}
                  data-id="meeting-room-input"
                  rows={1}
                  value={text}
                  onChange={(e) => onChange(e.target.value)}
                  onKeyDown={onKeyDown}
                  placeholder={mode === 'broadcast' ? '向全体广播…（Enter 发送）' : target ? `给 ${target.name} 派任务…（Enter 发送，Shift+Enter 换行）` : '输入 @ 选择成员…'}
                  className="max-h-40 min-h-[24px] flex-1 resize-none bg-transparent text-[13px] leading-6 text-zinc-200 outline-none placeholder:text-zinc-600"
                />
                <button
                  data-id="meeting-room-send"
                  onClick={send}
                  disabled={!canSend}
                  className={`grid h-8 w-8 shrink-0 place-items-center rounded-lg text-white transition-colors disabled:cursor-not-allowed disabled:bg-white/[0.06] disabled:text-zinc-600 ${mode === 'broadcast' ? 'bg-amber-500/90 hover:bg-amber-400' : 'bg-sky-500/90 hover:bg-sky-400'}`}
                  title={mode === 'broadcast' ? '广播给全体' : '派发（cicy-agent msg）'}
                >
                  {mode === 'broadcast' ? <Megaphone className="h-4 w-4" /> : <Send className="h-4 w-4" />}
                </button>
              </div>
            </div>
          </div>

          {/* 右：成员档案（介绍 / 职责 / 协作对象） */}
          {detail && (
            <div data-id="meeting-room-member-detail" className="absolute right-0 top-0 bottom-0 w-[300px] border-l border-[var(--vsc-border)] bg-[#0e0e0e] shadow-2xl flex flex-col">
              <div className="flex items-center gap-2.5 border-b border-[var(--vsc-border)] px-4 py-3">
                <span className="grid h-9 w-9 place-items-center rounded-lg bg-white/[0.06] text-lg">{detail.emoji}</span>
                <div className="min-w-0 flex-1">
                  <div className="truncate text-[13px] font-semibold text-zinc-100">{detail.name}</div>
                  <div className="font-mono text-[11px] text-zinc-500">{detail.id} · {detail.role}</div>
                </div>
                <button data-id="meeting-room-member-detail-close" onClick={() => setDetailId(null)} className="rounded p-1 text-zinc-500 hover:text-zinc-200">
                  <X className="h-4 w-4" />
                </button>
              </div>
              <div className="flex-1 overflow-auto px-4 py-4 space-y-4">
                <div>
                  <div className="mb-1.5 text-[11px] font-medium uppercase tracking-wide text-zinc-600">介绍</div>
                  <p className="text-[13px] leading-relaxed text-zinc-300">{detail.intro}</p>
                </div>
                <div>
                  <div className="mb-1.5 text-[11px] font-medium uppercase tracking-wide text-zinc-600">职责</div>
                  <ul className="space-y-1.5">
                    {detail.duties.map((d, i) => (
                      <li key={i} className="flex items-start gap-2 text-[13px] text-zinc-300">
                        <Circle className="mt-[6px] h-1.5 w-1.5 shrink-0 fill-zinc-500 text-zinc-500" />
                        <span>{d}</span>
                      </li>
                    ))}
                  </ul>
                </div>
                <div>
                  <div className="mb-1.5 text-[11px] font-medium uppercase tracking-wide text-zinc-600">协作对象（可直接互通）</div>
                  {detail.collaborators.length === 0 ? (
                    <div className="text-[12px] text-zinc-600">无</div>
                  ) : (
                    <div className="flex flex-wrap gap-1.5">
                      {detail.collaborators.map((cid) => {
                        const c = memberById[cid];
                        if (!c) return null;
                        return (
                          <button key={cid} data-id={`meeting-room-collab-${detail.id}-${cid}`}
                            onClick={() => peerContact(detail.id, cid)}
                            title={`模拟 ${detail.name} → ${c.name} 直接通信`}
                            className="inline-flex items-center gap-1 rounded-full bg-white/[0.05] py-1 pl-1.5 pr-2 text-[12px] text-zinc-300 hover:bg-white/10">
                            <span className="text-[13px]">{c.emoji}</span>
                            <span>{c.name}</span>
                            <ArrowRight className="h-3 w-3 text-zinc-500" />
                          </button>
                        );
                      })}
                    </div>
                  )}
                </div>
                <button
                  data-id="meeting-room-member-detail-at"
                  onClick={() => { setTargetId(detail.id); setMode('single'); inputRef.current?.focus(); }}
                  className="flex w-full items-center justify-center gap-1.5 rounded-lg bg-sky-500/15 py-2 text-[13px] text-sky-300 hover:bg-sky-500/25"
                >
                  <AtSign className="h-3.5 w-3.5" /> 给 {detail.name} 派任务
                </button>
              </div>
            </div>
          )}
        </section>
      </div>
    </div>
  );
}

// ── 单条时间线 ──
function TimelineRow({
  entry, memberById, onFetch, onPass, onQA,
}: {
  entry: Entry;
  memberById: Record<string, Member>;
  onFetch: (memberId: string) => void;
  onPass: (entryId: number, memberId: string) => void;
  onQA: (entryId: number, memberId: string) => void;
}) {
  const m = memberById[entry.from];
  const to = entry.to ? memberById[entry.to] : null;

  if (entry.kind === 'note' || entry.kind === 'review') {
    return (
      <div data-id={`meeting-room-entry-${entry.id}`} className="flex justify-center">
        <div className="max-w-[80%] rounded-lg bg-white/[0.03] px-3 py-1.5 text-center text-[12px] leading-relaxed text-zinc-500 whitespace-pre-wrap">
          {entry.kind === 'review' && <CheckCircle2 className="mr-1 inline h-3.5 w-3.5 text-emerald-400" />}
          {entry.text}
          <span className="ml-2 text-zinc-700">{entry.ts}</span>
        </div>
      </div>
    );
  }

  if (entry.kind === 'broadcast') {
    return (
      <div data-id={`meeting-room-entry-${entry.id}`} className="flex items-start gap-2.5">
        <span className="grid h-7 w-7 shrink-0 place-items-center rounded-lg bg-amber-500/15 text-[12px]">📢</span>
        <div className="min-w-0 flex-1">
          <div className="mb-0.5 flex items-center gap-2 text-[12px]">
            <span className="font-medium text-zinc-300">{SELF}</span>
            <span className="rounded bg-amber-500/15 px-1.5 py-0.5 text-[11px] text-amber-200">广播 · 全体</span>
            <span className="ml-auto text-[11px] text-zinc-700">{entry.ts}</span>
          </div>
          <div className="rounded-lg rounded-tl-sm border border-amber-500/20 bg-amber-500/[0.06] px-3 py-2 text-[13px] leading-relaxed text-amber-50/90 whitespace-pre-wrap">{entry.text}</div>
        </div>
      </div>
    );
  }

  if (entry.kind === 'peer') {
    return (
      <div data-id={`meeting-room-entry-${entry.id}`} className="flex justify-center">
        <div className="inline-flex max-w-[85%] items-center gap-2 rounded-full border border-white/10 bg-white/[0.03] px-3 py-1.5 text-[12px] text-zinc-400">
          <span>{m?.emoji ?? '🤖'}</span>
          <span className="text-zinc-300">{m?.name ?? entry.from}</span>
          <ArrowRight className="h-3 w-3 text-zinc-600" />
          <span>{to?.emoji ?? '🤖'}</span>
          <span className="text-zinc-300">{to?.name ?? entry.to}</span>
          <span className="text-zinc-500">{entry.text}</span>
          <span className="text-zinc-700">{entry.ts}</span>
        </div>
      </div>
    );
  }

  if (entry.kind === 'dispatch') {
    return (
      <div data-id={`meeting-room-entry-${entry.id}`} className="flex items-start gap-2.5">
        <span className="grid h-7 w-7 shrink-0 place-items-center rounded-lg bg-sky-500/15 text-[12px]">🏢</span>
        <div className="min-w-0 flex-1">
          <div className="mb-0.5 flex items-center gap-2 text-[12px]">
            <span className="font-medium text-zinc-300">{SELF}</span>
            <span className="text-zinc-600">派发给</span>
            <span className="inline-flex items-center gap-1 rounded bg-sky-500/15 px-1.5 py-0.5 font-mono text-[11px] text-sky-300">
              <AtSign className="h-3 w-3" />{entry.to}
            </span>
            {to && <span className="text-zinc-600">{to.name}</span>}
            <span className="ml-auto text-[11px] text-zinc-700">{entry.ts}</span>
          </div>
          <div className="rounded-lg rounded-tl-sm bg-white/[0.04] px-3 py-2 text-[13px] leading-relaxed text-zinc-200 whitespace-pre-wrap">{entry.text}</div>
          <div className="mt-1 flex items-center gap-1 text-[11px] text-zinc-600">
            <Loader2 className="h-3 w-3" /> 经 cicy-agent msg 送达 · 工作过程不进办公室
          </div>
        </div>
      </div>
    );
  }

  // done
  return (
    <div data-id={`meeting-room-entry-${entry.id}`} className="flex items-start gap-2.5">
      <span className="grid h-7 w-7 shrink-0 place-items-center rounded-lg bg-white/[0.05] text-[13px]">{m?.emoji ?? '🤖'}</span>
      <div className="min-w-0 flex-1">
        <div className="mb-0.5 flex items-center gap-2 text-[12px]">
          <span className="font-medium text-zinc-300">{m?.name ?? entry.from}</span>
          <span className="font-mono text-[11px] text-zinc-600">{entry.from}</span>
          <span className="ml-auto text-[11px] text-zinc-700">{entry.ts}</span>
        </div>
        <div className="inline-flex items-center gap-1.5 rounded-lg rounded-tl-sm bg-emerald-500/10 px-3 py-2 text-[13px] font-medium text-emerald-300">
          <CheckCircle2 className="h-4 w-4" /> {entry.text}
        </div>

        {entry.review === 'pending' && (
          <div data-id={`meeting-room-review-${entry.id}`} className="mt-2 flex flex-wrap items-center gap-2">
            <span className="inline-flex items-center gap-1 text-[11px] text-amber-300/80"><Clock className="h-3 w-3" /> 待 {SELF} 处理</span>
            <button data-id={`meeting-room-review-fetch-${entry.id}`} onClick={() => onFetch(entry.from)}
              className="inline-flex items-center gap-1 rounded-md border border-white/10 px-2 py-1 text-[12px] text-zinc-300 hover:bg-white/[0.06]">
              <Eye className="h-3.5 w-3.5" /> 查看工作内容
            </button>
            <button data-id={`meeting-room-review-pass-${entry.id}`} onClick={() => onPass(entry.id, entry.from)}
              className="inline-flex items-center gap-1 rounded-md bg-emerald-500/15 px-2 py-1 text-[12px] text-emerald-300 hover:bg-emerald-500/25">
              <ClipboardCheck className="h-3.5 w-3.5" /> 验收通过
            </button>
            <button data-id={`meeting-room-review-qa-${entry.id}`} onClick={() => onQA(entry.id, entry.from)}
              className="inline-flex items-center gap-1 rounded-md border border-white/10 px-2 py-1 text-[12px] text-zinc-300 hover:bg-white/[0.06]">
              <ShieldCheck className="h-3.5 w-3.5" /> 指派 QA 验收
            </button>
          </div>
        )}
        {entry.review === 'passed' && (
          <div className="mt-1.5 inline-flex items-center gap-1 text-[11px] text-emerald-400/80"><CheckCircle2 className="h-3 w-3" /> 已验收通过</div>
        )}
        {entry.review === 'qa' && (
          <div className="mt-1.5 inline-flex items-center gap-1 text-[11px] text-sky-300/80"><ShieldCheck className="h-3 w-3" /> 已指派 QA 验收</div>
        )}
      </div>
    </div>
  );
}
