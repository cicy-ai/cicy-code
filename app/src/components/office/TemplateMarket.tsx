import { useMemo, useState } from 'react';
import {
  X, Search, Check, Plus, LayoutGrid, Code2, PenTool, BarChart3, Megaphone,
  ShoppingCart, Palette, Briefcase, Headphones, Store, Users, Sparkles,
} from 'lucide-react';

/*
 * TemplateMarket —「Agent & 团队 市场」全屏大屏（data-id="template-market"）。
 * 两个视图：团队（一键组建整支班子）/ 单个 Agent（各行各业角色模版）。
 * 纯 UI 原型；onPick 加单个 agent，onPickTeam 加整支团队，由 Office 在画布上创建。
 */

export interface MarketTmpl { key: string; cat: string; name: string; role: string; emoji: string; accent: string; model: string; desc: string; tags: string[] }
export interface TeamMember { name: string; role: string; emoji: string; accent: string; model: string }
export interface TeamTmpl { key: string; cat: string; name: string; emoji: string; accent: string; desc: string; tags: string[]; members: TeamMember[] }

const AGENT_CATS: { key: string; name: string; icon: any }[] = [
  { key: 'all', name: '全部', icon: LayoutGrid },
  { key: 'dev', name: '软件研发', icon: Code2 },
  { key: 'content', name: '内容创作', icon: PenTool },
  { key: 'data', name: '数据分析', icon: BarChart3 },
  { key: 'mkt', name: '市场营销', icon: Megaphone },
  { key: 'ecom', name: '电商运营', icon: ShoppingCart },
  { key: 'design', name: '设计创意', icon: Palette },
  { key: 'biz', name: '商业职能', icon: Briefcase },
  { key: 'cs', name: '客服支持', icon: Headphones },
];
const TEAM_CATS: { key: string; name: string; icon: any }[] = [
  { key: 'all', name: '全部', icon: LayoutGrid },
  { key: 'creative', name: '创意内容', icon: PenTool },
  { key: 'software', name: '软件研发', icon: Code2 },
  { key: 'marketing', name: '营销增长', icon: Megaphone },
  { key: 'ecom', name: '电商运营', icon: ShoppingCart },
  { key: 'data', name: '数据智能', icon: BarChart3 },
];

const GRAD: Record<string, string> = {
  sky: 'from-sky-500/45 to-sky-700/15', violet: 'from-violet-500/45 to-violet-700/15',
  emerald: 'from-emerald-500/45 to-emerald-700/15', amber: 'from-amber-500/45 to-amber-700/15',
  rose: 'from-rose-500/45 to-rose-700/15',
};
const COVER: Record<string, string> = {
  sky: 'from-sky-500/30 via-sky-600/10 to-transparent', violet: 'from-violet-500/30 via-violet-600/10 to-transparent',
  emerald: 'from-emerald-500/30 via-emerald-600/10 to-transparent', amber: 'from-amber-500/30 via-amber-600/10 to-transparent',
  rose: 'from-rose-500/30 via-rose-600/10 to-transparent',
};

const T = (key: string, cat: string, name: string, role: string, emoji: string, accent: string, model: string, desc: string, tags: string[]): MarketTmpl =>
  ({ key, cat, name, role, emoji, accent, model, desc, tags });
const M = (name: string, role: string, emoji: string, accent: string, model = 'deepseek-v4-pro'): TeamMember => ({ name, role, emoji, accent, model });

