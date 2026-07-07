import { defineConfig } from 'vitepress'

const desc = '本地优先的多 agent 开发工作区 —— 把 Claude / Codex / OpenCode 编排成 tmux worker,收进同一个 React 工作区,通过 npm 分发单二进制。'

export default defineConfig({
  lang: 'zh-CN',
  title: 'cicy-code',
  description: desc,
  appearance: 'dark',        // 默认暗色
  cleanUrls: true,
  lastUpdated: true,
  metaChunk: true,
  sitemap: { hostname: 'https://docs.cicy-ai.com' },
  head: [
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
  themeConfig: {
    logo: '/favicon.svg',
    siteTitle: 'cicy-code',
    nav: [
      { text: '首页', link: 'https://cicy-ai.com' },
      { text: '快速开始', link: '/guide/getting-started' },
      { text: '下载', link: '/download' },
      { text: '文档', link: '/guide/introduction', activeMatch: '/guide/' },
    ],
    sidebar: [
      { text: '入门', collapsed: false, items: [
        { text: '介绍', link: '/guide/introduction' },
        { text: '快速开始', link: '/guide/getting-started' },
        { text: '下载', link: '/download' },
        { text: '构建与测试', link: '/guide/build-test' },
        { text: '发版', link: '/guide/release' },
      ]},
      { text: '核心', collapsed: false, items: [
        { text: '架构', link: '/guide/architecture' },
        { text: 'agent · pane · 记忆', link: '/guide/agents' },
        { text: 'skill 生态', link: '/guide/skills' },
      ]},
      { text: '参考', collapsed: false, items: [
        { text: '配置与路径', link: '/reference/config' },
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
