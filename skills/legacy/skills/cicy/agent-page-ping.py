#!/usr/bin/env python3
"""AgentPage Ping - 测试 Agent → WS → AgentPage 连通性"""
import websocket, json, sys, time, requests

def load_token():
    with open('/home/w3c_offical/global.json') as f:
        return json.load(f)['api_token']

API = 'http://localhost:8008'
WS  = 'ws://localhost:8008'

def ping_agent_page(pane='w-10001'):
    token = load_token()
    request_id = f"ping-{int(time.time())}"

    ws = websocket.create_connection(f"{WS}/api/chat/ws?pane={pane}&token={token}", timeout=10)
    ws.settimeout(1)

    resp = requests.post(f'{API}/api/chat/push',
        headers={'Authorization': f'Bearer {token}', 'Content-Type': 'application/json'},
        json={'pane': pane, 'type': 'desktop_event', 'data': {'type': 'ping', 'requestId': request_id}})

    if resp.status_code != 200:
        print(f"❌ 推送失败: {resp.text}")
        ws.close()
        return False

    print(f"✅ 发送 ping ({request_id})")

    for i in range(10):
        try:
            msg = ws.recv()
            data = json.loads(msg)
            if data.get('type') == 'pong' and data.get('data', {}).get('requestId') == request_id:
                print(f"✅ 收到 pong！连通成功")
                ws.close()
                return True
        except:
            pass

    print("❌ 超时未收到 pong")
    ws.close()
    return False

if __name__ == '__main__':
    success = ping_agent_page()
    sys.exit(0 if success else 1)
