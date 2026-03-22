import { sendCommandToTmux } from '../../services/mockApi';

const SKILLS = [
  { icon: '🔍', label: '代码审查', cmd: '/review' },
  { icon: '🧪', label: 'QA 测试', cmd: '/qa' },
  { icon: '🚀', label: '发布', cmd: '/ship' },
  { icon: '🔧', label: '调试', cmd: '/investigate' },
  { icon: '🧠', label: 'CEO 顾问', cmd: '/office-hours' },
  { icon: '📄', label: '更新文档', cmd: '/document-release' },
];

export default function SkillPanel({ paneId }: { paneId: string }) {
  const invoke = (cmd: string) => sendCommandToTmux(cmd, paneId);

  return (
    <div className="p-3 space-y-1">
      <div className="text-xs text-gray-400 font-medium mb-2 px-1">召唤员工</div>
      {SKILLS.map(({ icon, label, cmd }) => (
        <button
          key={cmd}
          onClick={() => invoke(cmd)}
          className="w-full flex items-center gap-2 px-3 py-2 rounded-lg text-sm text-left hover:bg-white/10 transition-colors"
        >
          <span>{icon}</span>
          <span className="text-gray-200">{label}</span>
          <span className="ml-auto text-xs text-gray-500">{cmd}</span>
        </button>
      ))}
    </div>
  );
}
