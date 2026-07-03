import { useCallback, useEffect, useRef, useState } from 'react';
import { ArrowUp, Loader2, Square, Paperclip, X, FileText, FileSpreadsheet, FileCode, FileArchive, Brain } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import CurrentHistoryView from './CurrentHistoryView';
import { isCicyLiteAgent } from '../../lib/agentType';
import apiService from '../../services/api';

/*
 * DispatcherChat — dispatcher(PM) agent 的专属卡片主体(data-id="dispatcher-chat")。
 * 上 = CurrentHistoryView(网关审计驱动的对话历史,reply.json 轮询 = 流式尾巴),
 * 下 = prompt 输入条,发送走 /api/tmux/send(送进 REPL stdin,与终端/TG 同一管道)。
 * 终端不再展示——dispatcher 在 web 上就是一个聊天窗口。
 *
 * 附件(图片/PDF/文档):回形针按钮 / 粘贴 / 拖拽 → 上传到 /assets/files(per-agent 工作区,
 * 带上传进度)→ 图片显缩略图预览、文档显小方块。发送时把上传得到的 FileRef 路径拼进消息
 * 末尾的「[附件]」区(方案 A,后端零改动:agent 用自己的文件工具 Read 这些路径)。
 */

type Attachment = {
  id: string;
  name: string;
  size: number;
  isImage: boolean;
  previewURL?: string; // 本地 objectURL(图片缩略图,移除时 revoke)
  status: 'uploading' | 'done' | 'error';
  progress: number; // 0..100
  fileRef?: string; // 后端 FileRef(主机路径)——拼进消息
  url?: string; // 可访问 URL
};

let attachSeq = 0;
const nextAttachId = () => `att-${++attachSeq}-${Date.now()}`;

function fmtSize(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(0)} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}

// 文件类型 → 彩色徽章 + 类型标签 + 图标(对齐 Claude web 的附件卡:PDF 红、表格绿、代码黄…)。
function fileTypeMeta(name: string): { label: string; badge: string; kind: 'doc' | 'sheet' | 'code' | 'zip' } {
  const ext = (name.split('.').pop() || '').toLowerCase();
  if (ext === 'pdf') return { label: 'PDF', badge: 'bg-rose-500/15 text-rose-300', kind: 'doc' };
  if (['doc', 'docx', 'rtf'].includes(ext)) return { label: ext.toUpperCase(), badge: 'bg-sky-500/15 text-sky-300', kind: 'doc' };
  if (['xls', 'xlsx', 'csv', 'tsv'].includes(ext)) return { label: ext.toUpperCase(), badge: 'bg-emerald-500/15 text-emerald-300', kind: 'sheet' };
  if (['json', 'js', 'ts', 'tsx', 'py', 'go', 'sh', 'md', 'log', 'yaml', 'yml'].includes(ext)) return { label: ext.toUpperCase(), badge: 'bg-amber-500/15 text-amber-300', kind: 'code' };
  if (['zip', 'tar', 'gz', '7z', 'rar'].includes(ext)) return { label: ext.toUpperCase(), badge: 'bg-zinc-500/20 text-zinc-300', kind: 'zip' };
  return { label: (ext || 'FILE').toUpperCase(), badge: 'bg-zinc-500/20 text-zinc-300', kind: 'doc' };
}

// hover 出现的删除 ×(Claude 风格:右上角小圆点)。
function RemoveBtn({ id, onRemove }: { id: string; onRemove: () => void }) {
  const { t } = useTranslation('chat');
  return (
    <button
      type="button"
      data-id={`attachment-remove-${id}`}
      onClick={onRemove}
      className="absolute -right-1.5 -top-1.5 inline-flex h-5 w-5 items-center justify-center rounded-full bg-zinc-900 text-zinc-300 opacity-0 shadow ring-1 ring-white/15 transition-opacity hover:bg-zinc-700 group-hover:opacity-100"
      aria-label={t('attachRemove')}
    >
      <X className="h-3 w-3" />
    </button>
  );
}

