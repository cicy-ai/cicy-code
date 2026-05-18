import { useCallback, useEffect, useRef, useState } from 'react';
import { X, RefreshCw, Loader2, Monitor, Globe, Cpu, Copy, Check, Zap, MessageSquare, Wifi, WifiOff } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import apiService from '../../services/api';
import { cn } from '../../lib/utils';

interface WsClient {
  master_agent_id: string;
  active_agent_id: string;
  client_id: string;
  isElectron: boolean;
  platform: string;
  user_agent: string;
  remote_addr: string;
  connected_at: string;
  uptime_sec: number;
}

type PingState = 'idle' | 'pinging' | 'ok' | 'fail';

function humanUptime(sec: number) {
  if (sec < 60) return `${sec}s`;
  if (sec < 3600) return `${Math.floor(sec / 60)}m`;
  if (sec < 86400) return `${Math.floor(sec / 3600)}h`;
  return `${Math.floor(sec / 86400)}d`;
}

function parseCicyDesktopVersion(ua: string): string | null {
  const m = ua.match(/ElectronMCP\/(\S+)/i);
  return m ? m[1] : null;
}

function platformOS(platform: string): string {
  if (platform === 'win') return 'Windows';
  if (platform === 'darwin') return 'macOS';
  if (platform === 'linux') return 'Linux';
  return platform || 'Unknown';
}

function ClientBadges({ client }: { client: WsClient }) {
  const cicyVer = client.isElectron ? parseCicyDesktopVersion(client.user_agent) : null;

  if (cicyVer) {
    return (
      <div className="flex items-center gap-1.5 flex-wrap">
        <Monitor className="h-3.5 w-3.5 text-sky-400 shrink-0" />
        <span className="text-[12px] font-semibold text-sky-300">cicy-desktop</span>
        <span className="rounded px-1 py-0.5 text-[9px] font-mono bg-sky-500/15 text-sky-400 ring-1 ring-sky-500/30">
          v{cicyVer}
        </span>
        <span className="text-[11px] text-zinc-500">· {platformOS(client.platform)}</span>
      </div>
    );
  }

  if (client.isElectron) {
    return (
      <div className="flex items-center gap-1.5">
        <Monitor className="h-3.5 w-3.5 text-violet-400 shrink-0" />
        <span className="text-[12px] font-medium text-zinc-200">Electron Desktop</span>
        <span className="text-[11px] text-zinc-500">· {platformOS(client.platform)}</span>
      </div>
    );
  }

  if (!client.platform || client.platform === 'node') {
    return (
      <div className="flex items-center gap-1.5">
        <Cpu className="h-3.5 w-3.5 text-zinc-500 shrink-0" />
        <span className="text-[12px] font-medium text-zinc-400">node</span>
      </div>
    );
  }

  return (
    <div className="flex items-center gap-1.5">
      <Globe className="h-3.5 w-3.5 text-emerald-400 shrink-0" />
      <span className="text-[12px] font-medium text-zinc-200">Browser</span>
      <span className="text-[11px] text-zinc-500">· {platformOS(client.platform)}</span>
    </div>
  );
}

function CopyButton({ text }: { text: string }) {
  const [copied, setCopied] = useState(false);
  const copy = () => {
    navigator.clipboard.writeText(text).catch(() => {});
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  };
  return (
    <button
      onClick={copy}
      className="ml-1 rounded p-0.5 text-zinc-600 hover:text-zinc-300 transition-colors"
      title="复制"
    >
      {copied ? <Check className="h-3 w-3 text-emerald-400" /> : <Copy className="h-3 w-3" />}
    </button>
  );
}

