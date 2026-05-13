#!/usr/bin/env python3
"""
Cloudflare Tunnel 管理脚本
自动添加/删除/列出路由 + DNS CNAME 记录

用法:
  python3 cf-tunnel.py list                    # 列出所有路由 (默认 prod)
  python3 cf-tunnel.py add 8101                # 添加路由
  python3 cf-tunnel.py add 8101 8102 8103      # 批量添加
  python3 cf-tunnel.py del 8101                # 删除路由
  CF_ENV=dev python3 cf-tunnel.py list         # 临时切到 dev 环境
"""

import os, sys, json, socket
import urllib.request, urllib.error

# 从 ~/cicy-ai/global.json 读取配置
def load_config():
    global_path = os.path.expanduser("~/cicy-ai/global.json")
    with open(global_path) as f:
        g = json.load(f)
    env = os.getenv("CF_ENV", "prod")
    cf = g.get("cf", {}).get(env, {})
    if not cf:
        print(f"❌ 未找到 cf.{env} 配置")
        sys.exit(1)
    return cf

CF = load_config()
CF_TOKEN = CF.get("api_token", "")
CF_ACCOUNT = CF.get("account_id", "")
TUNNEL_ID = CF.get("tunnel_id", "")
TUNNEL_CNAME = f"{TUNNEL_ID}.cfargotunnel.com"
DOMAIN = CF.get("domain", "")
ZONE_ID = CF.get("zone_id", "")


def api(method, path, data=None):
    url = f"https://api.cloudflare.com/client/v4/{path}"
    body = json.dumps(data).encode() if data else None
    req = urllib.request.Request(url, data=body, method=method)
    req.add_header("Authorization", f"Bearer {CF_TOKEN}")
    req.add_header("Content-Type", "application/json")
    try:
        with urllib.request.urlopen(req) as resp:
            return json.loads(resp.read())
    except urllib.error.HTTPError as e:
        return json.loads(e.read())


def port_listening(port):
    """检查本地端口是否在监听"""
    s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    s.settimeout(1)
    try:
        s.connect(("127.0.0.1", port))
        s.close()
        return True
    except:
        return False


def hostname_for(port):
    return f"g-{port}.{DOMAIN}"


# --- Tunnel ingress ---

def get_config():
    r = api("GET", f"accounts/{CF_ACCOUNT}/cfd_tunnel/{TUNNEL_ID}/configurations")
    if not r.get("success"):
        print(f"❌ 获取配置失败: {r.get('errors')}")
        sys.exit(1)
    return r["result"]["config"]


def put_config(config):
    r = api("PUT", f"accounts/{CF_ACCOUNT}/cfd_tunnel/{TUNNEL_ID}/configurations",
            {"config": config})
    if not r.get("success"):
        print(f"❌ 更新配置失败: {r.get('errors')}")
        sys.exit(1)
    return r


# --- DNS CNAME ---

def dns_list():
    """获取所有 DNS 记录"""
    records = []
    page = 1
    while True:
        r = api("GET", f"zones/{ZONE_ID}/dns_records?type=CNAME&per_page=100&page={page}")
        if not r.get("success"):
            break
        records.extend(r["result"])
        if len(r["result"]) < 100:
            break
        page += 1
    return records


def dns_add(hostname):
    """添加 CNAME 记录指向 tunnel"""
    r = api("POST", f"zones/{ZONE_ID}/dns_records", {
        "type": "CNAME",
        "name": hostname,
        "content": TUNNEL_CNAME,
        "proxied": True,
        "ttl": 1,
    })
    if r.get("success"):
        return True
    # 已存在也算成功
    errors = r.get("errors", [])
    if any("already" in str(e).lower() for e in errors):
        return True
    print(f"  ⚠️  DNS 创建失败 {hostname}: {errors}")
    return False


def dns_del(hostname):
    """删除 CNAME 记录"""
    records = dns_list()
    for rec in records:
        if rec["name"] == hostname:
            r = api("DELETE", f"zones/{ZONE_ID}/dns_records/{rec['id']}")
            return r.get("success", False)
    return False


# --- Commands ---

def cmd_list():
    config = get_config()
    ingress = config.get("ingress", [])
    print(f"📡 Tunnel 路由 ({len(ingress) - 1} 条):\n")
    for r in ingress:
        h = r.get("hostname", "(catch-all)")
        s = r.get("service", "?")
        # 检查本地端口
        port_str = ""
        if "localhost:" in s:
            port = int(s.split(":")[-1])
            up = port_listening(port)
            port_str = " ✅" if up else " ❌"
        print(f"  {h} → {s}{port_str}")


def cmd_add(ports):
    config = get_config()
    ingress = config.get("ingress", [])
    catch_all = ingress[-1] if ingress and "hostname" not in ingress[-1] else {"service": "http_status:404"}
    rules = [r for r in ingress if "hostname" in r]
    existing = {r["hostname"] for r in rules}

    added = []
    for port in ports:
        h = hostname_for(port)
        up = port_listening(port)
        if h in existing:
            print(f"  ⏭️  {h} 已存在 {'✅' if up else '❌ 端口未监听'}")
            continue
        if not up:
            print(f"  ⚠️  localhost:{port} 未监听，仍然添加路由")
        rules.append({"hostname": h, "service": f"http://localhost:{port}"})
        added.append((port, h))

    if not added:
        print("\n没有新增路由")
        return

    # 更新 tunnel ingress
    config["ingress"] = rules + [catch_all]
    put_config(config)

    # 创建 DNS CNAME
    for port, h in added:
        dns_ok = dns_add(h)
        print(f"  ✅ {h} → localhost:{port}  DNS:{'✅' if dns_ok else '❌'}")

    print(f"\n🎉 成功添加 {len(added)} 条路由")


def cmd_del(ports):
    config = get_config()
    ingress = config.get("ingress", [])
    catch_all = ingress[-1] if ingress and "hostname" not in ingress[-1] else {"service": "http_status:404"}
    rules = [r for r in ingress if "hostname" in r]

    to_del = {hostname_for(p) for p in ports}
    removed = []
    kept = []
    for r in rules:
        if r["hostname"] in to_del:
            removed.append(r["hostname"])
        else:
            kept.append(r)

    if not removed:
        print("没有匹配的路由")
        return

    config["ingress"] = kept + [catch_all]
    put_config(config)

    # 删除 DNS CNAME
    for h in removed:
        dns_ok = dns_del(h)
        print(f"  🗑️  {h}  DNS:{'✅' if dns_ok else '⚠️ 未找到'}")

    print(f"\n🎉 成功删除 {len(removed)} 条路由")


def main():
    if not CF_TOKEN:
        print("❌ 未设置 CLOUDFLARE_API_TOKEN_TUNNEL 或 CLOUDFLARE_API_TOKEN")
        sys.exit(1)

    if len(sys.argv) < 2:
        print(__doc__)
        sys.exit(0)

    cmd = sys.argv[1]
    if cmd == "list":
        cmd_list()
    elif cmd == "add":
        if len(sys.argv) < 3:
            print("用法: cf_tunnel.py add <port> [port2 ...]")
            sys.exit(1)
        cmd_add([int(p) for p in sys.argv[2:]])
    elif cmd == "del":
        if len(sys.argv) < 3:
            print("用法: cf_tunnel.py del <port> [port2 ...]")
            sys.exit(1)
        cmd_del([int(p) for p in sys.argv[2:]])
    else:
        print(f"未知命令: {cmd}")
        print(__doc__)
        sys.exit(1)


if __name__ == "__main__":
    main()
