#!/usr/bin/env python3
"""Gemini Vision - 截图识图，通过 Electron Gemini 窗口"""
import websocket, json, sys, time, requests, threading

API = 'http://localhost:8008'
WS  = 'ws://localhost:8008'

def load_token():
    with open('/home/w3c_offical/global.json') as f:
        return json.load(f)['api_token']

def gemini_vision(prompt='描述这个截图的内容', pane='w-10001', win_id=4, src_win_id=1, timeout=60):
    token = load_token()
    request_id = f"vision-{int(time.time())}"

    ws = websocket.create_connection(f"{WS}/api/chat/ws?pane={pane}&token={token}", timeout=timeout)
    ws.settimeout(2)

    def send():
        time.sleep(0.5)
        requests.post(f'{API}/api/chat/push',
            headers={'Authorization': f'Bearer {token}', 'Content-Type': 'application/json'},
            json={'pane': pane, 'type': 'desktop_event', 'data': {
                'type': 'gemini_vision_request', 'prompt': prompt,
                'win_id': win_id, 'src_win_id': src_win_id, 'requestId': request_id
            }})

    t = threading.Thread(target=send)
    t.start()

    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            msg = ws.recv()
            data = json.loads(msg)
            if data.get('type') == 'gemini_vision_result' and data.get('data', {}).get('requestId') == request_id:
                ws.close()
                d = data['data']
                if 'error' in d:
                    print(f"❌ {d['error']}", file=sys.stderr)
                    return None
                return d.get('result', '')
        except:
            pass

    ws.close()
    print("❌ 超时", file=sys.stderr)
    return None

if __name__ == '__main__':
    prompt = sys.argv[1] if len(sys.argv) > 1 else '描述这个截图的内容'
    win_id = int(sys.argv[2]) if len(sys.argv) > 2 else 4
    src_win_id = int(sys.argv[3]) if len(sys.argv) > 3 else 1
    result = gemini_vision(prompt, win_id=win_id, src_win_id=src_win_id)
    if result:
        print(result)
    else:
        sys.exit(1)
