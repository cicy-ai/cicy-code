import { useEffect, useRef, useState } from 'react';
import { AlertCircle, LoaderCircle, Send, Square, Zap } from 'lucide-react';
import apiService from '../services/api';
import {
  createOpenClawId,
  OpenClawGatewayClient,
  type OpenClawGatewayEvent,
  type OpenClawGatewayInfo,
} from '../services/openclawGateway';

interface Props {
  hasToken: boolean;
  onOpenWorkspace: () => void;
}

type HomeMessageRole = 'user' | 'assistant' | 'system' | 'tool';

interface HomeMessage {
  id: string;
  role: HomeMessageRole;
  sender: string;
  content: string;
  timestamp: number;
  isMe?: boolean;
}

interface TeamMember {
  id: string;
  label: string;
  status: 'online' | 'busy' | 'offline';
  connected: number;
  total: number;
  configured: number;
  enabled: number;
}

interface OpenClawHelloPayload {
  snapshot?: {
    sessionDefaults?: {
      mainSessionKey?: string;
    };
  };
}

interface OpenClawSessionSummary {
  key?: string;
  updatedAt?: number;
  kind?: string;
  channel?: string;
  lastChannel?: string;
  origin?: {
    provider?: string;
  };
}

interface OpenClawSessionsListPayload {
  sessions?: OpenClawSessionSummary[];
}

interface ResolvedGatewayInfo extends OpenClawGatewayInfo {
  main_session_key: string;
  session_prefix: string;
  preferred_session_keys: Record<string, string>;
}

function isPlainObject(value: unknown): value is Record<string, any> {
  return !!value && typeof value === 'object' && !Array.isArray(value);
}

function errorToString(error: unknown): string {
  if (error instanceof Error) {
    return error.message;
  }
  if (typeof error === 'string') {
    return error;
  }
  return '未知错误';
}

function formatTime(timestamp: number): string {
  return new Date(timestamp).toLocaleTimeString([], {
    hour: '2-digit',
    minute: '2-digit',
  });
}

function isNoReply(text: string): boolean {
  return /^\s*NO_REPLY\s*$/i.test(text.trim());
}

function extractMessageText(message: any): string {
  if (typeof message === 'string') {
    return message;
  }
  if (!message || typeof message !== 'object') {
    return '';
  }
  if (typeof message.text === 'string') {
    return message.text;
  }
  if (typeof message.content === 'string') {
    return message.content;
  }
  if (!Array.isArray(message.content)) {
    return '';
  }

  const parts = message.content
    .map((entry: any) => {
      if (!entry || typeof entry !== 'object') {
        return '';
      }
      if (entry.type === 'text' && typeof entry.text === 'string') {
        return entry.text;
      }
      if (entry.type === 'thinking' && typeof entry.thinking === 'string') {
        return entry.thinking;
      }
      if (entry.type === 'toolresult' && typeof entry.text === 'string') {
        return entry.text;
      }
      if (entry.type === 'toolcall' && typeof entry.name === 'string') {
        return `[工具：${entry.name}]`;
      }
      return '';
    })
    .filter(Boolean);

  return parts.join('\n\n');
}

function normalizeRole(role: string | undefined): HomeMessageRole {
  switch ((role || '').toLowerCase()) {
    case 'assistant':
      return 'assistant';
    case 'user':
      return 'user';
    case 'toolresult':
      return 'tool';
    default:
      return 'system';
  }
}

function senderLabelForMessage(message: any, role: HomeMessageRole, assistantFallback: string): string {
  if (role === 'user') {
    if (typeof message?.senderLabel === 'string' && message.senderLabel.trim()) {
      return message.senderLabel.trim();
    }
    return '你';
  }
  if (role === 'assistant') {
    if (typeof message?.senderLabel === 'string' && message.senderLabel.trim()) {
      return message.senderLabel.trim();
    }
    return assistantFallback;
  }
  if (role === 'tool') {
    return '工具';
  }
  return '系统';
}

function messageTimestamp(message: any): number {
  if (typeof message?.timestamp === 'number') {
    return message.timestamp;
  }
  return Date.now();
}