// 单个附件卡(对齐 Claude web):图片→圆角缩略图;文件→圆角卡(彩色类型徽章+文件名+类型·大小)。
// 上传中徽章/缩略图位转圈 + 显进度,失败显红,hover 出 ×。
function AttachmentChip({ att, onRemove }: { att: Attachment; onRemove: () => void }) {
  const { t } = useTranslation('chat');
  const uploading = att.status === 'uploading';
  const error = att.status === 'error';

  if (att.isImage) {
    // 外层不裁(否则伸到边角外的删除 × 被 overflow-hidden 吞掉);只让内层裁圆角缩略图。
    return (
      <div data-id={`attachment-image-${att.id}`} className="group relative h-16 w-16 shrink-0" title={att.name}>
        <div className="h-full w-full overflow-hidden rounded-xl border border-white/10 bg-black/30">
          {att.previewURL ? <img src={att.previewURL} alt={att.name} className="h-full w-full object-cover" /> : null}
          {uploading ? (
            <div className="absolute inset-0 flex flex-col items-center justify-center gap-0.5 bg-black/45 text-[10px] text-white">
              <Loader2 className="h-4 w-4 animate-spin" />
              <span>{att.progress}%</span>
            </div>
          ) : null}
          {error ? <div className="absolute inset-0 flex items-center justify-center bg-rose-900/55 text-[10px] text-rose-100">{t('attachFailed')}</div> : null}
        </div>
        <RemoveBtn id={att.id} onRemove={onRemove} />
      </div>
    );
  }

  const meta = fileTypeMeta(att.name);
  const Icon = meta.kind === 'sheet' ? FileSpreadsheet : meta.kind === 'code' ? FileCode : meta.kind === 'zip' ? FileArchive : FileText;
  return (
    <div data-id={`attachment-file-${att.id}`} className="group relative flex w-56 shrink-0 items-center gap-2.5 rounded-xl border border-white/10 bg-white/[0.04] p-2 pr-3" title={att.name}>
      <div className={`flex h-10 w-10 shrink-0 items-center justify-center rounded-lg ${meta.badge}`}>
        {uploading ? <Loader2 className="h-4 w-4 animate-spin" /> : <Icon className="h-5 w-5" />}
      </div>
      <div className="min-w-0 flex-1">
        <div data-id={`attachment-name-${att.id}`} className="truncate text-[13px] font-medium text-zinc-100">{att.name}</div>
        <div className="truncate text-[11px] text-zinc-500">
          {error ? <span className="text-rose-300">{t('attachUploadFailed')}</span> : uploading ? t('attachUploading', { progress: att.progress }) : `${meta.label} · ${fmtSize(att.size)}`}
        </div>
      </div>
      <RemoveBtn id={att.id} onRemove={onRemove} />
    </div>
  );
}

// 斜杠命令清单:输入框首字符为 `/` 时弹出。服务端拦截这些命令(slash-ack),不入对话历史。
const SLASH_COMMANDS: { cmd: string; label: string; desc: string }[] = [
  { cmd: '/clear', label: '清空对话', desc: '清空当前对话上下文,开始新对话' },
  { cmd: '/compact', label: '压缩对话', desc: '压缩历史以释放上下文,保留摘要' },
];

