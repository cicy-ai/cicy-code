#!/usr/bin/env python3
"""webpage-ping - 测试 Agent → WS → 网页客户端连通性（浏览器 + Electron 均可）"""
import websocket, json, sys, time, requests, os, re

def load_token():
    with open('/home/w3c_offical/global.json') as f:
        return json.load(f)['api_token']

API = 'http://localhost:8008'
WS  = 'ws://localhost:8008'

def ping(pane=None):
    token = load_token()
    if pane is None:
        m = re.search(r'(w-\d+)', os.getcwd())
        pane = m.group(1) if m else 'w-10001'

    request_id = f"webpage-ping-{int(time.time())}"
    ws = websocket.create_connection(f"{WS}/api/chat/ws?pane={pane}&token={token}", timeout=10)
    ws.settimeout(1)

    requests.post(f'{API}/api/chat/push',
        headers={'Authorization': f'Bearer {token}', 'Content-Type': 'application/json'},
        json={'pane': pane, 'type': 'webpage_ping', 'data': {'requestId': request_id}})

    print(f"✅ 发送 webpage_ping ({request_id}) → pane={pane}")

    for _ in range(15):
        try:
            msg = json.loads(ws.recv())
            if msg.get('type') == 'webpage_pong' and msg.get('data', {}).get('requestId') == request_id:
                version = msg.get('data', {}).get('version', 'unknown')
                print(f"✅ 收到 webpage_pong！网页客户端在线 (v{version})")
                ws.close()
                return True
        except:
            pass

    print("❌ 超时未收到 webpage_pong")
    ws.close()
    return False

if __name__ == '__main__':
    pane = sys.argv[1] if len(sys.argv) > 1 else None
    sys.exit(0 if ping(pane) else 1)