function messageIdentity(message: any, index: number): string {
  if (typeof message?.id === 'string' && message.id) {
    return `msg:${message.id}`;
  }
  if (typeof message?.messageId === 'string' && message.messageId) {
    return `msg:${message.messageId}`;
  }
  if (typeof message?.toolCallId === 'string' && message.toolCallId) {
    return `tool:${message.toolCallId}`;
  }
  return `msg:${message?.role || 'unknown'}:${messageTimestamp(message)}:${index}`;
}

function normalizeChatMessage(message: any, index: number, assistantFallback: string): HomeMessage | null {
  const content = extractMessageText(message).trim();
  if (!content || isNoReply(content)) {
    return null;
  }

  const role = normalizeRole(message?.role);
  return {
    id: messageIdentity(message, index),
    role,
    sender: senderLabelForMessage(message, role, assistantFallback),
    content,
    timestamp: messageTimestamp(message),
    isMe: role === 'user',
  };
}

function appendMessage(list: HomeMessage[], next: HomeMessage): HomeMessage[] {
  if (list.some(item => item.id === next.id)) {
    return list;
  }
  return [...list, next];
}

function channelLabel(snapshot: any, id: string): string {
  if (Array.isArray(snapshot?.channelMeta)) {
    const meta = snapshot.channelMeta.find((entry: any) => entry?.id === id);
    if (typeof meta?.label === 'string' && meta.label.trim()) {
      return meta.label.trim();
    }
  }
  if (isPlainObject(snapshot?.channelLabels) && typeof snapshot.channelLabels[id] === 'string') {
    return snapshot.channelLabels[id];
  }
  return id;
}

function countChannelAccounts(accounts: any[]) {
  return accounts.reduce(
    (stats, account) => {
      const isConnected =
        account?.connected === true ||
        account?.running === true ||
        (isPlainObject(account?.probe) && account.probe.ok === true);
      if (isConnected) {
        stats.connected += 1;
      }
      if (account?.configured) {
        stats.configured += 1;
      }
      if (account?.enabled) {
        stats.enabled += 1;
      }
      return stats;
    },
    { connected: 0, configured: 0, enabled: 0 },
  );
}

function normalizeMembers(snapshot: any): TeamMember[] {
  if (!snapshot || typeof snapshot !== 'object') {
    return [];
  }

  const known = new Set<string>();
  const orderedIds: string[] = [];
  const addId = (value: unknown) => {
    if (typeof value !== 'string' || !value.trim() || known.has(value)) {
      return;
    }
    known.add(value);
    orderedIds.push(value);
  };

  if (Array.isArray(snapshot.channelOrder)) {
    snapshot.channelOrder.forEach(addId);
  }
  if (Array.isArray(snapshot.channelMeta)) {
    snapshot.channelMeta.forEach((entry: any) => addId(entry?.id));
  }
  if (isPlainObject(snapshot.channelAccounts)) {
    Object.keys(snapshot.channelAccounts).forEach(addId);
  }

  return orderedIds.map(id => {
    const accounts = Array.isArray(snapshot.channelAccounts?.[id]) ? snapshot.channelAccounts[id] : [];
    const stats = countChannelAccounts(accounts);
    const status =
      stats.connected > 0 ? 'online' : stats.configured > 0 || stats.enabled > 0 ? 'busy' : 'offline';

    return {
      id,
      label: channelLabel(snapshot, id),
      status,
      connected: stats.connected,
      total: accounts.length,
      configured: stats.configured,
      enabled: stats.enabled,
    };
  });
}

function fallbackMembers(hasToken: boolean): TeamMember[] {
  return [
    {
      id: 'main',
      label: 'OpenClaw',
      status: hasToken ? 'online' : 'offline',
      connected: hasToken ? 1 : 0,
      total: 1,
      configured: hasToken ? 1 : 0,
      enabled: hasToken ? 1 : 0,
    },
  ];
}

function resolveCanonicalSessionKey(info: OpenClawGatewayInfo, hello: OpenClawHelloPayload | null): string {
  const mainSessionKey = hello?.snapshot?.sessionDefaults?.mainSessionKey;
  if (typeof mainSessionKey === 'string' && mainSessionKey.trim()) {
    return mainSessionKey.trim();
  }
  return info.session_key;
}

function buildSessionPrefix(mainSessionKey: string): string {
  const lastColon = mainSessionKey.lastIndexOf(':');
  if (lastColon >= 0) {
    return mainSessionKey.slice(0, lastColon + 1);
  }
  return `${mainSessionKey}:`;
}