const MARKET: MarketTmpl[] = [
  T('arch', 'dev', '架构师', 'dev-senior', '🏛️', 'sky', 'deepseek-v4-pro', '拆解需求、定义接口与目录结构、做关键技术选型并把关 review。', ['架构', '选型', 'review']),
  T('fe', 'dev', '前端工程师', 'dev-junior', '🎨', 'sky', 'deepseek-v4-pro', '实现页面与组件，关注交互细节、可访问性与性能。', ['React', 'UI', 'a11y']),
  T('be', 'dev', '后端工程师', 'dev', '🛠️', 'sky', 'deepseek-v4-pro', 'API 设计、数据库与服务端逻辑，保证一致性与性能。', ['API', 'DB', '服务']),
  T('qa', 'dev', '测试工程师', 'qa', '🧪', 'sky', 'gpt-5.5', '编写/执行用例、跑回归、核对验收标准并出 PASS/FAIL。', ['测试', '回归', '验收']),
  T('ops', 'dev', 'DevOps', 'ops', '🚀', 'sky', 'deepseek-v4-pro', '构建、部署、发布与回滚，线上监控与告警。', ['CI/CD', '部署', '监控']),
  T('sec', 'dev', '安全工程师', 'reviewer', '🛡️', 'sky', 'claude-haiku-4-5', '密钥/注入/权限/依赖风险审查，出安全结论。', ['安全', '审计']),
  T('copy', 'content', '文案策划', 'writer', '✍️', 'violet', 'deepseek-v4-pro', '营销文案、卖点提炼、不同渠道的语气适配。', ['文案', '营销']),
  T('script', 'content', '短视频编剧', 'scriptwriter', '🎬', 'violet', 'gpt-5.5', '脚本、分镜、开头钩子与节奏设计。', ['脚本', '短视频']),
  T('seo', 'content', 'SEO 编辑', 'seo', '🔍', 'violet', 'deepseek-v4-pro', '关键词与长尾布局、内容结构优化。', ['SEO', '内容']),
  T('trans', 'content', '翻译/本地化', 'translator', '🌐', 'violet', 'deepseek-v4-pro', '多语翻译与本地化，保留语气与术语一致。', ['翻译', 'i18n']),
  T('media', 'content', '新媒体运营', 'editor', '📣', 'violet', 'deepseek-v4-pro', '选题、排版、推送与互动追踪。', ['新媒体', '排版']),
  T('analyst', 'data', '数据分析师', 'analyst', '📊', 'emerald', 'gpt-5.5', '指标体系、报表与可执行洞察。', ['SQL', '报表', '洞察']),
  T('bi', 'data', 'BI 工程师', 'bi', '📈', 'emerald', 'deepseek-v4-pro', '看板搭建与数据可视化。', ['BI', '可视化']),
  T('de', 'data', '数据工程', 'data-eng', '🗄️', 'emerald', 'deepseek-v4-pro', '数据管道、清洗与数仓建模。', ['ETL', '数仓']),
  T('ml', 'data', 'ML 工程师', 'ml', '🤖', 'emerald', 'gpt-5.5', '特征、建模、训练与评估。', ['ML', '建模']),
  T('growth', 'mkt', '增长黑客', 'growth', '📈', 'amber', 'gpt-5.5', '增长实验设计、漏斗分析与迭代。', ['增长', '实验']),
  T('ads', 'mkt', '投放优化', 'ads', '🎯', 'amber', 'deepseek-v4-pro', '广告投放策略与 ROI 优化。', ['投放', 'ROI']),
  T('social', 'mkt', '社媒运营', 'social', '💬', 'amber', 'deepseek-v4-pro', '内容日历、互动与社群运营。', ['社媒', '运营']),
  T('brand', 'mkt', '品牌策划', 'brand', '✨', 'amber', 'deepseek-v4-pro', '品牌定位、叙事与视觉调性。', ['品牌', '叙事']),
  T('shop', 'ecom', '店铺运营', 'ecom', '🛒', 'rose', 'deepseek-v4-pro', '上架、活动排期与数据复盘。', ['电商', '运营']),
  T('sel', 'ecom', '选品分析', 'selection', '🔎', 'rose', 'gpt-5.5', '选品、趋势与利润测算。', ['选品', '趋势']),
  T('detail', 'ecom', '详情页文案', 'writer', '🏷️', 'rose', 'deepseek-v4-pro', '详情页结构与卖点转化。', ['详情页', '转化']),
  T('after', 'ecom', '售后助理', 'cs', '📦', 'rose', 'deepseek-v4-pro', '退换货、纠纷与售后流程。', ['售后', '工单']),
  T('ui', 'design', 'UI 设计', 'ui-design', '🖌️', 'violet', 'deepseek-v4-pro', '界面设计与组件规范。', ['UI', '规范']),
  T('graphic', 'design', '平面设计', 'graphic', '🎨', 'violet', 'deepseek-v4-pro', '海报、物料与视觉输出。', ['平面', '物料']),
  T('illus', 'design', '插画师', 'illustrator', '🖼️', 'violet', 'deepseek-v4-pro', '插画风格与配图。', ['插画', '风格']),
  T('pm', 'biz', '产品经理', 'pm', '📋', 'sky', 'gpt-5.5', '需求梳理、PRD 与优先级排布。', ['PRD', '需求']),
  T('pmo', 'biz', '项目经理', 'pmo', '🗂️', 'sky', 'deepseek-v4-pro', '排期、风险与跨角色协调。', ['排期', '风险']),
  T('fin', 'biz', '财务分析', 'finance', '💰', 'sky', 'gpt-5.5', '预算、报表与商业测算。', ['财务', '测算']),
  T('legal', 'biz', '法务顾问', 'legal', '⚖️', 'sky', 'claude-haiku-4-5', '合同审阅、合规与风险提示。', ['合同', '合规']),
  T('hr', 'biz', 'HR 招聘', 'hr', '🧑‍💼', 'sky', 'deepseek-v4-pro', 'JD 撰写、简历筛选与面评。', ['招聘', 'JD']),
  T('support', 'cs', '智能客服', 'support', '🎧', 'emerald', 'deepseek-v4-pro', '多轮答疑、FAQ 与转人工。', ['客服', 'FAQ']),
  T('triage', 'cs', '工单分类', 'triage', '🧭', 'emerald', 'gpt-5.5', '工单分类、优先级与路由。', ['工单', '路由']),
  T('kb', 'cs', '知识库维护', 'kb', '📚', 'emerald', 'deepseek-v4-pro', '知识沉淀、更新与检索。', ['KB', '检索']),
];

