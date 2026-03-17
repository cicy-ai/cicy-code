import { useState, useEffect } from 'react';
import { X } from 'lucide-react';
import { SettingsView } from '../SettingsView';
import { EditPaneData } from '../EditPaneDialog';
import apiService from '../../services/api';

export default function SettingsFloat({ paneId, fullPaneId, onClose }: { paneId: string; fullPaneId: string; onClose: () => void }) {
  const [paneData, setPaneData] = useState<EditPaneData>({ target: fullPaneId, title: paneId });
  const [saving, setSaving] = useState(false);
  const [msg, setMsg] = useState('');
  useEffect(() => { apiService.getPane(fullPaneId).then(({ data }) => setPaneData(prev => ({ ...prev, ...data }))).catch(() => {}); }, [fullPaneId]);
  const save = async () => { setSaving(true); try { await apiService.updatePane(paneId, paneData); setMsg('Saved'); } catch { setMsg('Failed'); } finally { setSaving(false); setTimeout(() => setMsg(''), 1500); } };
  return (
    <div className="fixed inset-0 z-[99999] flex items-start justify-center pt-12" onClick={onClose}>
      <div className="w-[520px] max-h-[70vh] bg-[#1c1c1e]/95 backdrop-blur-2xl rounded-2xl shadow-2xl border border-white/[0.08] overflow-hidden" onClick={e => e.stopPropagation()}>
        <div className="flex items-center justify-between px-4 py-3 border-b border-white/[0.06]">
          <span className="text-base font-semibold text-white">Settings</span>
          <button onClick={onClose} className="p-1 rounded-full hover:bg-white/10"><X size={14} className="text-white/60" /></button>
        </div>
        <div className="p-3 overflow-y-auto max-h-[calc(70vh-48px)]">
          <SettingsView pane={paneData} onChange={setPaneData} onSave={save} isSaving={saving} />
          {msg && <div className="mt-2 text-center text-xs text-emerald-400">{msg}</div>}
        </div>
      </div>
    </div>
  );
}