function buildResolvedGatewayInfo(
  info: OpenClawGatewayInfo,
  hello: OpenClawHelloPayload | null,
  sessionsPayload?: OpenClawSessionsListPayload | null,
): ResolvedGatewayInfo {
  const mainSessionKey = resolveCanonicalSessionKey(info, hello);
  return {
    ...info,
    session_key: mainSessionKey,
    main_session_key: mainSessionKey,
    session_prefix: buildSessionPrefix(mainSessionKey),
    preferred_session_keys: buildPreferredSessionKeys(mainSessionKey, sessionsPayload),
  };
}

function buildPreferredSessionKeys(
  mainSessionKey: string,
  sessionsPayload?: OpenClawSessionsListPayload | null,
): Record<string, string> {
  const preferred: Record<string, string> = { main: mainSessionKey };
  const latestByMember = new Map<string, { key: string; updatedAt: number; directRank: number }>();
  const sessions = Array.isArray(sessionsPayload?.sessions) ? sessionsPayload.sessions : [];

  for (const session of sessions) {
    const key = typeof session?.key === 'string' ? session.key.trim() : '';
    if (!key) {
      continue;
    }
    if (key === mainSessionKey) {
      preferred.main = key;
      continue;
    }

    const memberId =
      (typeof session?.lastChannel === 'string' && session.lastChannel.trim()) ||
      (typeof session?.channel === 'string' && session.channel.trim()) ||
      (typeof session?.origin?.provider === 'string' && session.origin.provider.trim()) ||
      '';
    if (!memberId) {
      continue;
    }

    const candidate = {
      key,
      updatedAt: typeof session?.updatedAt === 'number' ? session.updatedAt : 0,
      directRank: session?.kind === 'direct' ? 1 : 0,
    };
    const current = latestByMember.get(memberId);
    if (
      !current ||
      candidate.updatedAt > current.updatedAt ||
      (candidate.updatedAt === current.updatedAt && candidate.directRank > current.directRank)
    ) {
      latestByMember.set(memberId, candidate);
    }
  }

  for (const [memberId, candidate] of latestByMember.entries()) {
    preferred[memberId] = candidate.key;
  }
  return preferred;
}

function buildMemberSessionKey(info: ResolvedGatewayInfo | null, memberId: string): string {
  if (!info) {
    return memberId;
  }
  const preferred = info.preferred_session_keys[memberId];
  if (typeof preferred === 'string' && preferred.trim()) {
    return preferred;
  }
  if (memberId === 'main') {
    return info.main_session_key;
  }
  return `${info.session_prefix}${memberId}`;
}

function memberStatusText(member: TeamMember): string {
  if (member.status === 'online') {
    return '在线';
  }
  if (member.status === 'busy') {
    return '忙碌';
  }
  return '离线';
}

function memberStatusClasses(status: 'online' | 'busy' | 'offline') {
  if (status === 'online') {
    return 'bg-green-500';
  }
  if (status === 'busy') {
    return 'bg-orange-500';
  }
  return 'bg-zinc-600';
}

