#!/usr/bin/env python3
"""webpage - CiCy 网页客户端控制工具集"""
import json, sys, time, os, re, requests, websocket

# ── config ──────────────────────────────────────────────────────────────────

API = 'http://localhost:8008'
WS  = 'ws://localhost:8008'

def load_token():
    with open('/home/w3c_offical/global.json') as f:
        return json.load(f)['api_token']

def current_pane():
    m = re.search(r'(w-\d+)', os.getcwd())
    return m.group(1) if m else 'w-10001'

def ws_connect(pane, token):
    ws = websocket.create_connection(f"{WS}/api/chat/ws?pane={pane}&token={token}", timeout=10)
    ws.settimeout(1)
    return ws

def push(pane, token, type_, data):
    requests.post(f'{API}/api/chat/push',
        headers={'Authorization': f'Bearer {token}', 'Content-Type': 'application/json'},
        json={'pane': pane, 'type': type_, 'data': data})

def recv_until(ws, match_fn, tries=15):
    for _ in range(tries):
        try:
            msg = json.loads(ws.recv())
            if match_fn(msg):
                return msg
        except: pass
    return None

# ── tools ────────────────────────────────────────────────────────────────────

TOOLS = {}

def tool(name, desc):
    def dec(fn):
        TOOLS[name] = {'fn': fn, 'desc': desc}
        return fn
    return dec

@tool('ping', '测试网页客户端连通性（浏览器 + Electron 均可）')
def cmd_ping(args):
    pane = args[0] if args else current_pane()
    token = load_token()
    rid = f"webpage-ping-{int(time.time())}"
    ws = ws_connect(pane, token)
    push(pane, token, 'webpage_ping', {'requestId': rid})
    print(f"✅ 发送 webpage_ping → pane={pane}")
    msg = recv_until(ws, lambda m: m.get('type') == 'webpage_pong' and m.get('data', {}).get('requestId') == rid)
    ws.close()
    if msg:
        v = msg.get('data', {}).get('version', 'unknown')
        print(f"✅ 网页客户端在线 (v{v})")
        return True
    print("❌ 超时未收到 pong")
    return False

@tool('ipc-ping', '测试 Electron IPC 连通性（仅 Electron 客户端）')
def cmd_ipc_ping(args):
    pane = args[0] if args else current_pane()
    token = load_token()
    rid = f"ipc-ping-{int(time.time())}"
    ws = ws_connect(pane, token)
    push(pane, token, 'desktop_event', {'type': 'ipc_ping', 'requestId': rid})
    print(f"✅ 发送 ipc_ping → pane={pane}")
    msg = recv_until(ws, lambda m: m.get('type') == 'ipc_pong' and m.get('data', {}).get('requestId') == rid)
    ws.close()
    if msg:
        print(f"✅ Electron IPC 连通成功")
        return True
    print("❌ 超时未收到 ipc_pong（需要 Electron 客户端在线）")
    return False

@tool('exec-js', '在网页执行 JS 代码并返回结果')
def cmd_exec_js(args):
    if not args:
        print("用法: webpage exec-js '<js代码>' [pane]")
        return False
    code = args[0]
    pane = args[1] if len(args) > 1 else current_pane()
    token = load_token()
    rid = f"exec-{int(time.time()*1000)}"
    ws = ws_connect(pane, token)
    push(pane, token, 'exec_js', {'code': code, 'requestId': rid})
    msg = recv_until(ws, lambda m: m.get('type') == 'exec_js_result' and m.get('data', {}).get('requestId') == rid, tries=20)
    ws.close()
    if msg:
        d = msg.get('data', {})
        if 'error' in d:
            print(f"❌ {d['error']}")
            return False
        print(d.get('result', ''))
        return True
    print("❌ 超时")
    return False

@tool('send', '发送消息到 chat（user_q / ai_chunk / ai_done / worker_idle）')
def cmd_send(args):
    if len(args) < 2:
        print("用法: webpage send <type> <data_json> [pane]")
        print("  例: webpage send user_q '{\"q\":\"hello\"}'")
        return False
    type_ = args[0]
    data  = json.loads(args[1])
    pane  = args[2] if len(args) > 2 else current_pane()
    token = load_token()
    push(pane, token, type_, data)
    print(f"✅ 发送 {type_} → pane={pane}")
    return True

@tool('clients', '查看当前 WS 连接详情')
def cmd_clients(args):
    token = load_token()
    r = requests.get(f'{API}/api/chat/clients', headers={'Authorization': f'Bearer {token}'})
    print(json.dumps(r.json(), indent=2, ensure_ascii=False))
    return True

@tool('help', '显示所有可用工具')
def cmd_help(args):
    print("webpage - CiCy 网页客户端控制工具\n")
    print(f"{'命令':<12} 说明")
    print("-" * 40)
    for name, t in TOOLS.items():
        print(f"  {name:<12} {t['desc']}")
    print("\n用法: webpage <命令> [参数...]")
    return True

# ── main ─────────────────────────────────────────────────────────────────────

if __name__ == '__main__':
    cmd = sys.argv[1] if len(sys.argv) > 1 else 'help'
    args = sys.argv[2:]
    if cmd not in TOOLS:
        print(f"未知命令: {cmd}")
        cmd_help([])
        sys.exit(1)
    ok = TOOLS[cmd]['fn'](args)
    sys.exit(0 if ok else 1)
