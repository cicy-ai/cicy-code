# TTS (Text-to-Speech) Skill

Docker service using Microsoft Edge TTS. Converts text to MP3 audio.

## Service

- Container: `prod-tts`
- Port: `15002`
- Location: `~/projects/docker/docker-prod/tts-api/`

## API

### POST /tts
```bash
# Default voice (reads from ~/data/tts-tg-bot/tts_voice.txt or zh-CN-XiaoxiaoNeural)
curl -X POST http://127.0.0.1:15002/tts \
  -H "Content-Type: application/json" \
  -d '{"text":"你好世界"}' -o output.mp3

# Specify voice
curl -X POST http://127.0.0.1:15002/tts \
  -H "Content-Type: application/json" \
  -d '{"text":"Hello","voice":"en-US-JennyNeural"}' -o output.mp3
```

### POST /set_voice
```bash
curl -X POST http://127.0.0.1:15002/set_voice \
  -H "Content-Type: application/json" \
  -d '{"voice":"zh-CN-YunxiNeural"}'
```

### GET /get_voice
```bash
curl http://127.0.0.1:15002/get_voice
```

### GET /health
```bash
curl http://127.0.0.1:15002/health
```

## Management

```bash
cd ~/projects/docker/docker-prod
docker compose restart tts-api
docker compose logs -f tts-api
```
