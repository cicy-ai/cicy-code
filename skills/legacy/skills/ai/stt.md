# STT (Speech-to-Text) Skill

Docker service for speech-to-text recognition.

## Service

- Container: `prod-stt`
- Port: `15003`
- Location: `~/projects/docker/docker-prod/stt-api/`

## Engines

| Engine | 状态 | 速度 | 质量 |
|--------|------|------|------|
| google | ✅ 可用 | 快 | 一般，中英混合差 |
| whisper | ⏳ 待装 | 慢 | 好，多语言 |

## API

### GET /health
```bash
curl http://127.0.0.1:15003/health
# {"status":"ok","engines":["google","whisper"]}
```

### POST /stt
```bash
# Google（默认）
curl -X POST http://127.0.0.1:15003/stt -F "file=@voice.ogg"

# 指定语言
curl -X POST http://127.0.0.1:15003/stt -F "file=@voice.ogg" -F "language=en-US"

# Whisper（需先安装）
curl -X POST http://127.0.0.1:15003/stt -F "file=@voice.ogg" -F "engine=whisper"
```

Response: `{"text": "识别结果", "engine": "google"}`

## TG Bot 集成

tg-bot bridge 收到语音消息时自动调用 STT：
```
TG 语音 → 下载 ogg → POST /stt → 识别文本 → 回显 "🎙 xxx" → 发到 tmux
```

## Management

```bash
cd ~/projects/docker/docker-prod
docker compose up -d --build stt-api   # rebuild
docker compose restart stt-api
docker compose logs -f stt-api
```