const TEAMS: TeamTmpl[] = [
  {
    key: 'comic', cat: 'creative', name: '漫剧创作团队', emoji: '🎬', accent: 'rose',
    desc: '从剧本到成片的一条龙漫剧班子：编剧立骨、分镜定节奏、作画上色出成片。',
    tags: ['漫剧', '剧本→成片', '6 工种'],
    members: [
      M('导演/监制', 'director', '🎬', 'rose'), M('编剧', 'screenwriter', '📝', 'rose'),
      M('分镜师', 'storyboard', '🎞️', 'rose'), M('角色设定', 'char-design', '🧝', 'rose'),
      M('作画', 'artist', '🖌️', 'rose'), M('上色/合成', 'colorist', '🎨', 'rose'),
    ],
  },
  {
    key: 'software', cat: 'software', name: '软件开发团队', emoji: '💻', accent: 'sky',
    desc: '全生命周期研发班子：架构拆解、前后端实现、QA 验收、运维上线、安全把关。',
    tags: ['全栈', '研发→上线', '6 工种'],
    members: [
      M('架构师', 'dev-senior', '🏛️', 'sky'), M('前端', 'dev-junior', '🎨', 'sky'),
      M('后端', 'dev', '🛠️', 'sky'), M('测试', 'qa', '🧪', 'sky', 'gpt-5.5'),
      M('DevOps', 'ops', '🚀', 'sky'), M('安全', 'reviewer', '🛡️', 'sky', 'claude-haiku-4-5'),
    ],
  },
  {
    key: 'marketing', cat: 'marketing', name: '内容营销团队', emoji: '📣', accent: 'amber',
    desc: '从内容到增长：文案产出、SEO 与社媒分发、配图设计、数据复盘闭环。',
    tags: ['内容', '增长', '5 工种'],
    members: [
      M('文案策划', 'writer', '✍️', 'amber'), M('SEO 编辑', 'seo', '🔍', 'amber'),
      M('社媒运营', 'social', '💬', 'amber'), M('平面设计', 'graphic', '🎨', 'amber'),
      M('数据分析', 'analyst', '📊', 'amber', 'gpt-5.5'),
    ],
  },
  {
    key: 'ecom', cat: 'ecom', name: '电商运营团队', emoji: '🛒', accent: 'violet',
    desc: '开店到爆单：选品、店铺运营、详情页转化、投放拉新、客服售后。',
    tags: ['电商', '选品→转化', '5 工种'],
    members: [
      M('店铺运营', 'ecom', '🛒', 'violet'), M('选品分析', 'selection', '🔎', 'violet', 'gpt-5.5'),
      M('详情页文案', 'writer', '🏷️', 'violet'), M('投放优化', 'ads', '🎯', 'violet'),
      M('客服', 'cs', '🎧', 'violet'),
    ],
  },
  {
    key: 'data', cat: 'data', name: '数据团队', emoji: '📊', accent: 'emerald',
    desc: '数据全链路：管道与数仓、指标与看板、建模与预测。',
    tags: ['数据', 'ETL→洞察', '4 工种'],
    members: [
      M('数据分析', 'analyst', '📊', 'emerald', 'gpt-5.5'), M('BI 工程', 'bi', '📈', 'emerald'),
      M('数据工程', 'data-eng', '🗄️', 'emerald'), M('ML 工程', 'ml', '🤖', 'emerald', 'gpt-5.5'),
    ],
  },
  {
    key: 'shortvideo', cat: 'creative', name: '短视频团队', emoji: '🎥', accent: 'rose',
    desc: '爆款短视频流水线：选题编导、脚本、剪辑文案、账号运营。',
    tags: ['短视频', '选题→投放', '4 工种'],
    members: [
      M('编导', 'director', '🎬', 'rose'), M('编剧', 'scriptwriter', '📝', 'rose'),
      M('剪辑文案', 'editor', '✂️', 'rose'), M('账号运营', 'social', '💬', 'rose'),
    ],
  },
  {
    key: 'startup', cat: 'software', name: '产品初创团队', emoji: '🚀', accent: 'sky',
    desc: '0→1 精简班子：产品定方向、架构+全栈快速实现、设计与增长并进。',
    tags: ['0→1', 'MVP', '5 工种'],
    members: [
      M('产品经理', 'pm', '📋', 'sky', 'gpt-5.5'), M('架构师', 'dev-senior', '🏛️', 'sky'),
      M('全栈', 'dev', '🛠️', 'sky'), M('设计', 'ui-design', '🖌️', 'sky'),
      M('增长', 'growth', '📈', 'sky', 'gpt-5.5'),
    ],
  },
];

