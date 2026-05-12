import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import apiService from '../../services/api';

interface SkillDef {
  id: string;
  label: string;
  description: string;
  icon: string;
  mode: string;
  default_target: string;
}

interface Binding {
  id: number;
  name: string;
  title?: string;
  machine_id?: number;
  machine_label?: string;
}

export default function SkillPanel({ paneId, bindings }: { paneId: string; bindings: Binding[] }) {
  const { t } = useTranslation('layout');
  const [skills, setSkills] = useState<SkillDef[]>([]);
  const [runningId, setRunningId] = useState<string>('');

  useEffect(() => {
    apiService.getSkills().then((sRes) => {
      setSkills(Array.isArray(sRes.data?.skills) ? sRes.data.skills : []);
    }).catch(() => {});
  }, []);

  const defaultTarget = useMemo(() => bindings[0]?.name || paneId, [bindings, paneId]);
  const defaultMachineId = useMemo(() => bindings[0]?.machine_id || 0, [bindings]);
  const defaultMachineLabel = useMemo(() => bindings[0]?.machine_label || '', [bindings]);

  const runSkill = async (skill: SkillDef) => {
    setRunningId(skill.id);
    try {
      await apiService.runSkill({
        skill_id: skill.id,
        current_pane_id: paneId,
        target_pane_id: defaultTarget,
        target_machine_id: defaultMachineId || undefined,
        created_by: 'skill-panel',
      });
    } catch {
    } finally {
      setRunningId('');
    }
  };

  return (
    <div className="h-full flex flex-col overflow-hidden bg-[#0A0A0A]" data-id="skill-panel-root">
      <div className="px-3 py-2 border-b border-[var(--vsc-border)] shrink-0" data-id="skill-panel-header">
        <div className="text-xs text-gray-400 font-medium" data-id="skill-panel-title">{t('skillPanelTitle')}</div>
      </div>
      <div className="flex-1 overflow-y-auto px-1.5 py-1.5" data-id="skill-panel-list">
        <div className="space-y-1">
          {skills.map((skill) => (
            <button
              data-id={`skill-panel-skill-${skill.id}`}
              key={skill.id}
              onClick={() => runSkill(skill)}
              disabled={runningId === skill.id}
              className="w-full flex items-center gap-2 px-3 py-2 rounded-lg text-sm text-left hover:bg-white/10 transition-colors disabled:opacity-50"
            >
              <span>{skill.icon}</span>
              <span className="text-gray-200">{skill.label}</span>
              <span className="ml-auto text-xs text-gray-500 truncate">{skill.mode}</span>
            </button>
          ))}
        </div>
      </div>
      <div className="px-3 py-2 border-t border-[var(--vsc-border)] shrink-0 text-[11px] text-zinc-500" data-id="skill-panel-target-summary">
        {t('skillPanelTarget', { target: defaultTarget })}
        {defaultMachineId ? t('skillPanelNode', { node: defaultMachineLabel || defaultMachineId }) : ''}
      </div>
    </div>
  );
}
