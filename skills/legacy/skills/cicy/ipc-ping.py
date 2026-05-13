#!/usr/bin/env python3
"""IPC Ping - 测试 Agent → WS → AgentPage → electronRPC → Electron 连通性"""
import websocket, json, sys, time, requests

def load_token():
    with open('/home/w3c_offical/global.json') as f:
        return json.load(f)['api_token']

API = 'http://localhost:8008'
WS  = 'ws://localhost:8008'

def ping_ipc(pane=None):
    token = load_token()
    if pane is None:
        import os, re
        cwd = os.getcwd()
        m = re.search(r'(w-\d+)', cwd)
        pane = m.group(1) if m else 'w-10001'
    token = load_token()
    request_id = f"ipc-ping-{int(time.time())}"

    ws = websocket.create_connection(f"{WS}/api/chat/ws?pane={pane}&token={token}", timeout=10)
    ws.settimeout(1)

    resp = requests.post(f'{API}/api/chat/push',
        headers={'Authorization': f'Bearer {token}', 'Content-Type': 'application/json'},
        json={'pane': pane, 'type': 'desktop_event', 'data': {'type': 'ipc_ping', 'requestId': request_id}})

    if resp.status_code != 200:
        print(f"❌ 推送失败: {resp.text}")
        ws.close()
        return False

    print(f"✅ 发送 ipc_ping ({request_id})")

    for i in range(15):
        try:
            msg = ws.recv()
            data = json.loads(msg)
            if data.get('type') == 'ipc_pong' and data.get('data', {}).get('requestId') == request_id:
                result = data.get('data', {}).get('result', '')
                print(f"✅ 收到 ipc_pong！Electron 连通成功")
                if result:
                    print(f"   Electron 返回: {result}")
                ws.close()
                return True
        except:
            pass

    print("❌ 超时未收到 ipc_pong")
    ws.close()
    return False

if __name__ == '__main__':
    success = ping_ipc()
    sys.exit(0 if success else 1)
