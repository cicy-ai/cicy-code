---
title: 写自己的 skill
description: scaffold → 开发 → 测试 → 发布(private / public PR)。
---
# 写自己的 skill

## 脚手架

```bash
cicy-skill-spec scaffold <name> --private   # 私有 → ~/cicy-ai/skills/private/<name>/
cicy-skill-spec scaffold <name>             # 公有 PR → ./<name>/
```

骨架:

```
<name>/
├── manifest.json     # name == 目录名;entry "bin/<name>";每次发布 bump version
├── SKILL.md          # frontmatter description 必须 == manifest.description
├── README.md
├── bin/<name>        # #!/usr/bin/env node,chmod +x,尽量零依赖
└── references/{help.md,tools.md}
```

## 私有分享

本机起私有 registry 发布,把地址 + token 给队友:

```bash
cicy-code skill registry serve --dir ~/cicy-registry-data --token <READ> --admin-token <ADMIN>
cicy-code skill registry publish ~/cicy-ai/skills/private/<name>
```

## 公有发布(PR + tag)

公有 skill 在 `cicy-skills` 仓库,**PR + tag 驱动**:

```bash
node tools/validate-skill.js skills/<name>
node tools/test-skill.js skills/<name>
# PR 合并后:
git tag <name>-v<version> && git push origin <name>-v<version>
```

::: warning 硬规则
`(name, version)` **不可变** —— 改了必须 bump;**绝不手跑** `tools/publish.js`(只走 tag 的 GitHub Action)。
:::
