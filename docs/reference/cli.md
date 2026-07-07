---
title: CLI 命令
description: cicy-code 主命令与 skill 子命令速查。
---
# CLI 命令

## 主命令

```
cicy-code [options]
  --dev            开发模式          --preview   从磁盘 serve app/dist
  --public         绑 0.0.0.0        --port N    API 端口(默认 8008)
  --cft            Cloudflare quick tunnel
  --cft-token T    named tunnel(域名稳定)   --cft-host FQDN
  --lab            lab 模式          --audit     审计模式
  --version, -v    版本             --help, -h
```

## skill 子命令

```
cicy-code skill list | search <q> | info <name> | install <name>[@ver]
                    | update <name> | update --all | remove <name>
                    | installed | dev <path> | eject <name>
                    | registry <serve|publish|add|remove|sources>
```

## 其他子命令

```
cicy-code reseed-memory (--ids w-1,w-2 | --offline | --all) [--dry-run]
cicy-code audit ...        cicy-code mitm ...
```
