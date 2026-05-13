# TG Sender Skill

Docker service for sending Telegram messages, files, photos, videos, and voice.

## Service

- Container: `prod-tg-sender`
- Port: `15004`
- Location: `~/projects/docker/docker-prod/tg-sender/`
- Auto-reload: ✅ edit `server.py` → instant reload

## API

All endpoints require `token` (bot token) and `chat_id`.

### POST /send — Text message
```bash
curl -X POST http://127.0.0.1:15004/send \
  -H "Content-Type: application/json" \
  -d '{"token":"BOT_TOKEN","chat_id":"CHAT_ID","text":"Hello","parse_mode":"Markdown"}'
```

### POST /send_photo — Photo (URL or file_id)
```bash
curl -X POST http://127.0.0.1:15004/send_photo \
  -H "Content-Type: application/json" \
  -d '{"token":"BOT_TOKEN","chat_id":"CHAT_ID","photo":"https://example.com/img.jpg","caption":"caption"}'
```

### POST /send_audio — Audio (URL or file_id)
```bash
curl -X POST http://127.0.0.1:15004/send_audio \
  -H "Content-Type: application/json" \
  -d '{"token":"BOT_TOKEN","chat_id":"CHAT_ID","audio":"https://example.com/audio.mp3"}'
```

### POST /send_document — Document (URL or file_id)
```bash
curl -X POST http://127.0.0.1:15004/send_document \
  -H "Content-Type: application/json" \
  -d '{"token":"BOT_TOKEN","chat_id":"CHAT_ID","document":"https://example.com/file.pdf"}'
```

### POST /send_video — Video (URL or file_id)
```bash
curl -X POST http://127.0.0.1:15004/send_video \
  -H "Content-Type: application/json" \
  -d '{"token":"BOT_TOKEN","chat_id":"CHAT_ID","video":"https://example.com/video.mp4"}'
```

### POST /send_voice — Voice (URL or file_id)
```bash
curl -X POST http://127.0.0.1:15004/send_voice \
  -H "Content-Type: application/json" \
  -d '{"token":"BOT_TOKEN","chat_id":"CHAT_ID","voice":"https://example.com/voice.ogg"}'
```

### POST /send_video_file — Upload video file (auto-compress if >50MB)
```bash
curl -X POST http://127.0.0.1:15004/send_video_file \
  -F "token=BOT_TOKEN" \
  -F "chat_id=CHAT_ID" \
  -F "caption=视频说明" \
  -F "video=@big_video.mp4"
```

Videos >50MB are auto-compressed with ffmpeg to fit Telegram's limit.

## Management

```bash
cd ~/projects/docker/docker-prod
docker compose restart tg-sender
docker compose logs -f tg-sender
```
