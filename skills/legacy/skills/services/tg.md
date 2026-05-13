# TG CLI

Telegram CLI tool for sending messages via tg-sender API.

## Usage

```bash
tg send <message> [--token=] [--chatId=]
tg photo <url> [caption] [--token=] [--chatId=]
tg file <url> [--token=] [--chatId=]
tg video <url> [--token=] [--chatId=]
tg audio <url> [--token=] [--chatId=]
```

## Examples

```bash
# Use defaults from ~/cicy-ai/global.json
tg send "Hello from CLI"
tg send "测试消息 🚀"
tg photo "https://example.com/image.jpg" "Caption here"
tg file "https://example.com/file.pdf"
tg video "https://example.com/video.mp4"
tg audio "https://example.com/audio.mp3"

# Override token/chatId
tg send "test" --token=BOT_TOKEN --chatId=CHAT_ID
```

## Configuration

Add to `~/cicy-ai/global.json`:
```json
{
  "TG_BOT_TOKEN": "your_bot_token",
  "TG_CHAT_ID": "your_chat_id"
}
```

## Requirements

- tg-sender service running on `http://127.0.0.1:15004`
- Node.js (built-in modules only)

## Project

- Location: `~/projects/tg-cli`
- GitHub: https://github.com/cicy-dev/tg-cli (private)
- Command: `~/.local/bin/tg` → `~/projects/tg-cli/tg.js`

## Related Services

- **tg-sender** - Docker service (port 15004) for sending TG messages
- **tg-bot** - Supervisor service for receiving TG messages and controlling Tmux
