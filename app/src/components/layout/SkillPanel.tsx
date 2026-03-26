import { useEffect, useMemo, useState } from 'react';
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

interface Machine {
  id: number;
  machine_key: string;
  label: string;
  status: string;
  runtime_kind?: string;
  capabilities?: Record<string, any>;
}

export default function SkillPanel({ paneId }: { paneId: string }) {
  const [skills, setSkills] = useState<SkillDef[]>([]);
  const [bindings, setBindings] = useState<Binding[]>([]);
  const [machines, setMachines] = useState<Machine[]>([]);
  const [runningId, setRunningId] = useState<string>('');

  useEffect(() => {
    Promise.all([
      apiService.getSkills(),
      apiService.getAgentsByPane(paneId),
      apiService.getMachines(),
    ]).then(([sRes, bRes, mRes]) => {
      setSkills(Array.isArray(sRes.data?.skills) ? sRes.data.skills : []);
      setBindings(Array.isArray(bRes.data) ? bRes.data : []);
      setMachines(Array.isArray(mRes.data?.machines) ? mRes.data.machines : []);
    }).catch(() => {});
  }, [paneId]);

  const defaultTarget = useMemo(() => bindings[0]?.name || paneId, [bindings, paneId]);
  const defaultMachineId = useMemo(() => bindings[0]?.machine_id || machines[0]?.id || 0, [bindings, machines]);
  const defaultMachine = useMemo(() => machines.find(m => m.id === defaultMachineId), [machines, defaultMachineId]);
  const defaultMachineApiOnly = !!(defaultMachine && (defaultMachine.runtime_kind === 'cloudrun' || defaultMachine.capabilities?.supports_tmux === false));

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
    <div className="p-3 space-y-1" data-id="skill-panel-root">
      <div className="text-xs text-gray-400 font-medium mb-2 px-1" data-id="skill-panel-title">协作技能</div>
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
      <div className="pt-2 px-1 text-[11px] text-zinc-500" data-id="skill-panel-target-summary">
        target: {defaultTarget}
        {defaultMachineId ? ` · machine ${defaultMachine?.label || defaultMachineId}` : ''}
        {defaultMachine?.runtime_kind ? ` · ${defaultMachine.runtime_kind}` : ''}
        {defaultMachineApiOnly ? ' · API-only' : ''}
      </div>
    </div>
  );
}
