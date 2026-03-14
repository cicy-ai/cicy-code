import json
import re
import struct
import time
import threading
import redis
import urllib.request
from mitmproxy import http

r = redis.Redis(host='127.0.0.1', port=16379, decode_responses=True)

WEBHOOK = "http://127.0.0.1:14446/api/chat/webhook"
TARGET_HEADER = "AmazonCodeWhispererStreamingService.GenerateAssistantResponse"


def _post_webhook(pane: str, event: str, data: dict):
    """Fire-and-forget POST to chat webhook."""
    body = json.dumps({"pane": pane, "event": event, "data": data}).encode()
    req = urllib.request.Request(WEBHOOK, data=body, headers={"Content-Type": "application/json"})
    try:
        urllib.request.urlopen(req, timeout=3)
    except Exception:
        pass


def _parse_aws_events(raw: bytes) -> list:
    """Parse AWS Event Stream binary frames into (event_type, json_payload) tuples."""
    events = []
    pos = 0
    while pos + 12 <= len(raw):
        if pos + 4 > len(raw):
            break
        total_len = struct.unpack("!I", raw[pos:pos+4])[0]
        if total_len < 16 or pos + total_len > len(raw):
            break
        # headers start at offset 12, payload ends 4 bytes before frame end (message CRC)
        header_len = struct.unpack("!I", raw[pos+4:pos+8])[0]
        headers_start = pos + 12
        headers_end = headers_start + header_len
        payload = raw[headers_end:pos + total_len - 4]

        # Parse headers to find :event-type
        event_type = ""
        hp = headers_start
        while hp < headers_end:
            if hp + 1 > len(raw):
                break
            name_len = raw[hp]
            hp += 1
            name = raw[hp:hp+name_len].decode("utf-8", errors="ignore")
            hp += name_len
            if hp >= len(raw):
                break
            val_type = raw[hp]
            hp += 1
            if val_type == 7:  # string
                if hp + 2 > len(raw):
                    break
                vlen = struct.unpack("!H", raw[hp:hp+2])[0]
                hp += 2
                val = raw[hp:hp+vlen].decode("utf-8", errors="ignore")
                hp += vlen
                if name == ":event-type":
                    event_type = val
            else:
                break  # skip unknown types

        if event_type and payload:
            try:
                events.append((event_type, json.loads(payload)))
            except Exception:
                pass
        pos += total_len
    return events


# ── Streaming state ──
_stream = {}  # flow.id -> {pane, buf, raw, text, last_t}


def _parse_frames(buf):
    """Parse complete frames from buffer, return (events, remaining)."""
    events = []
    pos = 0
    while pos + 12 <= len(buf):
        total_len = struct.unpack("!I", buf[pos:pos+4])[0]
        if total_len < 16 or pos + total_len > len(buf):
            break
        header_len = struct.unpack("!I", buf[pos+4:pos+8])[0]
        hs = pos + 12
        he = hs + header_len
        payload = buf[he:pos + total_len - 4]
        etype = ""
        hp = hs
        while hp < he:
            if hp + 1 > len(buf): break
            nl = buf[hp]; hp += 1
            nm = buf[hp:hp+nl].decode("utf-8", errors="ignore"); hp += nl
            if hp >= len(buf): break
            vt = buf[hp]; hp += 1
            if vt == 7:
                if hp + 2 > len(buf): break
                vl = struct.unpack("!H", buf[hp:hp+2])[0]; hp += 2
                v = buf[hp:hp+vl].decode("utf-8", errors="ignore"); hp += vl
                if nm == ":event-type": etype = v
            else:
                break
        if etype and payload:
            try: events.append((etype, json.loads(payload)))
            except: pass
        pos += total_len
    return events, buf[pos:]


def _extract_and_push_q(pane: str, raw: bytes):
    """Extract user Q from request body and push to webhook."""
    try:
        body = json.loads(raw)
        cs = body.get("conversationState", {})
        cur = cs.get("currentMessage", {})
        um = cur.get("userInputMessage", {})
        content = um.get("content", "")
        ctx = um.get("userInputMessageContext", {})
        if ctx.get("toolResults") or not content:
            return
        m = re.search(r"USER MESSAGE BEGIN ---\n(.*?)\n--- USER MESSAGE END", content, re.DOTALL)
        if not m:
            return
        q = m.group(1).strip()
        if q:
            _post_webhook(pane, "user_q", {"q": q})
    except Exception:
        pass