function ClientCard({
  client,
  paneId,
  onSent,
}: {
  client: WsClient;
  paneId: string;
  onSent: () => void;
}) {
  const { t } = useTranslation('agentInspector');
  const [pingState, setPingState] = useState<PingState>('idle');
  const [pingMs, setPingMs] = useState<number | null>(null);
  const [sending, setSending] = useState(false);
  const [sendOk, setSendOk] = useState(false);

  const doPing = async () => {
    if (pingState === 'pinging') return;
    setPingState('pinging');
    setPingMs(null);
    const t0 = performance.now();
    try {
      await (apiService as any).pingChatClient(client.client_id);
      setPingMs(Math.round(performance.now() - t0));
      setPingState('ok');
    } catch {
      setPingState('fail');
    }
    setTimeout(() => setPingState('idle'), 4000);
  };

  const doSend = async () => {
    if (sending) return;
    setSending(true);
    setSendOk(false);
    try {
      const text = t('webClientsTestPrompt', { clientId: client.client_id });
      await apiService.sendCommand(paneId, text, true);
      setSendOk(true);
      // Brief flash then close the drawer
      setTimeout(onSent, 600);
    } catch {
      // agent will surface errors naturally
    } finally {
      setSending(false);
    }
  };

  const shortId =
    client.client_id.length > 30
      ? `${client.client_id.slice(0, 14)}…${client.client_id.slice(-12)}`
      : client.client_id;

  const isCicyDesktop = client.isElectron && !!parseCicyDesktopVersion(client.user_agent);

  return (
    <div
      data-id={`web-client-card-${client.client_id}`}
      className={cn(
        'rounded-lg border px-4 py-3 transition-colors',
        isCicyDesktop
          ? 'border-sky-500/20 bg-sky-500/[0.03] hover:border-sky-500/35 hover:bg-sky-500/[0.06]'
          : 'border-white/[0.07] bg-white/[0.02] hover:border-white/[0.12] hover:bg-white/[0.04]'
      )}
    >
      {/* Top row — identity + uptime + online dot */}
      <div className="flex items-start justify-between gap-2">
        <ClientBadges client={client} />
        <div className="flex items-center gap-2 shrink-0 pt-0.5">
          <span className="text-[10px] tabular-nums text-zinc-600" title={client.connected_at}>
            {humanUptime(client.uptime_sec)}
          </span>
          <span className="h-1.5 w-1.5 rounded-full bg-emerald-400 shadow-[0_0_4px_#34d399]" />
        </div>
      </div>

      {/* Client ID */}
      <div className="mt-2 flex items-center gap-0.5 font-mono text-[10px] text-zinc-600">
        <span className="truncate select-all" title={client.client_id}>{shortId}</span>
        <CopyButton text={client.client_id} />
      </div>

      {/* Meta */}
      <div className="mt-1 flex flex-wrap gap-x-3 gap-y-0.5 text-[10px] text-zinc-700">
        {client.remote_addr && (
          <span title="remote addr">{client.remote_addr.split(':')[0]}</span>
        )}
        {client.master_agent_id && (
          <span title="master agent">⌂ {client.master_agent_id}</span>
        )}
        {client.active_agent_id && client.active_agent_id !== client.master_agent_id && (
          <span title="active agent">▸ {client.active_agent_id}</span>
        )}
      </div>

      {/* Actions */}
      <div className="mt-3 flex items-center gap-2">
        {/* Ping */}
        <button
          onClick={doPing}
          disabled={pingState === 'pinging'}
          className={cn(
            'inline-flex items-center gap-1.5 rounded-md px-2.5 py-1 text-[11px] transition-colors',
            pingState === 'ok'
              ? 'bg-emerald-500/15 text-emerald-300 ring-1 ring-emerald-500/30'
              : pingState === 'fail'
                ? 'bg-red-500/15 text-red-300 ring-1 ring-red-500/30'
                : 'bg-white/[0.05] text-zinc-400 hover:bg-white/[0.09] hover:text-zinc-200'
          )}
        >
          {pingState === 'pinging' ? (
            <Loader2 className="h-3 w-3 animate-spin" />
          ) : pingState === 'ok' ? (
            <Wifi className="h-3 w-3" />
          ) : pingState === 'fail' ? (
            <WifiOff className="h-3 w-3" />
          ) : (
            <Zap className="h-3 w-3" />
          )}
          <span>
            {pingState === 'pinging'
              ? t('webClientsPinging')
              : pingState === 'ok'
                ? `${pingMs}ms`
                : pingState === 'fail'
                  ? t('webClientsPingTimeout')
                  : t('webClientsPing')}
          </span>
        </button>

        {/* 发消息 */}
        <button
          onClick={doSend}
          disabled={sending}
          title={`→ ${paneId}`}
          className={cn(
            'inline-flex items-center gap-1.5 rounded-md px-2.5 py-1 text-[11px] ring-1 transition-colors',
            sendOk
              ? 'bg-emerald-500/15 text-emerald-300 ring-emerald-500/30'
              : 'bg-indigo-500/15 text-indigo-300 ring-indigo-500/30 hover:bg-indigo-500/25 disabled:opacity-50'
          )}
        >
          {sending ? (
            <Loader2 className="h-3 w-3 animate-spin" />
          ) : sendOk ? (
            <Check className="h-3 w-3" />
          ) : (
            <MessageSquare className="h-3 w-3" />
          )}
          <span>
            {sending
              ? t('webClientsSending')
              : sendOk
                ? t('webClientsSent')
                : t('webClientsSend')}
          </span>
        </button>
      </div>
    </div>
  );
}