export default function DispatcherChat({ paneId, active, agentType = 'cicy', title = '' }: { paneId: string; active: boolean; agentType?: string; title?: string }) {
  const { t } = useTranslation('chat');
  // placeholder 跟当前 agent 的 title 走(产品经理→「问问你的产品经理」),不再写死「项目经理」。
  // title 为空时回退到通用文案。
  const roleName = String(title || '').trim();
  const idlePlaceholder = roleName ? t('composerPlaceholder', { role: roleName }) : t('composerPlaceholderNoRole');
  const [text, setText] = useState('');
  const [slashSel, setSlashSel] = useState(0);
  const [sending, setSending] = useState(false);
  // 回复进行中(busy)→ 锁发送、显示 waiting。只有 reply complete / fail 才解锁。
  // 信号来自 CurrentHistoryView 的轮询(cicy:dispatcher-busy)。
  const [busy, setBusy] = useState(false);
  // 发送队列(学 Claude Code 的 queuedCommands,队列归前端所有):busy 期间的输入
  // 不发后端,先堆在 prompt 上方(逐条可删),空闲时按顺序自动放行 —— 队首连续的
  // 普通消息合并成一条发出;斜杠命令(/clear /compact)独占一轮,绝不在 busy 时执行。
  // 停止生成只取消当前回复,队列保留(不再静默丢弃)。
  const [queue, setQueue] = useState<{ id: number; text: string }[]>([]);
  const queueSeqRef = useRef(1);
  const [attachments, setAttachments] = useState<Attachment[]>([]);
  const [dragOver, setDragOver] = useState(false);
  const composingRef = useRef(false);
  const inputRef = useRef<HTMLTextAreaElement | null>(null);
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const attachmentsRef = useRef<Attachment[]>([]);
  attachmentsRef.current = attachments;

  // 思考(extended thinking)开关 —— 像 Gemini 那样在输入框左下角。写入该 agent 的
  // agent_config.config {"thinking":"enabled|disabled"},优先于全局 gateway_thinking。
  // 网关 agentInspectorApplyThinking 实时读取,免重启。仅对 cicy(lite)agent 显示。
  const showThinking = isCicyLiteAgent(agentType);
  const [thinkingOn, setThinkingOn] = useState(false);
  const [thinkingSaving, setThinkingSaving] = useState(false);
  const configRef = useRef<Record<string, any>>({});

  useEffect(() => {
    if (!showThinking) return;
    let alive = true;
    apiService.getPane(paneId).then(({ data }) => {
      if (!alive) return;
      let cfg: Record<string, any> = {};
      try { cfg = data?.config ? JSON.parse(data.config) : {}; } catch { cfg = {}; }
      configRef.current = cfg && typeof cfg === 'object' ? cfg : {};
      setThinkingOn(String(configRef.current.thinking || '').toLowerCase() === 'enabled');
    }).catch(() => {});
    return () => { alive = false; };
  }, [paneId, showThinking]);

  const toggleThinking = useCallback(async () => {
    const next = thinkingOn ? 'disabled' : 'enabled';
    setThinkingSaving(true);
    const merged = { ...configRef.current, thinking: next };
    try {
      await apiService.updatePane(paneId, { config: JSON.stringify(merged) });
      configRef.current = merged;
      setThinkingOn(next === 'enabled');
    } catch {
      window.dispatchEvent(new CustomEvent('show-toast', { detail: t('composerThinkingSaveFailed') }));
    } finally {
      setThinkingSaving(false);
    }
  }, [paneId, thinkingOn]);

  const uploading = attachments.some((a) => a.status === 'uploading');

  useEffect(() => {
    const onBusy = (e: Event) => {
      const detail = (e as CustomEvent)?.detail || {};
      const id = String(detail.paneId || '').trim();
      if (id && id !== paneId) return;
      // The history poll's busy signal may only ENTER the in-flight state, never
      // leave it: replyInFlight flickers to false on transient gaps (tool-round
      // boundary / current.json reseed, session rotation, slow turn registration),
      // and honoring that false mid-generation would flip the stop button back to
      // "send" and make Esc a no-op — so a click sends a new message instead of
      // stopping. Clearing busy is owned solely by the hysteresis poll below
      // (positive terminal read only), so the two pollers can't race it off.
      if (detail.busy) setBusy(true);
    };
    window.addEventListener('cicy:dispatcher-busy', onBusy as EventListener);
    return () => window.removeEventListener('cicy:dispatcher-busy', onBusy as EventListener);
  }, [paneId]);

  // "Send to agent" for a cicy-lite agent (no terminal) lands here: append the
  // file path into the composer input so the operator can send it like any prompt.
  useEffect(() => {
    const onFill = (e: Event) => {
      const detail = (e as CustomEvent)?.detail || {};
      // Match on the short pane id so a "w-x:main.0" target still reaches the
      // "w-x" composer (the sender's pane-id format varies by call site).
      const id = String(detail.paneId || '').split(':')[0];
      if (id && id !== paneId.split(':')[0]) return;
      const insert = String(detail.text || '').trim();
      if (!insert) return;
      setText((prev) => (prev ? `${prev} ${insert}` : insert));
      inputRef.current?.focus();
    };
    window.addEventListener('cicy:fill-composer', onFill as EventListener);
    return () => window.removeEventListener('cicy:fill-composer', onFill as EventListener);
  }, [paneId]);

  // 切换 PM 时清空忙态 + 附件,避免把上一个会话的状态带过来。
  useEffect(() => {
    setBusy(false);
    setAttachments((prev) => { prev.forEach((a) => a.previewURL && URL.revokeObjectURL(a.previewURL)); return []; });
  }, [paneId]);

  // 卸载时回收所有图片 objectURL。
  useEffect(() => () => { attachmentsRef.current.forEach((a) => a.previewURL && URL.revokeObjectURL(a.previewURL)); }, []);

  // busy 的**唯一解锁权**在这里(单一清除源 + 滞回),不让 CurrentHistoryView 的 poll
  // 和这里互相抢着清 busy。规则:
  //   - 只在「确证终态」(complete / failed)时解锁——agent 这一轮确实结束了。
  //   - 绝不因「暂时看不到在跑的回合」(answerId===0,出现在工具轮边界 reseed、会话轮换、
  //     turn 还没登记的瞬间)而解锁:那种闪断会把停止键变回发送键、让 Esc 失效,正是这次
  //     要修的 bug。claude 把「打断」绑在一个权威的生成状态上,这里是等价物——没确证结束前
  //     一直保持「可打断」。
  //   - /clear 建空会话这种死锁:busy 是乐观置上的、但根本没有 turn 登记。只有在「从没见过
  //     在途回合」时,给更长宽限后才兜底解锁。
  useEffect(() => {
    if (!busy) return;
    let cancelled = false;
    let timer: number | null = null;
    const since = Date.now();
    let sawInFlight = false; // 是否曾确证看到这一轮在生成
    const tick = async () => {
      if (cancelled) return;
      try {
        const { data } = await apiService.getAgentCurrentReply(paneId);
        if (cancelled) return;
        const answerId = Number((data as any)?.history_id || 0);
        const complete = !!(data as any)?.complete;
        const status = String((data as any)?.status || '').trim().toLowerCase();
        const failed = status === 'failed' || status === 'fail' || status === 'error';
        if (answerId > 0 && !complete && !failed) sawInFlight = true;
        // 确证终态 → 解锁(给 800ms 起步宽限,避免刚发出去、上一轮残留的 complete 误判)。
        if (answerId > 0 && (complete || failed) && Date.now() - since > 800) { setBusy(false); return; }
        // 死锁兜底:从没见过在途回合 + 一直没有 turn,长宽限后解锁(/clear 空会话)。
        if (!sawInFlight && answerId === 0 && Date.now() - since > 5000) { setBusy(false); return; }
      } catch {}
      timer = window.setTimeout(tick, 1000);
    };
    timer = window.setTimeout(tick, 1000);
    return () => { cancelled = true; if (timer != null) window.clearTimeout(timer); };
  }, [busy, paneId]);

  const updateAtt = useCallback((id: string, patch: Partial<Attachment>) => {
    setAttachments((prev) => prev.map((a) => (a.id === id ? { ...a, ...patch } : a)));
  }, []);

  const startUpload = useCallback((id: string, file: File) => {
    apiService
      .uploadAssetFile(paneId, file, (loaded, total) => {
        updateAtt(id, { progress: Math.max(1, Math.round((loaded / total) * 100)) });
      })
      .then((resp: any) => {
        const f = resp?.data?.file || {};
        // 后端 JSON 是 snake_case:file_ref / url。fileRef 是文件的绝对路径引用(file://…),
        // 发消息时取它给 LLM,LLM 才能 Read 这个文件。
        updateAtt(id, { status: 'done', progress: 100, fileRef: String(f.file_ref || f.fileRef || ''), url: String(f.url || f.URL || '') });
      })
      .catch(() => {
        updateAtt(id, { status: 'error' });
        window.dispatchEvent(new CustomEvent('show-toast', { detail: t('composerUploadFailedNamed', { name: file.name }) }));
      });
  }, [paneId, updateAtt]);

  const addFiles = useCallback((files: FileList | File[]) => {
    const list = Array.from(files);
    for (const file of list) {
      const id = nextAttachId();
      const isImage = file.type.startsWith('image/');
      const previewURL = isImage ? URL.createObjectURL(file) : undefined;
      setAttachments((prev) => [...prev, { id, name: file.name, size: file.size, isImage, previewURL, status: 'uploading', progress: 0 }]);
      startUpload(id, file);
    }
  }, [startUpload]);

  const removeAttachment = useCallback((id: string) => {
    setAttachments((prev) => {
      const a = prev.find((x) => x.id === id);
      if (a?.previewURL) URL.revokeObjectURL(a.previewURL);
      return prev.filter((x) => x.id !== id);
    });
  }, []);

  const send = useCallback(async (override?: string) => {
    const value = (override ?? text).trim();
    // 斜杠命令(override 直发)是纯指令,不挂附件。
    const done = override ? [] : attachmentsRef.current.filter((a) => a.status === 'done');
    // 无内容(既无文本也无已传附件)、发送中、或还有附件在上传 → 不发。
    if ((!value && done.length === 0) || sending || uploading) return;
    // 已上传附件用**标准 markdown** 拼进消息,URL 用文件的**绝对路径**(从 FileRef 取):
    // 这样 LLM/agent 拿到真实路径、能用文件工具 Read、真正"用"这个文档;UI 渲染时再据路径
    // 解析成预览/下载地址(见 CurrentHistoryView 的 img/a 组件)。绝对路径不含任何 token,
    // 出站审计也不会拦。图片 ![name](abs);文件 [name](abs)。
    let body = value;
    if (done.length) {
      const md = done
        .map((a) => {
          const abs = a.fileRef ? '/' + a.fileRef.replace(/^file:\/\//, '').replace(/^\/+/, '') : (a.url || '');
          return a.isImage ? `![${a.name}](${abs})` : `[${a.name}](${abs})`;
        })
        .join('\n\n');
      body = (value ? value + '\n\n' : '') + md;
    }
    // busy(正在输出/thinking)→ 消息入队,不打后端。队列渲染在 prompt 上方,
    // 空闲后由 flush effect 自动放行。斜杠命令同样排队(必须等不 busy 才执行)。
    if (busy) {
      const id = queueSeqRef.current;
      queueSeqRef.current += 1;
      setQueue((prev) => [...prev, { id, text: body }]);
      setText('');
      setAttachments((prev) => { prev.forEach((a) => a.previewURL && URL.revokeObjectURL(a.previewURL)); return []; });
      inputRef.current?.focus();
      return;
    }
    setSending(true);
    setBusy(true); // 立刻锁住,不等轮询事件回传
    setText('');
    setAttachments((prev) => { prev.forEach((a) => a.previewURL && URL.revokeObjectURL(a.previewURL)); return []; });
    // Paint the q bubble + reserve the a slot THIS frame — BEFORE the POST round-trips.
    window.dispatchEvent(new CustomEvent('cicy:current-history-refresh', { detail: { paneId, text: body } }));
    try {
      await apiService.sendCommand(paneId, body, true);
      window.dispatchEvent(new CustomEvent('cicy:current-history-refresh', { detail: { paneId } }));
    } catch {
      setText(value);
      setBusy(false);
      window.dispatchEvent(new CustomEvent('cicy:current-history-cancel-optimistic', { detail: { paneId } }));
      window.dispatchEvent(new CustomEvent('show-toast', { detail: t('composerSendFailed') }));
    } finally {
      setSending(false);
      inputRef.current?.focus();
    }
  }, [paneId, text, sending, uploading, busy]);

  // 空闲放行:busy→idle 且队列非空时,自动发出队首批次 —— 队首是斜杠命令就单独发
  // (独占一轮);否则把队首连续的普通消息合并成一条。剩余项留队列,等下一次空闲。
  useEffect(() => {
    if (busy || sending || uploading || queue.length === 0) return;
    const isSlash = (s: string) => /^\/\w+(\s|$)/.test(s.trim());
    let batch: { id: number; text: string }[];
    if (isSlash(queue[0].text)) {
      batch = [queue[0]];
    } else {
      batch = [];
      for (const it of queue) { if (isSlash(it.text)) break; batch.push(it); }
    }
    const ids = new Set(batch.map((b) => b.id));
    setQueue((prev) => prev.filter((it) => !ids.has(it.id)));
    void send(batch.map((b) => b.text).join('\n'));
  }, [busy, sending, uploading, queue, send]);

  // 切换 agent → 队列不跨 pane。
  useEffect(() => { setQueue([]); }, [paneId]);

  // 取消生成,按 agent 形态分流:
  // - cicy 是 headless(无 tmux pane)→ /api/cicy/cancel,服务端取消正在跑的网关请求。
  // - 终端类 agent(claude/codex)→ 往 pane 送 Escape。
  const cancel = useCallback(async () => {
    if (!busy) return;
    try {
      if (agentType === 'cicy') await apiService.cancelCicyReply(paneId);
      else await apiService.sendKeys(paneId, 'Escape');
      setBusy(false); // reflect the stop at once; if the turn is still tearing down
                      // the history poll re-sets busy=true until it truly ends.
      window.dispatchEvent(new CustomEvent('show-toast', { detail: t('composerCanceled') }));
    } catch {
      window.dispatchEvent(new CustomEvent('show-toast', { detail: t('composerCancelFailed') }));
    }
  }, [paneId, busy, agentType]);

  const hasContent = !!text.trim() || attachments.some((a) => a.status === 'done');
  const canSend = hasContent && !sending && !uploading;

  // 斜杠命令菜单:首字符为 `/` 且还没敲空格(仍在拼命令 token)时,按前缀过滤候选。
  const slashToken = text.startsWith('/') && !/\s/.test(text) ? text.toLowerCase() : '';
  const slashMatches = slashToken ? SLASH_COMMANDS.filter((c) => c.cmd.startsWith(slashToken)) : [];
  const slashOpen = slashMatches.length > 0 && !sending;
  const slashSelClamped = Math.min(slashSel, Math.max(0, slashMatches.length - 1));

  return (
    <div data-id="dispatcher-chat" className="flex h-full w-full flex-col bg-[#0c0d10]">
      <div data-id="dispatcher-chat-history" className="min-h-0 flex-1 overflow-hidden">
        <CurrentHistoryView key={paneId} paneId={paneId} open={active} agentType={agentType} fullWidth leftAlignQuestions />
      </div>
      <div
        data-id="dispatcher-chat-input-bar"
        className={`relative shrink-0 border-t bg-black/[0.25] px-4 py-2.5 transition-colors ${dragOver ? 'border-blue-500/50 bg-blue-500/[0.06]' : 'border-white/[0.06]'}`}
        onDragEnter={(e) => { e.preventDefault(); setDragOver(true); }}
        onDragOver={(e) => { e.preventDefault(); setDragOver(true); }}
        onDragLeave={(e) => { if (e.currentTarget === e.target) setDragOver(false); }}
        onDrop={(e) => { e.preventDefault(); setDragOver(false); if (e.dataTransfer?.files?.length) addFiles(e.dataTransfer.files); }}
      >
        {dragOver ? (
          <div data-id="dispatcher-chat-drop-hint" className="pointer-events-none absolute inset-1 z-10 flex items-center justify-center rounded-xl border-2 border-dashed border-blue-500/50 bg-[#0c0d10]/80 text-sm text-blue-200">
            {t('composerDropHint')}
          </div>
        ) : null}
        {/* 发送队列(Claude Code 风格):busy 期间发出的消息堆在 prompt 上方,
            逐条可移除;当前回复完成后按顺序自动放行。 */}
        {queue.length > 0 ? (
          <div data-id="dispatcher-chat-queue" className="mb-2 flex w-full flex-col gap-1">
            <div data-id="dispatcher-chat-queue-header" className="flex items-center gap-1.5 px-1 text-[11px] text-zinc-500">
              <Loader2 className="h-3 w-3 animate-spin text-zinc-600" />
              {t('composerQueuedHeader', { n: queue.length, defaultValue: '{{n}} 条排队中 · 当前回复完成后自动发送' })}
            </div>
            {queue.map((it) => {
              const isSlashItem = /^\/\w+(\s|$)/.test(it.text.trim());
              return (
                <div
                  key={it.id}
                  data-id="dispatcher-chat-queue-item"
                  className="group flex items-start gap-2 rounded-xl border border-white/[0.07] bg-white/[0.03] px-3 py-1.5"
                >
                  <span
                    data-id="dispatcher-chat-queue-item-text"
                    className={`min-w-0 flex-1 whitespace-pre-wrap break-words text-[13px] leading-5 [display:-webkit-box] [-webkit-box-orient:vertical] [-webkit-line-clamp:2] overflow-hidden ${isSlashItem ? 'font-mono text-amber-200/80' : 'text-zinc-400'}`}
                  >
                    {it.text}
                  </span>
                  <button
                    type="button"
                    data-id="dispatcher-chat-queue-item-remove"
                    onClick={() => setQueue((prev) => prev.filter((q) => q.id !== it.id))}
                    title={t('composerQueueRemove', { defaultValue: '移出队列' })}
                    className="mt-0.5 shrink-0 rounded-md p-0.5 text-zinc-600 opacity-0 transition-opacity hover:bg-white/[0.06] hover:text-zinc-300 group-hover:opacity-100"
                  >
                    <X className="h-3.5 w-3.5" />
                  </button>
                </div>
              );
            })}
          </div>
        ) : null}
        {attachments.length > 0 ? (
          <div data-id="dispatcher-chat-attachments" className="mb-2 flex w-full flex-wrap gap-2">
            {attachments.map((a) => (
              <AttachmentChip key={a.id} att={a} onRemove={() => removeAttachment(a.id)} />
            ))}
          </div>
        ) : null}
        {/* 斜杠命令菜单:首字符 `/` 即浮在输入卡上方(Claude/Slack 风格)。↑↓ 选,
            Enter/点击执行,Tab 补全,Esc 关闭。 */}
        {slashOpen ? (
          <div data-id="dispatcher-chat-slash-menu" className="absolute bottom-full left-4 right-4 z-20 mb-2 overflow-hidden rounded-2xl border border-white/[0.10] bg-[#16171b] shadow-xl shadow-black/40">
            <div className="px-3 py-1.5 text-[11px] font-medium uppercase tracking-wide text-zinc-500">{t('slashMenuTitle')}</div>
            {slashMatches.map((c, i) => {
              const selected = i === slashSelClamped;
              return (
                <button
                  key={c.cmd}
                  type="button"
                  data-id={`dispatcher-chat-slash-item-${c.cmd.slice(1)}`}
                  onMouseEnter={() => setSlashSel(i)}
                  onMouseDown={(e) => { e.preventDefault(); void send(c.cmd); setSlashSel(0); }}
                  className={`flex w-full items-center gap-3 px-3 py-2 text-left transition-colors ${selected ? 'bg-blue-500/15' : 'hover:bg-white/[0.04]'}`}
                >
                  <span className="shrink-0 font-mono text-[13px] text-blue-300">{c.cmd}</span>
                  <span className="truncate text-[12px] text-zinc-500">{c.desc}</span>
                </button>
              );
            })}
          </div>
        ) : null}
        {/* Gemini-style prompt card: big rounded container, textarea on top,
            a bottom toolbar (left: attach + thinking; right: round send).
            Full width (matches the full-width history). */}
        <div data-id="dispatcher-chat-input-inner" className={`flex w-full flex-col gap-2.5 rounded-[26px] border bg-white/[0.04] px-3 pb-2.5 pt-2.5 shadow-lg shadow-black/20 transition-colors ${busy ? 'border-white/[0.06] opacity-80' : 'border-white/[0.10] focus-within:border-blue-500/40'}`}>
          <input
            ref={fileInputRef}
            data-id="dispatcher-chat-file-input"
            type="file"
            multiple
            accept="image/*,.pdf,.doc,.docx,.txt,.md,.csv,.tsv,.xlsx,.json,.zip,.log"
            className="hidden"
            onChange={(e) => { if (e.target.files) addFiles(e.target.files); e.target.value = ''; }}
          />
          <textarea
            ref={inputRef}
            data-id="dispatcher-chat-input"
            value={text}
            rows={Math.min(8, Math.max(1, text.split('\n').length))}
            placeholder={busy ? t('composerBusyPlaceholder') : idlePlaceholder}
            onChange={(e) => { setText(e.target.value); setSlashSel(0); }}
            onCompositionStart={() => { composingRef.current = true; }}
            onCompositionEnd={() => { composingRef.current = false; }}
            onPaste={(e) => {
              const files = Array.from(e.clipboardData?.files || []);
              if (files.length) { e.preventDefault(); addFiles(files); }
            }}
            onKeyDown={(e) => {
              e.stopPropagation();
              // 斜杠菜单打开时,方向键/Enter/Tab/Esc 先归菜单(优先于发送/取消)。
              if (slashOpen && !composingRef.current) {
                if (e.key === 'ArrowDown') { e.preventDefault(); setSlashSel((s) => Math.min(s + 1, slashMatches.length - 1)); return; }
                if (e.key === 'ArrowUp') { e.preventDefault(); setSlashSel((s) => Math.max(s - 1, 0)); return; }
                if (e.key === 'Tab') { e.preventDefault(); setText(slashMatches[slashSelClamped].cmd + ' '); setSlashSel(0); return; }
                if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); void send(slashMatches[slashSelClamped].cmd); setSlashSel(0); return; }
                if (e.key === 'Escape') { e.preventDefault(); setText(''); setSlashSel(0); return; }
              }
              if (e.key === 'Escape' && busy && !composingRef.current) {
                e.preventDefault();
                void cancel();
                return;
              }
              if (e.key === 'Enter' && !e.shiftKey && !composingRef.current) {
                e.preventDefault();
                void send();
              }
            }}
            className="max-h-44 w-full resize-none bg-transparent px-2 pt-1.5 text-[15px] leading-6 text-zinc-100 outline-none placeholder:text-zinc-600"
          />
          {/* bottom toolbar */}
          <div data-id="dispatcher-chat-toolbar" className="flex items-center justify-between gap-2">
            <div data-id="dispatcher-chat-toolbar-left" className="flex items-center gap-1.5">
              <button
                type="button"
                data-id="dispatcher-chat-attach"
                onClick={() => fileInputRef.current?.click()}
                className="inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-full border border-white/[0.10] text-zinc-300 transition-colors hover:bg-white/[0.08] hover:text-zinc-100"
                title={t('composerAttach')}
                aria-label="Attach"
              >
                <Paperclip className="h-[15px] w-[15px]" />
              </button>
              {showThinking ? (
                <button
                  type="button"
                  data-id="dispatcher-chat-thinking-toggle"
                  onClick={toggleThinking}
                  disabled={thinkingSaving}
                  aria-pressed={thinkingOn}
                  title={thinkingOn ? t('composerThinkingOn') : t('composerThinkingOff')}
                  className={`inline-flex h-7 shrink-0 items-center gap-1 rounded-full border px-2.5 text-[12px] font-medium transition-colors disabled:opacity-50 ${
                    thinkingOn
                      ? 'border-blue-500/40 bg-blue-500/15 text-blue-300 hover:bg-blue-500/25'
                      : 'border-white/[0.10] text-zinc-400 hover:bg-white/[0.06] hover:text-zinc-200'
                  }`}
                >
                  {thinkingSaving ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Brain className="h-3.5 w-3.5" />}
                  {t('composerThinking')}
                </button>
              ) : null}
            </div>
            {/* 生成中 = 停止按钮(点它或按 Esc 都能取消);否则 = 圆形发送。 */}
            <button
              data-id={busy ? 'dispatcher-chat-stop' : 'dispatcher-chat-send'}
              type="button"
              onClick={() => (busy ? void cancel() : void send())}
              disabled={busy ? sending : !canSend}
              className={`inline-flex h-9 w-9 shrink-0 items-center justify-center rounded-full transition-colors ${
                busy
                  ? 'bg-white/[0.12] text-zinc-100 hover:bg-white/[0.18]'
                  : canSend
                    ? 'bg-blue-600 text-white hover:bg-blue-500'
                    : 'bg-white/[0.06] text-zinc-600'
              }`}
              title={busy ? t('composerStop') : uploading ? t('composerUploading') : t('composerSend')}
              aria-label={busy ? 'Stop (Esc)' : 'Send'}
            >
              {sending ? <Loader2 className="h-4 w-4 animate-spin" /> : busy ? <Square className="h-3.5 w-3.5 fill-current" /> : <ArrowUp className="h-[18px] w-[18px]" />}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
