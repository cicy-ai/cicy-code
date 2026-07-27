import { defineConfig } from 'vitepress'

const desc = '本地优先的多 agent 开发工作区 —— 把 Claude / Codex / OpenCode 编排成 tmux worker,收进同一个 React 工作区,通过 npm 分发单二进制。'

export default defineConfig({
  lang: 'zh-CN',
  title: 'cicy-code',
  description: desc,
  appearance: 'force-dark',  // 强制暗色,隐藏 light/dark 切换开关
  cleanUrls: true,
  lastUpdated: true,
  metaChunk: true,
  sitemap: { hostname: 'https://docs.cicy-ai.com' },
  head: [
    // Google Analytics 4 (gtag.js) — injected into every docs page's <head>
    ['script', { async: '', src: 'https://www.googletagmanager.com/gtag/js?id=G-DQJNV4PGMQ' }],
    ['script', {}, "window.dataLayer = window.dataLayer || [];\nfunction gtag(){dataLayer.push(arguments);}\ngtag('js', new Date());\ngtag('config', 'G-DQJNV4PGMQ');"],
    ['meta', { name: 'theme-color', content: '#0a0a0f' }],
    ['meta', { name: 'author', content: 'CiCy AI' }],
    ['meta', { property: 'og:type', content: 'website' }],
    ['meta', { property: 'og:site_name', content: 'cicy-code docs' }],
    ['meta', { property: 'og:title', content: 'cicy-code — 本地优先的多 agent 开发工作区' }],
    ['meta', { property: 'og:description', content: desc }],
    ['meta', { property: 'og:url', content: 'https://docs.cicy-ai.com/' }],
    ['meta', { name: 'twitter:card', content: 'summary_large_image' }],
    ['meta', { name: 'twitter:title', content: 'cicy-code — 文档' }],
    ['meta', { name: 'twitter:description', content: desc }],
    ['link', { rel: 'icon', type: 'image/svg+xml', href: '/favicon.svg' }],
  ],
  vite: {
    plugins: [
      {
        // Physically delete EVERY `@media (min-width: 1440px)` block from the
        // built CSS — VitePress uses them only to "center within
        // --vp-layout-max-width" on wide screens (the source of the navbar-title
        // jitter and the off-left layout). Removing them makes the <1440px
        // (left-aligned, fixed sidebar-width) layout apply at all widths.
        // Brace-matched so the whole block + its nested rules are stripped.
        name: 'strip-vp-1440-media',
        enforce: 'post',
        generateBundle(_options: unknown, bundle: Record<string, any>) {
          const strip1440 = (css: string): string => {
            const open = /@media[^{]*min-width:\s*1440px[^{]*\{/g;
            let out = '';
            let last = 0;
            let m: RegExpExecArray | null;
            while ((m = open.exec(css)) !== null) {
              out += css.slice(last, m.index);
              let depth = 1;
              let p = m.index + m[0].length;
              while (p < css.length && depth > 0) {
                const c = css[p++];
                if (c === '{') depth++;
                else if (c === '}') depth--;
              }
              last = p;
              open.lastIndex = p;
            }
            out += css.slice(last);
            return out;
          };
          for (const file of Object.values(bundle)) {
            if (file.type === 'asset' && file.fileName.endsWith('.css') && typeof file.source === 'string') {
              file.source = strip1440(file.source);
            }
          }
        },
      },
    ],
  },
  themeConfig: {
    logo: '/logo.svg',
    siteTitle: 'cicy-ai',
    nav: [
      { text: '首页', link: 'https://cicy-ai.com', target: '_self' },
      { text: '快速开始', link: '/guide/getting-started' },
      { text: '下载', link: '/guide/download' },
      { text: 'FAQ', link: '/faq/agent-login' },
      { text: '文档', link: '/guide/introduction', activeMatch: '/guide/' },
    ],
    sidebar: [
      { text: '开始', collapsed: false, items: [
        { text: '介绍', link: '/guide/introduction' },
        { text: '下载与安装', link: '/guide/download' },
        { text: '快速开始', link: '/guide/getting-started' },
        { text: '界面截图', link: '/guide/screenshots' },
      ]},
      { text: '常见问题', collapsed: false, items: [
        { text: 'Claude Code 官方登录', link: '/faq/claude-official-login' },
        { text: 'Claude Code 第三方中转 API', link: '/faq/claude-third-party-api' },
        { text: 'Codex 官方登录', link: '/faq/codex-official-login' },
        { text: 'Codex 第三方中转 API', link: '/faq/codex-third-party-api' },
      ]},
      { text: '核心概念', collapsed: false, items: [
        { text: 'Agent 与 Pane', link: '/concepts/agent-pane' },
        { text: 'cicy Agent', link: '/concepts/cicy-agent' },
        { text: '记忆与模板', link: '/concepts/memory' },
        { text: '团队与协作', link: '/concepts/teams' },
        { text: 'Fork 分身', link: '/concepts/fork' },
        { text: 'Skill 能力', link: '/concepts/skill' },
      ]},
      { text: '指南', collapsed: false, items: [
        { text: '创建与管理 agent', link: '/guides/create-agent' },
        { text: '项目与角色模板', link: '/guides/templates' },
        { text: '派活与任务', link: '/guides/tasks' },
        { text: '跨 agent 协作', link: '/guides/collaboration' },
        { text: '装用 skill', link: '/guides/skills-use' },
        { text: '浏览器 / 桌面控制', link: '/guides/browser-desktop' },
        { text: '定制记忆', link: '/guides/memory-customize' },
        { text: '团队知识库', link: '/guides/knowledge' },
      ]},
            { text: '进阶', collapsed: false, items: [
        { text: '本地 AI 网关', link: '/advanced/gateway' },
        { text: '网关 vs 非网关启动', link: '/advanced/gateway-modes' },
        { text: 'MITM 审计代理', link: '/advanced/mitm' },
        { text: '审计策略', link: '/advanced/audit' },
      ]},
      { text: 'Skill 生态', collapsed: true, items: [
        { text: '概览与安装', link: '/skills/overview' },
        { text: '三类 skill', link: '/skills/kinds' },
        { text: '写自己的 skill', link: '/skills/authoring' },
      ]},
      { text: '开发', collapsed: true, items: [
        { text: '仓库结构', link: '/develop/repo-structure' },
        { text: '本地开发', link: '/develop/local-dev' },
        { text: '构建与测试', link: '/develop/build-test' },
        { text: '发版', link: '/develop/release' },
        { text: '架构', link: '/develop/architecture' },
      ]},
      { text: '部署', collapsed: true, items: [
        { text: '单机 / 本地', link: '/deploy/single' },
        { text: 'Docker / runtime', link: '/deploy/docker' },
        { text: '自托管隧道 (cicy-hub)', link: '/deploy/tunnel' },
        { text: '云端 (cicy-cloud)', link: '/deploy/cloud' },
      ]},
      { text: '参考', collapsed: true, items: [
        { text: '配置与路径', link: '/reference/config' },
        { text: 'CLI 命令', link: '/reference/cli' },
        { text: '环境变量', link: '/reference/env' },
      ]},
    ],
    outline: { level: [2, 3], label: '本页导航' },
    search: { provider: 'local' },
    socialLinks: [{ icon: 'github', link: 'https://github.com/cicy-ai/cicy-code' }],
    docFooter: { prev: '上一页', next: '下一页' },
    darkModeSwitchLabel: '主题',
    sidebarMenuLabel: '菜单',
    returnToTopLabel: '回到顶部',
    lastUpdatedText: '最后更新',
    footer: {
      message: 'part of <a href="https://cicy-ai.com">CiCy AI</a> · Apache-2.0',
      copyright: '© 2026 CiCy AI',
    },
  },
})