def _process_ai_response(pane: str, raw: bytes):
    """Parse AI response events and push to webhook."""
    events = _parse_aws_events(raw)
    if not events:
        return

    # Collect text chunks and tool uses
    text_parts = []
    tools = {}  # toolUseId -> {name, input_parts}

    for etype, payload in events:
        if etype == "assistantResponseEvent":
            c = payload.get("content", "")
            if c:
                text_parts.append(c)
        elif etype == "toolUseEvent":
            tid = payload.get("toolUseId", "")
            name = payload.get("name", "")
            inp = payload.get("input", "")
            if tid:
                if tid not in tools:
                    tools[tid] = {"name": name, "input_parts": []}
                if name and not tools[tid]["name"]:
                    tools[tid]["name"] = name
                if inp:
                    tools[tid]["input_parts"].append(inp)

    # Push ai_chunk with full text
    full_text = "".join(text_parts)
    if full_text:
        _post_webhook(pane, "ai_chunk", {"delta": full_text})

    # Push tool events
    for tid, info in tools.items():
        full_input = "".join(info["input_parts"])
        _post_webhook(pane, "tool_action", {
            "id": tid, "name": info["name"], "input": full_input
        })

    # Signal done
    from mitmproxy import ctx
    ctx.log.info(f"[chat-hook] {pane} text={len(full_text)}c tools={len(tools)}")
    _post_webhook(pane, "ai_done", {"text_length": len(full_text), "tool_count": len(tools)})


def responseheaders(flow: http.HTTPFlow):
    """Enable streaming for Kiro AI responses — push events in real-time."""
    target = flow.request.headers.get("x-amz-target", "")
    if target != TARGET_HEADER:
        return
    auth = flow.metadata.get("proxyauth")
    if not auth:
        return
    pane = auth[0]

    # Push Q immediately
    if flow.request.content:
        threading.Thread(target=_extract_and_push_q, args=(pane, flow.request.content), daemon=True).start()

    _stream[flow.id] = {"pane": pane, "buf": b"", "raw": b"", "text": [], "last_t": time.time()}

    def on_chunk(chunk: bytes) -> bytes:
        st = _stream.get(flow.id)
        if not st:
            return chunk
        st["buf"] += chunk
        st["raw"] += chunk
        events, st["buf"] = _parse_frames(st["buf"])
        for etype, payload in events:
            if etype == "assistantResponseEvent":
                c = payload.get("content", "")
                if c:
                    st["text"].append(c)
                    now = time.time()
                    if now - st["last_t"] > 0.4:
                        st["last_t"] = now
                        _post_webhook(pane, "ai_chunk", {"delta": "".join(st["text"])})
            elif etype == "toolUseEvent":
                # Don't push tool_start — wait for ai_done to get complete tool data
                pass
        return chunk

    flow.response.stream = on_chunk


def response(flow: http.HTTPFlow):
    auth = flow.metadata.get("proxyauth")
    if not auth:
        return
    pane_id = auth[0]

    ts = int(time.time())
    req_raw = flow.request.content or b''
    # Streaming mode: response.content is empty, use collected raw
    st = _stream.pop(flow.id, None)
    res_raw = st["raw"] if st else (flow.response.content or b'')
    req_body = req_raw.decode('utf-8', errors='ignore')
    res_body = res_raw.decode('utf-8', errors='ignore')
    req_kb = round(len(req_raw) / 1024, 1)
    res_kb = round(len(res_raw) / 1024, 1)
    url = flow.request.pretty_url
    method = flow.request.method
    status = flow.response.status_code

    req_headers = dict(flow.request.headers)
    res_headers = dict(flow.response.headers)

    entry = json.dumps({
        "pane": pane_id, "method": method, "url": url,
        "req_kb": req_kb, "res_kb": res_kb, "status": status, "ts": ts,
        "req_headers": req_headers, "res_headers": res_headers,
        "req_body": req_body, "res_body": res_body,
    })
    r.lpush('kiro_http_log', entry)
    r.ltrim('kiro_http_log', 0, 9999)
    r.publish('kiro_traffic_live', entry)

    # Detect Kiro AI response and push final events
    target = flow.request.headers.get("x-amz-target", "")
    if target == TARGET_HEADER:
        from mitmproxy import ctx
        ctx.log.info(f"[chat-dbg] {pane_id} req={len(req_raw)} res={len(res_raw)}")
        # Push final text + ai_done (Q already pushed in responseheaders)
        if res_raw:
            threading.Thread(target=_process_ai_response, args=(pane_id, res_raw), daemon=True).start()
