import { useEffect, useState } from 'react';
import { X, RefreshCw } from 'lucide-react';
import { fsApi, FsStatResponse, fsBasename } from './api';

interface Props {
  agentId: string;
  path: string;
  onClose: () => void;
}

function formatBytes(n: number): string {
  if (!Number.isFinite(n) || n < 0) return `${n}`;
  if (n < 1024) return `${n} B`;
  const units = ['KB', 'MB', 'GB', 'TB'];
  let v = n / 1024;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i += 1;
  }
  return `${v >= 100 ? Math.round(v) : v >= 10 ? v.toFixed(1) : v.toFixed(2)} ${units[i]}`;
}

function formatRelative(unix: number): string {
  const diff = Date.now() - unix * 1000;
  if (diff < 0) return '未来';
  if (diff < 60_000) return '刚刚';
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)} 分钟前`;
  if (diff < 86_400_000) return `${Math.floor(diff / 3_600_000)} 小时前`;
  if (diff < 30 * 86_400_000) return `${Math.floor(diff / 86_400_000)} 天前`;
  return new Date(unix * 1000).toLocaleDateString();
}

function formatAbs(unix: number): string {
  const d = new Date(unix * 1000);
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
}

export default function FileInfoModal({ agentId, path, onClose }: Props) {
  const [stat, setStat] = useState<FsStatResponse | null>(null);
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    setLoading(true);
    setError('');
    fsApi
      .stat(agentId, path)
      .then((s) => setStat(s))
      .catch((e) => setError((e as Error).message))
      .finally(() => setLoading(false));
  }, [agentId, path]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [onClose]);

  return (
    <div
      data-id="file-info-modal-backdrop"
      className="fixed inset-0 z-[2147483600] bg-black/40 backdrop-blur-sm flex items-center justify-center"
      onPointerDown={onClose}
    >
      <div
        data-id="file-info-modal"
        className="w-full max-w-md mx-4 rounded-lg border border-zinc-700 bg-zinc-900 shadow-2xl"
        onPointerDown={(e) => e.stopPropagation()}
      >
        <div className="flex items-center gap-2 px-4 py-2.5 border-b border-zinc-800">
          <span data-id="file-info-modal-title" className="font-mono text-sm text-zinc-100 truncate" title={path}>
            {fsBasename(path)}
          </span>
          <span className="flex-1" />
          <button data-id="file-info-modal-close" onClick={onClose} className="p-1 rounded hover:bg-zinc-800">
            <X className="w-4 h-4 text-zinc-500" />
          </button>
        </div>
        <div className="p-4 text-xs text-zinc-300 space-y-2">
          {loading && (
            <div className="flex items-center gap-2 text-zinc-500">
              <RefreshCw className="w-3.5 h-3.5 animate-spin" /> 读取中…
            </div>
          )}
          {error && <div className="text-red-400">{error}</div>}
          {stat && (
            <table className="w-full">
              <tbody>
                <Row k="路径" v={<span className="font-mono break-all">{stat.path || '/'}</span>} />
                <Row k="类型" v={stat.is_dir ? '目录' : 'file'} />
                <Row
                  k="大小"
                  v={
                    <>
                      {formatBytes(stat.size)} <span className="text-zinc-500">({stat.size} B)</span>
                    </>
                  }
                />
                <Row k="修改时间" v={`${formatAbs(stat.mtime)} (${formatRelative(stat.mtime)})`} />
                <Row k="权限" v={<span className="font-mono">{stat.mode}</span>} />
                {stat.mime && <Row k="MIME" v={<span className="font-mono">{stat.mime}</span>} />}
              </tbody>
            </table>
          )}
        </div>
      </div>
    </div>
  );
}

function Row({ k, v }: { k: string; v: React.ReactNode }) {
  return (
    <tr>
      <td className="py-1 pr-3 text-zinc-500 align-top w-20">{k}</td>
      <td className="py-1 text-zinc-100">{v}</td>
    </tr>
  );
}