export function WebClientsDrawer({
  open,
  onClose,
  paneId,
}: {
  open: boolean;
  onClose: () => void;
  paneId: string;
}) {
  const { t } = useTranslation('agentInspector');
  const [clients, setClients] = useState<WsClient[]>([]);
  const [loading, setLoading] = useState(false);
  const [lastRefresh, setLastRefresh] = useState<Date | null>(null);
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null);

  const load = useCallback(async (showSpinner = false) => {
    if (showSpinner) setLoading(true);
    try {
      const res = await (apiService as any).getChatClients();
      setClients(Array.isArray(res?.data) ? res.data : []);
      setLastRefresh(new Date());
    } catch {
      // keep stale data on error
    } finally {
      if (showSpinner) setLoading(false);
    }
  }, []);

  useEffect(() => {
    if (!open) {
      if (pollRef.current) clearInterval(pollRef.current);
      return;
    }
    load(true);
    pollRef.current = setInterval(() => load(false), 5000);
    return () => { if (pollRef.current) clearInterval(pollRef.current); };
  }, [open, load]);

  if (!open) return null;

  // cicy-desktop first, then other Electron, then browser, then node
  const sorted = [...clients].sort((a, b) => {
    const rank = (c: WsClient) => {
      if (c.isElectron && parseCicyDesktopVersion(c.user_agent)) return 0;
      if (c.isElectron) return 1;
      if (c.platform && c.platform !== 'node') return 2;
      return 3;
    };
    const dr = rank(a) - rank(b);
    return dr !== 0 ? dr : a.uptime_sec - b.uptime_sec;
  });

  return (
    <div
      data-id="web-clients-drawer-overlay"
      className="fixed inset-0 z-[100000] flex justify-end cursor-pointer"
      onClick={onClose}
    >
      <div className="absolute inset-0 bg-black/55 backdrop-blur-sm" />
      <aside
        data-id="web-clients-drawer"
        className="relative z-10 flex h-full w-[420px] max-w-[96vw] cursor-default flex-col border-l border-white/[0.08] bg-[#0f0f11] shadow-2xl shadow-black/60"
        onClick={(e) => e.stopPropagation()}
      >
        {/* Header */}
        <header className="flex shrink-0 items-center justify-between border-b border-white/[0.06] px-5 py-4">
          <div className="flex items-center gap-2.5 min-w-0">
            <Globe className="h-4 w-4 shrink-0 text-zinc-400" />
            <div className="min-w-0">
              <h2 className="text-[15px] font-semibold text-white">
                {t('webClientsTitle')}
                {clients.length > 0 && (
                  <span className="ml-2 rounded-full bg-white/[0.08] px-2 py-0.5 text-[11px] font-normal text-zinc-400">
                    {clients.length}
                  </span>
                )}
              </h2>
              <p className="mt-0.5 text-[11px] text-zinc-600 truncate">
                {lastRefresh
                  ? t('webClientsRefreshed', { time: lastRefresh.toLocaleTimeString(), pane: paneId })
                  : t('webClientsSubtitle')}
              </p>
            </div>
          </div>
          <div className="flex items-center gap-1 shrink-0">
            <button
              onClick={() => load(true)}
              disabled={loading}
              className="rounded-md p-1.5 text-zinc-500 hover:bg-white/[0.06] hover:text-zinc-200 transition-colors disabled:opacity-40"
              title="Refresh"
            >
              {loading
                ? <Loader2 className="h-4 w-4 animate-spin" />
                : <RefreshCw className="h-4 w-4" />}
            </button>
            <button
              onClick={onClose}
              className="rounded-md p-1.5 text-zinc-500 hover:bg-white/[0.06] hover:text-zinc-200 transition-colors"
            >
              <X className="h-4 w-4" />
            </button>
          </div>
        </header>

        {/* Client list */}
        <div className="flex-1 overflow-y-auto px-4 py-4">
          {loading && clients.length === 0 ? (
            <div className="flex items-center justify-center py-20 text-zinc-700">
              <Loader2 className="h-5 w-5 animate-spin" />
            </div>
          ) : sorted.length === 0 ? (
            <div className="flex flex-col items-center justify-center gap-3 py-20 text-center">
              <WifiOff className="h-8 w-8 text-zinc-700" />
              <div>
                <p className="text-[13px] text-zinc-600">{t('webClientsEmpty')}</p>
                <p className="text-[11px] text-zinc-700 mt-1">{t('webClientsEmptyHint')}</p>
              </div>
            </div>
          ) : (
            <div className="space-y-3">
              {sorted.map((c) => (
                <ClientCard
                  key={c.client_id}
                  client={c}
                  paneId={paneId}
                  onSent={onClose}
                />
              ))}
            </div>
          )}
        </div>
      </aside>
    </div>
  );
}
