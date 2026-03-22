import { sendCommandToTmux } from '../../services/mockApi';

const SKILLS = [
  { icon: '🔍', label: '代码审查', cmd: 'Use the /review skill from gstack to do a pre-landing code review on the current branch' },
  { icon: '🧪', label: 'QA 测试', cmd: 'Use the /qa skill from gstack to QA test the app' },
  { icon: '🚀', label: '发布', cmd: 'Use the /ship skill from gstack to run tests, review, and create a PR' },
  { icon: '🔧', label: '调试', cmd: 'Use the /investigate skill from gstack to systematically debug the current issue' },
  { icon: '🧠', label: 'CEO 顾问', cmd: 'Use the /office-hours skill from gstack' },
  { icon: '📄', label: '更新文档', cmd: 'Use the /document-release skill from gstack to update all docs after shipping' },
];

export default function SkillPanel({ paneId }: { paneId: string }) {
  const invoke = (cmd: string) => {
    window.dispatchEvent(new CustomEvent('chat-q-sent', { detail: { pane: paneId, q: cmd } }));
    sendCommandToTmux(cmd, paneId);
  };

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