export default function HomeChatView({ hasToken, onOpenWorkspace }: Props) {
  const [inputMessage, setInputMessage] = useState('');
  const [chatMessages, setChatMessages] = useState<HomeMessage[]>([]);
  const [chatStream, setChatStream] = useState('');
  const [connectionState, setConnectionState] = useState<'idle' | 'connecting' | 'ready' | 'error'>(
    hasToken ? 'connecting' : 'idle',
  );
  const [statusText, setStatusText] = useState(
    hasToken ? '正在连接 OpenClaw 网关...' : '登录后可连接 OpenClaw',
  );
  const [activeRunId, setActiveRunId] = useState<string | null>(null);
  const [members, setMembers] = useState<TeamMember[]>(fallbackMembers(hasToken));
  const [activeMemberId, setActiveMemberId] = useState('main');
  const [activeMemberLabel, setActiveMemberLabel] = useState('OpenClaw');
  const [historyLoading, setHistoryLoading] = useState(false);
  const bottomRef = useRef<HTMLDivElement>(null);
  const clientRef = useRef<OpenClawGatewayClient | null>(null);
  const gatewayInfoRef = useRef<ResolvedGatewayInfo | null>(null);
  const activeSessionKeyRef = useRef('main');
  const activeMemberIdRef = useRef('main');
  const activeMemberLabelRef = useRef('OpenClaw');
  const chatStreamRef = useRef('');
  const historyRequestRef = useRef(0);

  const setLiveStream = (value: string) => {
    chatStreamRef.current = value;
    setChatStream(value);
  };

  const loadSessionHistory = async (sessionKey: string, assistantLabel: string) => {
    if (!clientRef.current) {
      return;
    }

    const requestId = historyRequestRef.current + 1;
    historyRequestRef.current = requestId;
    setHistoryLoading(true);
    setChatMessages([]);
    setLiveStream('');
    setActiveRunId(null);

    try {
      const payload = await clientRef.current.request('chat.history', {
        sessionKey,
        limit: 200,
      });
      if (historyRequestRef.current !== requestId) {
        return;
      }

      const normalizedHistory = (Array.isArray(payload?.messages) ? payload.messages : [])
        .map((message: any, index: number) => normalizeChatMessage(message, index, assistantLabel))
        .filter((message: HomeMessage | null): message is HomeMessage => message !== null);

      setChatMessages(normalizedHistory);
      setConnectionState('ready');
      setStatusText('已连接');
    } catch (error) {
      if (historyRequestRef.current !== requestId) {
        return;
      }
      setChatMessages([]);
      setStatusText(errorToString(error));
    } finally {
      if (historyRequestRef.current === requestId) {
        setHistoryLoading(false);
      }
    }
  };

  const selectMember = (member: TeamMember) => {
    setActiveMemberId(member.id);
    activeMemberIdRef.current = member.id;
    setActiveMemberLabel(member.label);
    activeMemberLabelRef.current = member.label;

    const sessionKey = buildMemberSessionKey(gatewayInfoRef.current, member.id);
    activeSessionKeyRef.current = sessionKey;

    if (clientRef.current && gatewayInfoRef.current) {
      void loadSessionHistory(sessionKey, member.label);
    }
  };

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [chatMessages, chatStream]);

  useEffect(() => {
    let cancelled = false;

    const resetState = () => {
      setChatMessages([]);
      setMembers(fallbackMembers(hasToken));
      setActiveMemberId('main');
      activeMemberIdRef.current = 'main';
      setActiveMemberLabel('OpenClaw');
      activeMemberLabelRef.current = 'OpenClaw';
      activeSessionKeyRef.current = 'main';
      setActiveRunId(null);
      setLiveStream('');
      setHistoryLoading(false);
    };

    const finalizeStreamMessage = (payload?: any) => {
      const streamText = chatStreamRef.current.trim();
      const fallback =
        streamText.length > 0
          ? {
              role: 'assistant',
              senderLabel: activeMemberLabelRef.current,
              content: streamText,
              timestamp: Date.now(),
            }
          : null;
      const normalized = normalizeChatMessage(
        payload?.message || fallback,
        Date.now(),
        activeMemberLabelRef.current,
      );
      if (normalized) {
        setChatMessages(prev => appendMessage(prev, normalized));
      }
      setLiveStream('');
      setActiveRunId(null);
    };

    const handleChatEvent = (payload: any) => {
      const sessionKey = activeSessionKeyRef.current;
      if (!payload || payload.sessionKey !== sessionKey) {
        return;
      }

      if (typeof payload.runId === 'string' && payload.runId) {
        setActiveRunId(payload.runId);
      }

      if (payload.state === 'delta') {
        const next = extractMessageText(payload.message);
        if (next) {
          setLiveStream(next);
        }
        setConnectionState('ready');
        setStatusText('正在回复...');
        return;
      }

      if (payload.state === 'final') {
        finalizeStreamMessage(payload);
        setConnectionState('ready');
        setStatusText('已连接');
        return;
      }

      if (payload.state === 'aborted') {
        finalizeStreamMessage(payload);
        setConnectionState('ready');
        setStatusText('已停止');
        return;
      }

      if (payload.state === 'error') {
        if (chatStreamRef.current.trim()) {
          finalizeStreamMessage(payload);
        } else {
          setLiveStream('');
          setActiveRunId(null);
        }
        setConnectionState('ready');
        setStatusText(payload.errorMessage || '运行失败');
      }
    };

    const refreshSessionKeys = async (reason: 'connect' | 'sessions.changed' = 'connect') => {
      if (!clientRef.current || !gatewayInfoRef.current) {
        return;
      }
      try {
        const sessionsPayload = (await clientRef.current.request('sessions.list', {})) as OpenClawSessionsListPayload;
        if (cancelled || !gatewayInfoRef.current) {
          return;
        }
        const nextInfo = buildResolvedGatewayInfo(
          gatewayInfoRef.current,
          { snapshot: { sessionDefaults: { mainSessionKey: gatewayInfoRef.current.main_session_key } } },
          sessionsPayload,
        );
        gatewayInfoRef.current = nextInfo;

        const nextSessionKey = buildMemberSessionKey(nextInfo, activeMemberIdRef.current);
        if (nextSessionKey !== activeSessionKeyRef.current) {
          activeSessionKeyRef.current = nextSessionKey;
          if (reason === 'sessions.changed') {
            void loadSessionHistory(nextSessionKey, activeMemberLabelRef.current);
          }
        }
      } catch {
        // Ignore session refresh failures and keep the last known mapping.
      }
    };

    const connectGateway = async () => {
      clientRef.current?.close();
      clientRef.current = null;
      gatewayInfoRef.current = null;
      historyRequestRef.current = 0;
      resetState();

      if (!hasToken) {
        setConnectionState('idle');
        setStatusText('登录后可连接 OpenClaw');
        return;
      }

      setConnectionState('connecting');
      setStatusText('正在连接 OpenClaw 网关...');

      try {
        const response = await apiService.getOpenClawGateway();
        if (cancelled) {
          return;
        }

        const info = response.data as OpenClawGatewayInfo;

        const client = new OpenClawGatewayClient({
          url: info.ws_url,
          token: info.token,
          onEvent: (event: OpenClawGatewayEvent) => {
            if (cancelled) {
              return;
            }
            if (event.event === 'chat') {
              handleChatEvent(event.payload);
              return;
            }
            if (event.event === 'sessions.changed') {
              void refreshSessionKeys('sessions.changed');
            }
          },
          onClose: () => {
            if (cancelled) {
              return;
            }
            setConnectionState('error');
            setStatusText('OpenClaw 连接已断开');
          },
        });

        clientRef.current = client;
        const hello = (await client.connect()) as OpenClawHelloPayload | null;
        const sessionsPayload = (await client.request('sessions.list', {})) as OpenClawSessionsListPayload;
        const resolvedInfo = buildResolvedGatewayInfo(info, hello, sessionsPayload);
        gatewayInfoRef.current = resolvedInfo;
        if (cancelled) {
          client.close();
          return;
        }

        const channelsPayload = await client.request('channels.status', {
          probe: false,
          timeoutMs: 4000,
        });
        if (cancelled) {
          return;
        }

        const resolvedMembers = normalizeMembers(channelsPayload);
        const nextMembers = resolvedMembers.length > 0 ? resolvedMembers : fallbackMembers(true);
        setMembers(nextMembers);
        setConnectionState('ready');
        setStatusText('已连接');
        selectMember(nextMembers[0]);
      } catch (error) {
        if (cancelled) {
          return;
        }
        setConnectionState('error');
        setStatusText(errorToString(error));
      }
    };

    connectGateway();

    return () => {
      cancelled = true;
      clientRef.current?.close();
      clientRef.current = null;
      gatewayInfoRef.current = null;
    };
  }, [hasToken]);

  const handleSendMessage = async () => {
    const text = inputMessage.trim();
    if (!text || !clientRef.current || !gatewayInfoRef.current || activeRunId) {
      return;
    }

    setChatMessages(prev =>
      appendMessage(prev, {
        id: `local:${createOpenClawId()}`,
        role: 'user',
        sender: '你',
        content: text,
        timestamp: Date.now(),
        isMe: true,
      }),
    );
    setInputMessage('');
    setLiveStream('');
    setStatusText('正在处理...');

    try {
      const response = await clientRef.current.request('chat.send', {
        sessionKey: activeSessionKeyRef.current,
        message: text,
        deliver: false,
        idempotencyKey: createOpenClawId(),
      });
      if (typeof response?.runId === 'string' && response.runId) {
        setActiveRunId(response.runId);
      }
    } catch (error) {
      const detail = errorToString(error);
      setStatusText(`发送失败: ${detail}`);
      setChatMessages(prev =>
        appendMessage(prev, {
          id: `error:${createOpenClawId()}`,
          role: 'system',
          sender: '系统',
          content: `发送失败: ${detail}`,
          timestamp: Date.now(),
        }),
      );
    }
  };

  const handleStopRun = async () => {
    if (!clientRef.current || !gatewayInfoRef.current) {
      return;
    }

    try {
      await clientRef.current.request('chat.abort', activeRunId
        ? { sessionKey: activeSessionKeyRef.current, runId: activeRunId }
        : { sessionKey: activeSessionKeyRef.current });
      setStatusText('已发送停止请求');
    } catch (error) {
      setStatusText(`停止失败: ${errorToString(error)}`);
    }
  };

  const canSend = hasToken && connectionState === 'ready' && !activeRunId;

  return (
    <div className="min-h-screen bg-black text-white flex overflow-hidden">
      <main className="flex-grow flex flex-col min-w-0 bg-black relative">
        <button
          onClick={onOpenWorkspace}
          className="fixed right-4 top-4 z-30 rounded-full border border-white/10 bg-black/70 px-3 py-1.5 text-[11px] font-black uppercase tracking-widest text-zinc-300 backdrop-blur transition hover:border-orange-500/40 hover:text-white"
        >
          原版 UI
        </button>

        <header className="sticky top-0 z-10 border-b border-white/10 bg-black/50 px-6 py-4 backdrop-blur-md">
          <div className="flex items-center gap-3">
            {connectionState === 'connecting' ? (
              <LoaderCircle className="h-3.5 w-3.5 animate-spin text-orange-500" />
            ) : (
              <div
                className={`h-2 w-2 rounded-full ${
                  connectionState === 'ready' ? 'bg-green-500' : connectionState === 'error' ? 'bg-red-500' : 'bg-zinc-600'
                }`}
              />
            )}
            <div>
              <h2 className="text-lg font-bold text-white">{activeMemberLabel}</h2>
              <div className="text-[10px] font-black uppercase tracking-widest text-zinc-500">{statusText}</div>
            </div>
          </div>
        </header>

        <div className="flex-grow flex overflow-hidden relative">
          <div className="flex-grow flex flex-col min-w-0 bg-black">
            <div className="flex-grow overflow-y-auto p-6 space-y-8 scroll-smooth custom-scrollbar">
              <div className="max-w-3xl mx-auto space-y-8">
                {!hasToken && (
                  <div className="rounded-3xl border border-white/10 bg-zinc-950 p-6 text-sm text-zinc-300">
                    登录后才能连接 OpenClaw 对话。
                  </div>
                )}

                {hasToken && historyLoading && (
                  <div className="rounded-3xl border border-white/10 bg-zinc-950 p-6 text-sm text-zinc-300">
                    正在加载 {activeMemberLabel} 的历史消息...
                  </div>
                )}

                {hasToken && connectionState === 'error' && !historyLoading && chatMessages.length === 0 && (
                  <div className="rounded-3xl border border-red-500/30 bg-red-500/10 p-6 text-sm text-red-100">
                    <div className="mb-2 flex items-center gap-2 font-semibold">
                      <AlertCircle className="h-4 w-4" />
                      对话连接失败
                    </div>
                    <div className="whitespace-pre-wrap break-words text-red-100/80">{statusText}</div>
                  </div>
                )}

                {chatMessages.map(chat => (
                  <div key={chat.id} className={`flex gap-4 ${chat.isMe ? 'flex-row-reverse' : ''}`}>
                    {!chat.isMe && (
                      <div className="w-10 h-10 rounded-xl shrink-0 flex items-center justify-center font-black text-xs text-white bg-zinc-800">
                        {chat.sender[0]}
                      </div>
                    )}
                    <div className={`flex flex-col gap-1.5 max-w-[80%] ${chat.isMe ? 'items-end' : ''}`}>
                      <div className="flex items-center gap-2 px-1">
                        <span className="text-[10px] font-black text-gray-500 uppercase tracking-widest">
                          {chat.sender}
                        </span>
                        <span className="text-[9px] text-gray-600">{formatTime(chat.timestamp)}</span>
                      </div>
                      <div
                        className={`p-4 rounded-2xl text-sm leading-relaxed whitespace-pre-wrap break-words ${
                          chat.isMe
                            ? 'bg-orange-500 text-black font-medium'
                            : 'bg-zinc-900 text-gray-300 border border-white/5'
                        }`}
                      >
                        {chat.content}
                      </div>
                    </div>
                  </div>
                ))}

                {chatStream && (
                  <div className="flex gap-4">
                    <div className="w-10 h-10 rounded-xl shrink-0 flex items-center justify-center font-black text-xs text-white bg-zinc-800">
                      {activeMemberLabel[0]}
                    </div>
                    <div className="flex flex-col gap-1.5 max-w-[80%]">
                      <div className="flex items-center gap-2 px-1">
                        <span className="text-[10px] font-black text-gray-500 uppercase tracking-widest">
                          {activeMemberLabel}
                        </span>
                        <span className="text-[9px] text-gray-600">实时</span>
                      </div>
                      <div className="p-4 rounded-2xl text-sm leading-relaxed whitespace-pre-wrap break-words bg-zinc-900 text-gray-300 border border-white/5">
                        {chatStream}
                      </div>
                    </div>
                  </div>
                )}

                <div ref={bottomRef} />
              </div>
            </div>

            <div className="p-6 border-t border-white/5 bg-black/50 backdrop-blur-xl">
              <div className="max-w-3xl mx-auto relative">
                <div className="absolute left-4 top-1/2 -translate-y-1/2 flex items-center gap-2 text-gray-500">
                  <Zap className="w-4 h-4" />
                </div>
                <input
                  type="text"
                  placeholder={hasToken ? `给 ${activeMemberLabel} 下达指令...` : '登录后可发送消息'}
                  value={inputMessage}
                  onChange={(event) => setInputMessage(event.target.value)}
                  onKeyDown={(event) => event.key === 'Enter' && canSend && handleSendMessage()}
                  disabled={!canSend}
                  className="w-full bg-zinc-900 border border-white/10 rounded-2xl py-4 pl-12 pr-24 text-sm focus:border-orange-500 transition-all outline-none disabled:cursor-not-allowed disabled:opacity-60"
                />
                <div className="absolute right-2 top-1/2 -translate-y-1/2 flex items-center gap-2">
                  {activeRunId ? (
                    <button
                      onClick={handleStopRun}
                      className="bg-white text-black p-2 rounded-xl hover:bg-zinc-200 transition-all shadow-lg"
                    >
                      <Square className="w-4 h-4" />
                    </button>
                  ) : (
                    <button
                      onClick={handleSendMessage}
                      disabled={!canSend}
                      className="bg-orange-500 text-black p-2 rounded-xl hover:bg-orange-400 transition-all shadow-lg disabled:cursor-not-allowed disabled:opacity-50"
                    >
                      <Send className="w-4 h-4" />
                    </button>
                  )}
                </div>
              </div>
            </div>
          </div>

          <aside className="w-72 border-l border-white/10 bg-zinc-950 hidden lg:flex flex-col shrink-0">
            <div className="p-6 border-b border-white/5">
              <h3 className="text-[10px] font-black text-gray-500 uppercase tracking-widest mb-4">
                军团成员（通道）
              </h3>
              <div className="space-y-4">
                {members.map(member => {
                  const isActive = member.id === activeMemberId;
                  return (
                    <button
                      key={member.id}
                      onClick={() => selectMember(member)}
                      className={`w-full flex items-center gap-3 text-left rounded-2xl px-3 py-3 transition-all ${
                        isActive ? 'bg-white/5 border border-white/10' : 'hover:bg-white/5'
                      }`}
                    >
                      <div className="relative">
                        <div className="w-10 h-10 rounded-xl flex items-center justify-center text-white font-black text-xs bg-zinc-800">
                          {member.label[0]}
                        </div>
                        <div
                          className={`absolute -bottom-0.5 -right-0.5 w-3 h-3 rounded-full border-2 border-zinc-950 ${memberStatusClasses(member.status)}`}
                        />
                      </div>
                      <div className="flex-grow min-w-0">
                        <div
                          className={`text-xs font-bold truncate transition-colors ${
                            isActive ? 'text-orange-500' : 'text-white'
                          }`}
                        >
                          {member.label}
                        </div>
                        <div className="text-[10px] text-gray-500 font-medium">
                          {memberStatusText(member)} · {member.connected}/{member.total || 1}
                        </div>
                      </div>
                    </button>
                  );
                })}
              </div>
            </div>
          </aside>
        </div>
      </main>
    </div>
  );
}
