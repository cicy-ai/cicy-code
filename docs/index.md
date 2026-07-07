---
layout: page
title: cicy-code 文档
aside: false
editLink: false
head:
  - ['meta', { name: 'robots', content: 'noindex' }]
  - ['meta', { 'http-equiv': 'refresh', content: '0; url=/guide/getting-started' }]
---

<script setup>
import { onMounted } from 'vue'
import { useRouter } from 'vitepress'
const router = useRouter()
onMounted(() => { router.go('/guide/getting-started') })
</script>

正在进入 [快速开始](/guide/getting-started)…