export default function TemplateMarket({ open, onClose, onPick, onPickTeam }: {
  open: boolean; onClose: () => void; onPick: (t: MarketTmpl) => void; onPickTeam: (t: TeamTmpl) => void;
}) {
  const [view, setView] = useState<'team' | 'agent'>('team');
  const [cat, setCat] = useState('all');
  const [q, setQ] = useState('');
  const [added, setAdded] = useState<Set<string>>(new Set());
  const [builtTeams, setBuiltTeams] = useState<Set<string>>(new Set());

  const cats = view === 'team' ? TEAM_CATS : AGENT_CATS;
  const counts = useMemo(() => {
    const items = view === 'team' ? TEAMS : MARKET;
    const m: Record<string, number> = { all: items.length };
    for (const t of items) m[t.cat] = (m[t.cat] || 0) + 1;
    return m;
  }, [view]);

  const agents = useMemo(() => {
    const kw = q.trim().toLowerCase();
    return MARKET.filter((t) => (cat === 'all' || t.cat === cat) && (!kw || t.name.toLowerCase().includes(kw) || t.role.toLowerCase().includes(kw) || t.desc.toLowerCase().includes(kw) || t.tags.some((x) => x.toLowerCase().includes(kw))));
  }, [cat, q]);
  const teams = useMemo(() => {
    const kw = q.trim().toLowerCase();
    return TEAMS.filter((t) => (cat === 'all' || t.cat === cat) && (!kw || t.name.toLowerCase().includes(kw) || t.desc.toLowerCase().includes(kw) || t.tags.some((x) => x.toLowerCase().includes(kw)) || t.members.some((mm) => mm.name.toLowerCase().includes(kw))));
  }, [cat, q]);

  if (!open) return null;
  const switchView = (v: 'team' | 'agent') => { setView(v); setCat('all'); setQ(''); };
  const pickAgent = (t: MarketTmpl) => { onPick(t); setAdded((s) => new Set(s).add(t.key)); };
  const buildTeam = (t: TeamTmpl) => { onPickTeam(t); setBuiltTeams((s) => new Set(s).add(t.key)); };

  return (
    <div data-id="template-market" className="absolute inset-0 z-[200] flex items-center justify-center bg-black/70 p-6 backdrop-blur-sm" onPointerDown={onClose}>
      <div className="relative flex h-[88vh] w-[1180px] max-w-[96vw] overflow-hidden rounded-2xl border border-white/10 bg-[#0b0b0e] shadow-2xl" onPointerDown={(e) => e.stopPropagation()}>
        {/* 左：分类 */}
        <aside data-id="market-cats" className="flex w-52 shrink-0 flex-col border-r border-white/[0.06] bg-[#0d0d10]">
          <div className="flex items-center gap-2 px-4 py-4 text-zinc-100"><Store className="h-5 w-5 text-sky-400" /><span className="text-[14px] font-semibold">市场</span></div>
          <div className="flex-1 space-y-0.5 overflow-auto px-2 pb-3">
            {cats.map((c) => {
              const Icon = c.icon; const on = cat === c.key;
              return (
                <button key={c.key} data-id={`market-cat-${c.key}`} onClick={() => setCat(c.key)} className={`flex w-full items-center gap-2.5 rounded-lg px-2.5 py-2 text-left text-[13px] transition-colors ${on ? 'bg-white/[0.08] text-zinc-100' : 'text-zinc-400 hover:bg-white/[0.04] hover:text-zinc-200'}`}>
                  <Icon className={`h-4 w-4 ${on ? 'text-sky-400' : 'text-zinc-500'}`} /><span className="flex-1">{c.name}</span><span className="text-[11px] text-zinc-600">{counts[c.key] || 0}</span>
                </button>
              );
            })}
          </div>
        </aside>

        {/* 右 */}
        <div className="flex min-w-0 flex-1 flex-col">
          <header className="flex shrink-0 items-center gap-3 border-b border-white/[0.06] px-6 py-4">
            <div className="min-w-0">
              <div className="text-[17px] font-semibold text-zinc-100">AI 团队 & Agent 市场</div>
              <div className="text-[12px] text-zinc-500">{view === 'team' ? '一键组建整支团队，立刻开工' : '各行各业的预置 agent，按需添加'}</div>
            </div>
            {/* 视图切换 */}
            <div data-id="market-view" className="ml-3 inline-flex items-center gap-0.5 rounded-lg bg-white/[0.05] p-0.5 text-[12.5px]">
              <button data-id="market-view-team" onClick={() => switchView('team')} className={`inline-flex items-center gap-1.5 rounded-md px-3 py-1.5 transition-colors ${view === 'team' ? 'bg-white/10 text-zinc-100' : 'text-zinc-500 hover:text-zinc-300'}`}><Users className="h-3.5 w-3.5" /> 团队</button>
              <button data-id="market-view-agent" onClick={() => switchView('agent')} className={`inline-flex items-center gap-1.5 rounded-md px-3 py-1.5 transition-colors ${view === 'agent' ? 'bg-white/10 text-zinc-100' : 'text-zinc-500 hover:text-zinc-300'}`}><Sparkles className="h-3.5 w-3.5" /> 单个 Agent</button>
            </div>
            <div className="relative ml-auto w-64 max-w-[34%]">
              <Search className="absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-zinc-500" />
              <input data-id="market-search" value={q} onChange={(e) => setQ(e.target.value)} placeholder={view === 'team' ? '搜索团队 / 工种…' : '搜索角色 / 技能…'} className="w-full rounded-lg border border-white/10 bg-[#141418] py-2 pl-8 pr-3 text-[13px] text-zinc-200 outline-none placeholder:text-zinc-600 focus:border-white/25" />
            </div>
            <button data-id="market-close" onClick={onClose} className="grid h-8 w-8 place-items-center rounded-lg text-zinc-500 hover:bg-white/10 hover:text-zinc-200"><X className="h-4 w-4" /></button>
          </header>

          {view === 'team' ? (
            <div data-id="market-team-grid" className="flex-1 overflow-auto px-6 py-5">
              {teams.length === 0 ? <div className="grid h-full place-items-center text-[13px] text-zinc-600">没有匹配的团队</div> : (
                <div className="grid gap-4" style={{ gridTemplateColumns: 'repeat(auto-fill, minmax(320px, 1fr))' }}>
                  {teams.map((t) => {
                    const built = builtTeams.has(t.key);
                    return (
                      <div key={t.key} data-id={`market-team-${t.key}`} className="group flex flex-col overflow-hidden rounded-2xl border border-white/[0.08] bg-[#101014] transition-colors hover:border-white/20">
                        <div className={`relative flex h-20 items-center gap-3 bg-gradient-to-r ${COVER[t.accent] ?? COVER.sky} px-4`}>
                          <span className={`grid h-12 w-12 place-items-center rounded-xl bg-gradient-to-br ${GRAD[t.accent] ?? GRAD.sky} text-2xl ring-1 ring-white/15`}>{t.emoji}</span>
                          <div className="min-w-0"><div className="truncate text-[15px] font-semibold text-zinc-50">{t.name}</div><div className="text-[11px] text-zinc-400">{t.members.length} 名成员</div></div>
                        </div>
                        <div className="flex flex-1 flex-col px-4 pb-3.5 pt-3">
                          <p className="mb-3 line-clamp-2 min-h-[34px] text-[12px] leading-relaxed text-zinc-400">{t.desc}</p>
                          <div className="mb-3 flex items-center">
                            <div className="flex">
                              {t.members.slice(0, 6).map((mm, i) => (
                                <span key={i} title={`${mm.name} · ${mm.role}`} className={`grid h-8 w-8 place-items-center rounded-full bg-gradient-to-br ${GRAD[mm.accent] ?? GRAD.sky} text-sm ring-2 ring-[#101014] ${i > 0 ? '-ml-2' : ''}`}>{mm.emoji}</span>
                              ))}
                            </div>
                            <div className="ml-2 flex flex-wrap gap-1">{t.tags.map((tag) => <span key={tag} className="rounded-md bg-white/[0.05] px-1.5 py-0.5 text-[10.5px] text-zinc-500">{tag}</span>)}</div>
                          </div>
                          <button data-id={`market-build-${t.key}`} onClick={() => buildTeam(t)} className={`mt-auto inline-flex w-full items-center justify-center gap-1.5 rounded-xl py-2 text-[13px] font-medium transition-colors ${built ? 'bg-emerald-500/15 text-emerald-300' : 'bg-sky-500 text-white hover:bg-sky-400'}`}>
                            {built ? <><Check className="h-4 w-4" /> 已组建（可再建）</> : <><Users className="h-4 w-4" /> 一键组建团队</>}
                          </button>
                        </div>
                      </div>
                    );
                  })}
                </div>
              )}
            </div>
          ) : (
            <div data-id="market-grid" className="flex-1 overflow-auto px-6 py-5">
              {agents.length === 0 ? <div className="grid h-full place-items-center text-[13px] text-zinc-600">没有匹配的模版</div> : (
                <div className="grid gap-3.5" style={{ gridTemplateColumns: 'repeat(auto-fill, minmax(248px, 1fr))' }}>
                  {agents.map((t) => {
                    const isAdded = added.has(t.key);
                    return (
                      <div key={t.key} data-id={`market-card-${t.key}`} className="group flex flex-col rounded-2xl border border-white/[0.07] bg-[#101014] p-3.5 transition-colors hover:border-white/15">
                        <div className="mb-2 flex items-center gap-2.5">
                          <span className={`grid h-11 w-11 shrink-0 place-items-center rounded-xl bg-gradient-to-br ${GRAD[t.accent] ?? GRAD.sky} text-xl ring-1 ring-white/10`}>{t.emoji}</span>
                          <div className="min-w-0"><div className="truncate text-[14px] font-semibold text-zinc-100">{t.name}</div><div className="truncate font-mono text-[11px] text-zinc-500">{t.role}</div></div>
                        </div>
                        <p className="mb-2.5 line-clamp-2 min-h-[34px] text-[12px] leading-relaxed text-zinc-400">{t.desc}</p>
                        <div className="mb-3 flex flex-wrap gap-1">{t.tags.map((tag) => <span key={tag} className="rounded-md bg-white/[0.05] px-1.5 py-0.5 text-[10.5px] text-zinc-500">{tag}</span>)}</div>
                        <div className="mt-auto flex items-center gap-2">
                          <span className="truncate rounded bg-white/[0.05] px-1.5 py-0.5 font-mono text-[10px] text-zinc-500" title={`默认模型 ${t.model}`}>{t.model}</span>
                          <button data-id={`market-add-${t.key}`} onClick={() => pickAgent(t)} className={`ml-auto inline-flex items-center gap-1 rounded-lg px-2.5 py-1.5 text-[12px] font-medium transition-colors ${isAdded ? 'bg-emerald-500/15 text-emerald-300' : 'bg-sky-500 text-white hover:bg-sky-400'}`}>
                            {isAdded ? <><Check className="h-3.5 w-3.5" /> 已添加</> : <><Plus className="h-3.5 w-3.5" /> 添加</>}
                          </button>
                        </div>
                      </div>
                    );
                  })}
                </div>
              )}
            </div>
          )}

          <footer className="flex shrink-0 items-center justify-between border-t border-white/[0.06] px-6 py-3 text-[12px] text-zinc-500">
            <span>{view === 'team' ? `${TEAMS.length} 支团队 · 已组建 ${builtTeams.size}` : `${MARKET.length} 个模版 · 已添加 ${added.size}`}</span>
            <button data-id="market-done" onClick={onClose} className="rounded-lg bg-white/[0.06] px-4 py-1.5 text-zinc-200 hover:bg-white/[0.12]">完成</button>
          </footer>
        </div>
      </div>
    </div>
  );
}
