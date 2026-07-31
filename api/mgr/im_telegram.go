// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// tgLang normalises a Telegram language_code to one of: "zh-CN", "ja", "fr", "en".
func tgLang(code string) string {
	c := strings.ToLower(strings.TrimSpace(code))
	switch {
	case c == "zh-hans" || c == "zh-cn" || strings.HasPrefix(c, "zh"):
		return "zh-CN"
	case strings.HasPrefix(c, "ja"):
		return "ja"
	case strings.HasPrefix(c, "fr"):
		return "fr"
	default:
		return "en"
	}
}

// tgStrings holds all UI strings for the Telegram bot.
var tgStrings = map[string]map[string]string{
	"cmdHello": {
		"zh-CN": "👋 你好！我是你的 AI 助手。直接发消息即可开始对话。\n\n发送 /help 查看可用命令。",
		"ja":    "👋 こんにちは！AIアシスタントです。メッセージを送るだけで会話を始められます。\n\n/help でコマンド一覧を確認できます。",
		"fr":    "👋 Bonjour ! Je suis votre assistant IA. Envoyez un message pour commencer.\n\n/help pour voir les commandes disponibles.",
		"en":    "👋 Hello! I'm your AI assistant. Just send a message to start chatting.\n\n/help to see available commands.",
	},
	"cmdHelp": {
		"zh-CN": "📖 可用命令\n\n/start — 开始对话\n/help — 显示此帮助\n/agents — 列出所有 agent\n/admin — 管理当前绑定的 agent\n/model — 配置当前 agent 的供应商和模型\n/status — 查看当前状态\n\n直接发送文字或语音即可与 AI 对话。",
		"ja":    "📖 使用可能なコマンド\n\n/start — 会話を始める\n/help — ヘルプを表示\n/agents — エージェント一覧\n/admin — バインド中のエージェントを管理\n/model — 供給業者とモデルを設定\n/status — 現在の状態を確認\n\nテキストまたは音声を送るだけでAIと会話できます。",
		"fr":    "📖 Commandes disponibles\n\n/start — Démarrer\n/help — Afficher cette aide\n/agents — Lister les agents\n/admin — Gérer l'agent lié\n/model — Configurer le fournisseur et le modèle\n/status — Voir le statut actuel\n\nEnvoyez du texte ou de la voix pour parler à l'IA.",
		"en":    "📖 Available Commands\n\n/start — Start chatting\n/help — Show this help\n/agents — List all agents\n/admin — Manage bound agent\n/model — Configure provider & model\n/status — Check current status\n\nJust send text or voice to chat with the AI.",
	},
	"noAgentBound": {
		"zh-CN": "⚠️ 当前未绑定 agent，请先使用 /agents 绑定。",
		"ja":    "⚠️ エージェントがバインドされていません。/agents でバインドしてください。",
		"fr":    "⚠️ Aucun agent lié. Utilisez /agents pour en lier un.",
		"en":    "⚠️ No agent bound. Use /agents to bind one.",
	},
	"statusOK": {
		"zh-CN": "✅ 状态正常\n🤖 %s\n📡 平台: Telegram",
		"ja":    "✅ 正常\n🤖 %s\n📡 プラットフォーム: Telegram",
		"fr":    "✅ Statut normal\n🤖 %s\n📡 Plateforme : Telegram",
		"en":    "✅ Status OK\n🤖 %s\n📡 Platform: Telegram",
	},
	"statusBound": {
		"zh-CN": "已绑定 agent: %s",
		"ja":    "バインド済みエージェント: %s",
		"fr":    "Agent lié : %s",
		"en":    "Bound agent: %s",
	},
	"statusUnbound": {
		"zh-CN": "未绑定 agent",
		"ja":    "エージェント未バインド",
		"fr":    "Aucun agent lié",
		"en":    "No agent bound",
	},
	"selectAgent": {
		"zh-CN": "🤖 选择 Agent:",
		"ja":    "🤖 エージェントを選択:",
		"fr":    "🤖 Sélectionner un agent :",
		"en":    "🤖 Select Agent:",
	},
	"noAgents": {
		"zh-CN": "暂无活跃 agent",
		"ja":    "アクティブなエージェントがありません",
		"fr":    "Aucun agent actif",
		"en":    "No active agents",
	},
	"agentNotFound": {
		"zh-CN": "⚠️ Agent 未找到",
		"ja":    "⚠️ エージェントが見つかりません",
		"fr":    "⚠️ Agent introuvable",
		"en":    "⚠️ Agent not found",
	},
	"gwOn": {
		"zh-CN": "✅ 开启",
		"ja":    "✅ 有効",
		"fr":    "✅ Activé",
		"en":    "✅ On",
	},
	"gwOff": {
		"zh-CN": "❌ 关闭",
		"ja":    "❌ 無効",
		"fr":    "❌ Désactivé",
		"en":    "❌ Off",
	},
	"agentDetail": {
		"zh-CN": "🤖 %s\n\nID: %s\nTitle: %s\nType: %s\n本地网关: %s\n代理: %s",
		"ja":    "🤖 %s\n\nID: %s\nTitle: %s\nType: %s\nローカルGW: %s\nプロキシ: %s",
		"fr":    "🤖 %s\n\nID: %s\nTitle: %s\nType: %s\nGateway local: %s\nProxy: %s",
		"en":    "🤖 %s\n\nID: %s\nTitle: %s\nType: %s\nLocal GW: %s\nProxy: %s",
	},
	"providerLabel": {
		"zh-CN": "\n供应商: %s",
		"ja":    "\nプロバイダー: %s",
		"fr":    "\nFournisseur : %s",
		"en":    "\nProvider: %s",
	},
	"modelLabel": {
		"zh-CN": "\n模型: %s",
		"ja":    "\nモデル: %s",
		"fr":    "\nModèle : %s",
		"en":    "\nModel: %s",
	},
	"defaultVal": {
		"zh-CN": "(默认)",
		"ja":    "(デフォルト)",
		"fr":    "(par défaut)",
		"en":    "(default)",
	},
	"btnSwitch": {
		"zh-CN": "💬 切换到此对话",
		"ja":    "💬 このエージェントに切替",
		"fr":    "💬 Passer à cet agent",
		"en":    "💬 Switch to this agent",
	},
	"btnCLI": {
		"zh-CN": "🖥 打开 CLI",
		"ja":    "🖥 CLIを開く",
		"fr":    "🖥 Ouvrir CLI",
		"en":    "🖥 Open CLI",
	},
	"btnOpenTerminal": {
		"zh-CN": "🔗 打开终端",
		"ja":    "🔗 ターミナルを開く",
		"fr":    "🔗 Ouvrir terminal",
		"en":    "🔗 Open Terminal",
	},
	"btnRestart": {
		"zh-CN": "🔄 重启 Agent",
		"ja":    "🔄 エージェントを再起動",
		"fr":    "🔄 Redémarrer l'agent",
		"en":    "🔄 Restart Agent",
	},
	"btnSummary": {
		"zh-CN": "📋 Summary",
		"ja":    "📋 Summary",
		"fr":    "📋 Summary",
		"en":    "📋 Summary",
	},
	"btnCompact": {
		"zh-CN": "🗜 Compact",
		"ja":    "🗜 Compact",
		"fr":    "🗜 Compact",
		"en":    "🗜 Compact",
	},
	"summaryRunning": {
		"zh-CN": "⏳ 正在生成 summary，请稍候...",
		"ja":    "⏳ summary を生成中...",
		"fr":    "⏳ Génération du résumé...",
		"en":    "⏳ Generating summary, please wait...",
	},
	"compactSent": {
		"zh-CN": "✅ 已发送 /compact 给 agent",
		"ja":    "✅ /compact を送信しました",
		"fr":    "✅ /compact envoyé à l'agent",
		"en":    "✅ /compact sent to agent",
	},
	"btnBack": {
		"zh-CN": "↩️ 返回",
		"ja":    "↩️ 戻る",
		"fr":    "↩️ Retour",
		"en":    "↩️ Back",
	},
	"restartOK": {
		"zh-CN": "✅ `%s` 已重启",
		"ja":    "✅ `%s` を再起動しました",
		"fr":    "✅ `%s` redémarré",
		"en":    "✅ `%s` restarted",
	},
	"restartFail": {
		"zh-CN": "❌ 重启失败: %v",
		"ja":    "❌ 再起動失敗: %v",
		"fr":    "❌ Échec du redémarrage : %v",
		"en":    "❌ Restart failed: %v",
	},
	"switchOK": {
		"zh-CN": "✅ 已切换到 `%s`\n后续消息将发送给此 agent。",
		"ja":    "✅ `%s` に切り替えました\n以降のメッセージはこのエージェントに送られます。",
		"fr":    "✅ Basculé vers `%s`\nLes prochains messages seront envoyés à cet agent.",
		"en":    "✅ Switched to `%s`\nFuture messages will go to this agent.",
	},
	"selectProvider": {
		"zh-CN": "⚙️ %s — 选择供应商\n当前: %s / %s",
		"ja":    "⚙️ %s — プロバイダーを選択\n現在: %s / %s",
		"fr":    "⚙️ %s — Sélectionner fournisseur\nActuel : %s / %s",
		"en":    "⚙️ %s — Select Provider\nCurrent: %s / %s",
	},
	"selectModel": {
		"zh-CN": "⚙️ %s — %s — 选择模型",
		"ja":    "⚙️ %s — %s — モデルを選択",
		"fr":    "⚙️ %s — %s — Sélectionner modèle",
		"en":    "⚙️ %s — %s — Select Model",
	},
	"providerNotFound": {
		"zh-CN": "⚠️ 供应商不存在",
		"ja":    "⚠️ プロバイダーが見つかりません",
		"fr":    "⚠️ Fournisseur introuvable",
		"en":    "⚠️ Provider not found",
	},
	"modelUpdated": {
		"zh-CN": "✅ 已更新 `%s`\n供应商: %s\n模型: %s",
		"ja":    "✅ `%s` を更新しました\nプロバイダー: %s\nモデル: %s",
		"fr":    "✅ `%s` mis à jour\nFournisseur : %s\nModèle : %s",
		"en":    "✅ Updated `%s`\nProvider: %s\nModel: %s",
	},
	"modelUpdateFail": {
		"zh-CN": "❌ 更新失败: %v",
		"ja":    "❌ 更新失敗: %v",
		"fr":    "❌ Échec de la mise à jour : %v",
		"en":    "❌ Update failed: %v",
	},
	"cmdDescHelp": {
		"zh-CN": "显示帮助信息",
		"ja":    "ヘルプを表示",
		"fr":    "Afficher l'aide",
		"en":    "Show help",
	},
	"cmdDescStart": {
		"zh-CN": "开始对话",
		"ja":    "会話を始める",
		"fr":    "Démarrer",
		"en":    "Start chatting",
	},
	"cmdDescAgents": {
		"zh-CN": "列出所有 agent",
		"ja":    "エージェント一覧",
		"fr":    "Lister les agents",
		"en":    "List all agents",
	},
	"cmdDescAdmin": {
		"zh-CN": "管理当前绑定的 agent",
		"ja":    "バインド中のエージェントを管理",
		"fr":    "Gérer l'agent lié",
		"en":    "Manage bound agent",
	},
	"cmdDescModel": {
		"zh-CN": "配置当前 agent 的供应商和模型",
		"ja":    "供給業者とモデルを設定",
		"fr":    "Configurer fournisseur et modèle",
		"en":    "Configure provider & model",
	},
	"cmdDescStatus": {
		"zh-CN": "查看当前状态",
		"ja":    "現在の状態を確認",
		"fr":    "Voir le statut",
		"en":    "Check status",
	},
}

// tgT returns the translated string for the given lang and key.
// Falls back to "en" if the lang or key is not found.
func tgT(lang, key string) string {
	l := tgLang(lang)
	if m, ok := tgStrings[key]; ok {
		if s, ok := m[l]; ok {
			return s
		}
		if s, ok := m["en"]; ok {
			return s
		}
	}
	return key
}

// telegramTransport implements botTransport over the Telegram Bot API,
// reusing the helpers in tg.go.
type telegramTransport struct {
	token string
}

func newTelegramTransport(acc *imAccount) (botTransport, error) {
	token := strings.TrimSpace(acc.Secret)
	if token == "" {
		return nil, fmt.Errorf("telegram bot token not set")
	}
	return &telegramTransport{token: token}, nil
}

func (t *telegramTransport) Kind() string  { return imPlatformTelegram }
func (t *telegramTransport) CanEdit() bool { return true }

// telegramValidateToken probes getMe with the token.
//   - returns (username, true, nil) on success;
//   - (_, true, err) when Telegram itself rejected the token (a real bad token);
//   - (_, false, err) when api.telegram.org was unreachable (network/proxy issue —
//     not necessarily a bad token, so callers may still accept it).
func telegramValidateToken(token string) (username string, reachable bool, err error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return "", true, fmt.Errorf("empty token")
	}
	result, perr := tgPostFormWithToken(token, "getMe", url.Values{})
	if perr != nil {
		if result != nil {
			return "", true, perr // Telegram replied ok:false → token rejected
		}
		return "", false, perr // could not reach Telegram
	}
	body, _ := result["result"].(map[string]interface{})
	if body == nil {
		return "", true, fmt.Errorf("getMe returned no result")
	}
	username, _ = body["username"].(string)
	if strings.TrimSpace(username) == "" {
		if name, ok := body["first_name"].(string); ok {
			username = strings.TrimSpace(name)
		}
	}
	if strings.TrimSpace(username) == "" {
		username = "bot"
	}
	return username, true, nil
}

func (t *telegramTransport) Poll(cursor string) ([]botMsg, string, error) {
	offset, _ := strconv.ParseInt(strings.TrimSpace(cursor), 10, 64)
	if offset < 0 {
		offset = 0
	}
	reqURL := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?timeout=30&offset=%d", t.token, offset)
	resp, err := http.Get(reqURL)
	if err != nil {
		return nil, cursor, err
	}
	defer resp.Body.Close()
	var payload tgGetUpdatesResp
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, cursor, err
	}
	if !payload.OK {
		return nil, cursor, fmt.Errorf("telegram getUpdates returned not ok")
	}
	var msgs []botMsg
	maxUpdate := offset - 1
	for _, update := range payload.Result {
		if update.UpdateID > maxUpdate {
			maxUpdate = update.UpdateID
		}
		// Handle inline button callbacks
		if update.CallbackQuery != nil {
			go t.handleCallback(update.CallbackQuery)
			continue
		}
		m := update.primaryMessage()
		if m == nil {
			continue
		}
		text := strings.TrimSpace(firstNonEmpty(m.Text, m.Caption))
		peer := botPeer{ChatID: strconv.FormatInt(m.Chat.ID, 10)}
		fromID := strconv.FormatInt(m.From.ID, 10)
		lang := m.From.LanguageCode

		// 语音消息：下载 OGG OPUS 字节 → imHandleInbound 走 STT。
		if m.Voice != nil && strings.TrimSpace(m.Voice.FileID) != "" {
			data, err := tgDownloadFile(t.token, m.Voice.FileID)
			if err != nil {
				log.Printf("[im] tg voice download failed: %v", err)
			} else if len(data) > 0 {
				msgs = append(msgs, botMsg{
					Text:        text,
					Peer:        peer,
					FromID:      fromID,
					LangCode:    lang,
					VoiceData:   data,
					VoiceFormat: "ogg",
				})
				continue
			}
		}

		// 普通音频文件：作为附件落到 inbox（不走 STT）。
		// 图片 / 文件 / 视频：下载到 inbox 让 agent 用绝对路径读。
		var atts []botAttachment
		if m.Audio != nil && strings.TrimSpace(m.Audio.FileID) != "" {
			if att, err := tgBuildAttachment(t.token, "file", m.Audio.FileID, m.Audio.FileName, m.Audio.FileSize, "audio.bin"); err == nil {
				atts = append(atts, att)
			}
		}
		if len(m.Photo) > 0 {
			// Photo 数组按尺寸递增，最后一个是最高分辨率。
			best := m.Photo[len(m.Photo)-1]
			if att, err := tgBuildAttachment(t.token, "image", best.FileID, "", best.FileSize, "photo.jpg"); err == nil {
				atts = append(atts, att)
			}
		}
		if m.Document != nil && strings.TrimSpace(m.Document.FileID) != "" {
			kind := "file"
			if strings.HasPrefix(m.Document.MimeType, "image/") {
				kind = "image"
			} else if strings.HasPrefix(m.Document.MimeType, "video/") {
				kind = "video"
			}
			if att, err := tgBuildAttachment(t.token, kind, m.Document.FileID, m.Document.FileName, m.Document.FileSize, "file.bin"); err == nil {
				atts = append(atts, att)
			}
		}
		if m.Video != nil && strings.TrimSpace(m.Video.FileID) != "" {
			if att, err := tgBuildAttachment(t.token, "video", m.Video.FileID, m.Video.FileName, m.Video.FileSize, "video.mp4"); err == nil {
				atts = append(atts, att)
			}
		}
		if m.VideoNote != nil && strings.TrimSpace(m.VideoNote.FileID) != "" {
			if att, err := tgBuildAttachment(t.token, "video", m.VideoNote.FileID, "", m.VideoNote.FileSize, "video_note.mp4"); err == nil {
				atts = append(atts, att)
			}
		}
		if len(atts) > 0 {
			imDebugf("[im] tg pollMsg from=%s attachments=%d kinds=%v", fromID, len(atts), tgAttachmentKinds(atts))
			msgs = append(msgs, botMsg{
				Text:        text,
				Peer:        peer,
				FromID:      fromID,
				LangCode:    lang,
				Attachments: atts,
			})
			continue
		}

		if text == "" {
			continue
		}
		msgs = append(msgs, botMsg{
			Text:     text,
			Peer:     peer,
			FromID:   fromID,
			LangCode: lang,
		})
	}
	next := cursor
	if maxUpdate >= offset {
		next = strconv.FormatInt(maxUpdate+1, 10)
	}
	return msgs, next, nil
}

func (t *telegramTransport) Send(peer botPeer, text string) (string, error) {
	if peer.empty() {
		return "", fmt.Errorf("no chat id")
	}
	text = imClampMessage(text)
	// 优先用 Markdown 模式渲染（reply hook 输出含 ```json``` 代码块和 *bold* 等）。
	// 失败时（Markdown 解析错误，如非闭合的反引号/星号）回退纯文本。
	result, err := sendTGMessageWithToken(t.token, peer.ChatID, text)
	if err != nil {
		log.Printf("[im] tg send markdown failed, fallback plain: %v", err)
		result, err = sendTGPlainMessageWithToken(t.token, peer.ChatID, text)
		if err != nil {
			return "", err
		}
	}
	mid := tgExtractMessageID(result)
	if mid <= 0 {
		return "", nil
	}
	return strconv.FormatInt(mid, 10), nil
}

func (t *telegramTransport) Edit(peer botPeer, messageID, text string) error {
	if peer.empty() {
		return fmt.Errorf("no chat id")
	}
	mid, _ := strconv.ParseInt(strings.TrimSpace(messageID), 10, 64)
	if mid <= 0 {
		return errBotEditUnsupported
	}
	text = imClampMessage(text)
	_, err := editTGMessageTextWithToken(t.token, peer.ChatID, mid, text)
	return err
}

func (t *telegramTransport) Typing(peer botPeer) error {
	if peer.empty() {
		return nil
	}
	_, err := sendTGChatActionWithToken(t.token, peer.ChatID, "typing")
	return err
}

// imClampMessage trims and length-limits outbound IM text.
func imClampMessage(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.TrimSpace(text)
	if text == "" {
		text = "..."
	}
	runes := []rune(text)
	const limit = 3500
	if len(runes) > limit {
		text = string(runes[:limit]) + "\n..."
	}
	return text
}

// tgBuildAttachment 通过 file_id 解析下载 URL 并预下载字节，构成 botAttachment。
// 不暴露含 bot token 的 URL —— 字节直接塞进 att.Bytes，URL 留空。
func tgBuildAttachment(token, kind, fileID, filename string, size int64, fallbackName string) (botAttachment, error) {
	data, err := tgDownloadFile(token, fileID)
	if err != nil {
		return botAttachment{}, err
	}
	name := strings.TrimSpace(filename)
	if name == "" {
		name = fallbackName
	}
	return botAttachment{
		Kind:     kind,
		Filename: name,
		Bytes:    data,
		Size:     size,
	}, nil
}

func tgAttachmentKinds(atts []botAttachment) []string {
	out := make([]string, 0, len(atts))
	for _, a := range atts {
		out = append(out, a.Kind)
	}
	return out
}

// telegramBotCommands defines the menu commands shown to users.
// telegramSyncBotCommands registers the bot menu commands via setMyCommands for each supported language.
func telegramSyncBotCommands(token string) {
	type cmdEntry struct {
		Command     string `json:"command"`
		Description string `json:"description"`
	}
	// langs to register: "" = default (en), plus explicit overrides
	langs := []string{"", "zh-hans", "ja", "fr"}
	for _, lc := range langs {
		// map "" -> "en" for tgT, others use tgLang
		tlang := lc
		if tlang == "" {
			tlang = "en"
		}
		cmds := []cmdEntry{
			{"help", tgT(tlang, "cmdDescHelp")},
			{"start", tgT(tlang, "cmdDescStart")},
			{"agents", tgT(tlang, "cmdDescAgents")},
			{"admin", tgT(tlang, "cmdDescAdmin")},
			{"model", tgT(tlang, "cmdDescModel")},
			{"status", tgT(tlang, "cmdDescStatus")},
		}
		payload := map[string]any{"commands": cmds}
		if lc != "" {
			payload["language_code"] = lc
		}
		body, _ := json.Marshal(payload)
		resp, err := http.Post(
			fmt.Sprintf("https://api.telegram.org/bot%s/setMyCommands", token),
			"application/json",
			strings.NewReader(string(body)),
		)
		if err != nil {
			log.Printf("[im] telegram setMyCommands lang=%q failed: %v", lc, err)
			continue
		}
		resp.Body.Close()
	}
}

// telegramHandleCommand handles /commands locally, returns true if handled.
func telegramHandleCommand(acc *imAccount, tr botTransport, msg botMsg, text string) bool {
	lang := msg.LangCode
	cmd := strings.TrimSpace(text)
	// strip @botname suffix: /help@mybot → /help
	if i := strings.Index(cmd, "@"); i > 0 {
		cmd = cmd[:i]
	}
	// 会话级绑定优先(和入站路由同一优先级):/admin /model /status 都看当前会话。
	boundPane := imChatBoundPane(acc.ID, msg.Peer.ChatID)
	if boundPane == "" {
		boundPane = strings.TrimSpace(acc.BoundPaneID)
	}
	switch cmd {
	case "/start":
		imSendOutbound(imOutboundMessage{AccountID: acc.ID, Transport: tr, Peer: msg.Peer, Text: tgT(lang, "cmdHello"), Purpose: imOutboundPurposeProgrammatic})
		return true
	case "/help":
		imSendOutbound(imOutboundMessage{AccountID: acc.ID, Transport: tr, Peer: msg.Peer, Text: tgT(lang, "cmdHelp"), Purpose: imOutboundPurposeProgrammatic})
		return true
	case "/agents":
		telegramSendAgentsPage(acc.Secret, msg.Peer.ChatID, 0, lang)
		return true
	case "/model":
		pane := shortPaneID(boundPane)
		if pane == "" {
			imSendOutbound(imOutboundMessage{AccountID: acc.ID, Transport: tr, Peer: msg.Peer, Text: tgT(lang, "noAgentBound"), Purpose: imOutboundPurposeProgrammatic})
			return true
		}
		telegramSendModelProviders(acc.Secret, msg.Peer.ChatID, 0, pane, lang)
		return true
	case "/admin":
		pane := shortPaneID(boundPane)
		if pane == "" {
			imSendOutbound(imOutboundMessage{AccountID: acc.ID, Transport: tr, Peer: msg.Peer, Text: tgT(lang, "noAgentBound"), Purpose: imOutboundPurposeProgrammatic})
			return true
		}
		detailText, kb := telegramAgentDetailKeyboard(pane, -1, lang)
		body := map[string]any{"chat_id": msg.Peer.ChatID, "text": detailText}
		if kb != nil {
			body["reply_markup"] = map[string]any{"inline_keyboard": kb}
		}
		data, _ := json.Marshal(body)
		resp, err := http.Post(fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", acc.Secret), "application/json", strings.NewReader(string(data)))
		if err == nil {
			resp.Body.Close()
		}
		return true
	case "/status":
		pane := normPaneID(boundPane)
		status := tgT(lang, "statusUnbound")
		if pane != "" {
			status = fmt.Sprintf(tgT(lang, "statusBound"), shortPaneID(pane))
		}
		reply := fmt.Sprintf(tgT(lang, "statusOK"), status)
		imSendOutbound(imOutboundMessage{AccountID: acc.ID, Transport: tr, Peer: msg.Peer, Text: reply, Purpose: imOutboundPurposeProgrammatic})
		return true
	}
	return false
}

type tgAgent struct {
	PaneID           string
	Title            string
	AgentType        string
	UseCustomGateway bool
	ProxyEnable      bool
	DefaultModel     string
}

const tgAgentsPageSize = 8

func telegramQueryAgents() []tgAgent {
	if store == nil {
		return nil
	}
	rows, err := store.Query(`SELECT pane_id, COALESCE(title,''), COALESCE(agent_type,''), COALESCE(use_custom_gateway,0), COALESCE(proxy_enable,0), COALESCE(default_model,'') FROM agent_config WHERE active=1 ORDER BY pane_id`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []tgAgent
	for rows.Next() {
		var a tgAgent
		var gw, proxy int
		if rows.Scan(&a.PaneID, &a.Title, &a.AgentType, &gw, &proxy, &a.DefaultModel) == nil {
			a.PaneID = shortPaneID(a.PaneID)
			a.UseCustomGateway = gw == 1
			a.ProxyEnable = proxy == 1
			out = append(out, a)
		}
	}
	return out
}

func telegramAgentsPageKeyboard(agents []tgAgent, page int, lang string) (string, [][]map[string]string) {
	total := len(agents)
	if total == 0 {
		return tgT(lang, "noAgents"), nil
	}
	start := page * tgAgentsPageSize
	if start >= total {
		start = 0
		page = 0
	}
	end := start + tgAgentsPageSize
	if end > total {
		end = total
	}
	pages := (total + tgAgentsPageSize - 1) / tgAgentsPageSize

	// Each agent is a button row
	var rows [][]map[string]string
	for _, a := range agents[start:end] {
		label := a.Title
		if label == "" {
			label = a.PaneID
		}
		label += " - " + a.PaneID
		if a.AgentType != "" {
			label += " - " + a.AgentType
		}
		rows = append(rows, []map[string]string{
			{"text": label, "callback_data": fmt.Sprintf("ag_detail:%s:%d", a.PaneID, page)},
		})
	}
	// Pagination row
	if pages > 1 {
		var nav []map[string]string
		if page > 0 {
			nav = append(nav, map[string]string{"text": "⬅️", "callback_data": fmt.Sprintf("ag_page:%d", page-1)})
		}
		nav = append(nav, map[string]string{"text": fmt.Sprintf("%d/%d", page+1, pages), "callback_data": "ag_noop"})
		if page < pages-1 {
			nav = append(nav, map[string]string{"text": "➡️", "callback_data": fmt.Sprintf("ag_page:%d", page+1)})
		}
		rows = append(rows, nav)
	}

	text := tgT(lang, "selectAgent")
	return text, rows
}

func telegramAgentDetailKeyboard(paneID string, fromPage int, lang string) (string, [][]map[string]string) {
	agents := telegramQueryAgents()
	var agent *tgAgent
	for i := range agents {
		if agents[i].PaneID == paneID {
			agent = &agents[i]
			break
		}
	}
	if agent == nil {
		return tgT(lang, "agentNotFound"), nil
	}

	gwStatus := tgT(lang, "gwOff")
	if agent.UseCustomGateway {
		gwStatus = tgT(lang, "gwOn")
	}
	proxyStatus := tgT(lang, "gwOff")
	if agent.ProxyEnable {
		proxyStatus = tgT(lang, "gwOn")
	}

	text := fmt.Sprintf(tgT(lang, "agentDetail"),
		agent.Title, agent.PaneID, agent.Title, agent.AgentType, gwStatus, proxyStatus)

	// Show provider/model info if local gateway is enabled
	if agent.UseCustomGateway {
		model := agent.DefaultModel
		providerName := ""
		if provider, ok := loadProviderForAgentType(agent.AgentType); ok {
			providerName = provider.Name
			if model == "" {
				model = providerDefaultModelForAgentType(provider, agent.AgentType)
			}
		}
		if providerName == "" {
			providerName = loadDefaultProviderKeyForAgentType(agent.AgentType)
		}
		if model == "" {
			model = tgT(lang, "defaultVal")
		}
		if providerName != "" {
			text += fmt.Sprintf(tgT(lang, "providerLabel"), providerName)
		}
		text += fmt.Sprintf(tgT(lang, "modelLabel"), model)
	}

	var rows [][]map[string]string
	adminMode := fromPage == -1
	// Show bind button for any compatible agent type, gateway or not (reply push
	// works on both the local gateway and the non-gateway MITM audit path).
	canBind := !adminMode && (agent.AgentType == "claude" || agent.AgentType == "opencode" || agent.AgentType == "codex")
	if canBind {
		rows = append(rows, []map[string]string{
			{"text": tgT(lang, "btnSwitch"), "callback_data": fmt.Sprintf("ag_bind:%s:%d", paneID, fromPage)},
		})
	}
	rows = append(rows, []map[string]string{
		{"text": tgT(lang, "btnRestart"), "callback_data": fmt.Sprintf("ag_restart:%s:%d", paneID, fromPage)},
	})
	rows = append(rows, []map[string]string{
		{"text": tgT(lang, "btnSummary"), "callback_data": fmt.Sprintf("ag_summary:%s:%d", paneID, fromPage)},
		{"text": tgT(lang, "btnCompact"), "callback_data": fmt.Sprintf("ag_compact:%s:%d", paneID, fromPage)},
	})
	if !adminMode {
		rows = append(rows, []map[string]string{
			{"text": tgT(lang, "btnBack"), "callback_data": fmt.Sprintf("ag_page:%d", fromPage)},
		})
	}
	return text, rows
}

// telegramSendAgentsPage sends the agents list with inline buttons.
func telegramSendAgentsPage(token, chatID string, page int, lang string) {
	agents := telegramQueryAgents()
	text, keyboard := telegramAgentsPageKeyboard(agents, page, lang)
	body := map[string]any{
		"chat_id": chatID,
		"text":    text,
	}
	if keyboard != nil {
		body["reply_markup"] = map[string]any{"inline_keyboard": keyboard}
	}
	data, _ := json.Marshal(body)
	resp, err := http.Post(
		fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", token),
		"application/json",
		strings.NewReader(string(data)),
	)
	if err == nil {
		resp.Body.Close()
	}
}

func telegramEditMessage(token, chatID string, messageID int64, text string, keyboard [][]map[string]string) {
	body := map[string]any{
		"chat_id":    chatID,
		"message_id": messageID,
		"text":       text,
	}
	if keyboard != nil {
		body["reply_markup"] = map[string]any{"inline_keyboard": keyboard}
	} else {
		body["reply_markup"] = map[string]any{"inline_keyboard": []any{}}
	}
	data, _ := json.Marshal(body)
	resp, err := http.Post(
		fmt.Sprintf("https://api.telegram.org/bot%s/editMessageText", token),
		"application/json",
		strings.NewReader(string(data)),
	)
	if err == nil {
		resp.Body.Close()
	}
}

func (t *telegramTransport) handleCallback(cb *tgCallbackQuery) {
	// Answer the callback to dismiss the loading spinner
	tgPostFormWithToken(t.token, "answerCallbackQuery", url.Values{"callback_query_id": {cb.ID}})

	if cb.Message == nil || cb.Data == "ag_noop" {
		return
	}
	chatID := strconv.FormatInt(cb.Message.Chat.ID, 10)
	msgID := cb.Message.MessageID
	lang := cb.From.LanguageCode

	switch {
	case strings.HasPrefix(cb.Data, "ag_page:"):
		page, _ := strconv.Atoi(strings.TrimPrefix(cb.Data, "ag_page:"))
		agents := telegramQueryAgents()
		text, kb := telegramAgentsPageKeyboard(agents, page, lang)
		telegramEditMessage(t.token, chatID, msgID, text, kb)

	case strings.HasPrefix(cb.Data, "ag_detail:"):
		// ag_detail:<paneID>:<fromPage>
		parts := strings.SplitN(strings.TrimPrefix(cb.Data, "ag_detail:"), ":", 2)
		paneID := parts[0]
		fromPage := 0
		if len(parts) > 1 {
			fromPage, _ = strconv.Atoi(parts[1])
		}
		text, kb := telegramAgentDetailKeyboard(paneID, fromPage, lang)
		telegramEditMessage(t.token, chatID, msgID, text, kb)

	case strings.HasPrefix(cb.Data, "ag_bind:"):
		// ag_bind:<paneID>:<fromPage>
		parts := strings.SplitN(strings.TrimPrefix(cb.Data, "ag_bind:"), ":", 2)
		paneID := parts[0]
		fromPage := 0
		if len(parts) > 1 {
			fromPage, _ = strconv.Atoi(parts[1])
		}
		telegramBindAgent(t.token, chatID, msgID, paneID, fromPage, lang)

	case strings.HasPrefix(cb.Data, "ag_cli:"):
		// ag_cli:<paneID>:<fromPage>
		parts := strings.SplitN(strings.TrimPrefix(cb.Data, "ag_cli:"), ":", 2)
		paneID := parts[0]
		fromPage := 0
		if len(parts) > 1 {
			fromPage, _ = strconv.Atoi(parts[1])
		}
		telegramOpenCLI(t.token, chatID, msgID, paneID, fromPage, lang)
	case strings.HasPrefix(cb.Data, "ag_restart:"):
		parts := strings.SplitN(strings.TrimPrefix(cb.Data, "ag_restart:"), ":", 2)
		paneID := parts[0]
		fromPage := 0
		if len(parts) > 1 {
			fromPage, _ = strconv.Atoi(parts[1])
		}
		fullPaneID := paneID
		if !strings.Contains(fullPaneID, ":") {
			fullPaneID += ":main.0"
		}
		apiToken := loadAPIToken()
		err := restartPaneCore(fullPaneID, apiToken)
		var restartText string
		if err != nil {
			restartText = fmt.Sprintf(tgT(lang, "restartFail"), err)
		} else {
			restartText = fmt.Sprintf(tgT(lang, "restartOK"), paneID)
		}
		kb := [][]map[string]string{
			{{"text": tgT(lang, "btnBack"), "callback_data": fmt.Sprintf("ag_detail:%s:%d", paneID, fromPage)}},
		}
		telegramEditMessage(t.token, chatID, msgID, restartText, kb)
	case strings.HasPrefix(cb.Data, "ag_summary:"):
		parts := strings.SplitN(strings.TrimPrefix(cb.Data, "ag_summary:"), ":", 2)
		paneID := parts[0]
		fromPage := 0
		if len(parts) > 1 {
			fromPage, _ = strconv.Atoi(parts[1])
		}
		imDebugf("[im] ag_summary pane=%s chat=%s", paneID, chatID)
		telegramEditMessage(t.token, chatID, msgID, tgT(lang, "summaryRunning"), nil)
		token := t.token
		go func() {
			out, err := runAgentSummary(paneID)
			log.Printf("[im] ag_summary result pane=%s err=%v len=%d", paneID, err, len(out))
			kb := [][]map[string]string{{{"text": tgT(lang, "btnBack"), "callback_data": fmt.Sprintf("ag_detail:%s:%d", paneID, fromPage)}}}
			if err != nil {
				telegramEditMessage(token, chatID, msgID, fmt.Sprintf("❌ summary failed: %v", err), kb)
			} else {
				// Truncate to Telegram edit limit
				runes := []rune(out)
				if len(runes) > 4096 {
					out = string(runes[:4093]) + "..."
				}
				telegramEditMessage(token, chatID, msgID, out, kb)
			}
		}()
	case strings.HasPrefix(cb.Data, "ag_compact:"):
		parts := strings.SplitN(strings.TrimPrefix(cb.Data, "ag_compact:"), ":", 2)
		paneID := parts[0]
		fromPage := 0
		if len(parts) > 1 {
			fromPage, _ = strconv.Atoi(parts[1])
		}
		fullPaneID := paneID
		if !strings.Contains(fullPaneID, ":") {
			fullPaneID += ":main.0"
		}
		if err := sendTextToPane(fullPaneID, "/compact", true); err != nil {
			telegramEditMessage(t.token, chatID, msgID, fmt.Sprintf("❌ compact failed: %v", err), nil)
		} else {
			kb := [][]map[string]string{{{"text": tgT(lang, "btnBack"), "callback_data": fmt.Sprintf("ag_detail:%s:%d", paneID, fromPage)}}}
			telegramEditMessage(t.token, chatID, msgID, tgT(lang, "compactSent"), kb)
		}
	case strings.HasPrefix(cb.Data, "ml_provider:"):
		// ml_provider:<providerKey>:<paneID>
		parts := strings.SplitN(strings.TrimPrefix(cb.Data, "ml_provider:"), ":", 2)
		if len(parts) == 2 {
			telegramSendModelModels(t.token, chatID, msgID, parts[0], parts[1], lang)
		}
	case strings.HasPrefix(cb.Data, "ml_model:"):
		// ml_model:<providerKey>:<model>:<paneID>
		parts := strings.SplitN(strings.TrimPrefix(cb.Data, "ml_model:"), ":", 3)
		if len(parts) == 3 {
			telegramApplyModel(t.token, chatID, msgID, parts[0], parts[1], parts[2], lang)
		}
	case strings.HasPrefix(cb.Data, "ml_back:"):
		paneID := strings.TrimPrefix(cb.Data, "ml_back:")
		telegramSendModelProviders(t.token, chatID, msgID, paneID, lang)
	}
}

func telegramBindAgent(token, chatID string, msgID int64, paneID string, fromPage int, lang string) {
	if store == nil {
		return
	}
	fullPaneID := paneID
	if !strings.Contains(fullPaneID, ":") {
		fullPaneID += ":main.0"
	}
	// 按会话绑定:只绑「点按钮的这个 chat」,不动账号级绑定——一个 bot 的不同
	// 会话(私聊/群)可以各自绑不同的 agent。账号按 token 定位。
	var accID int64
	_ = store.QueryRow("SELECT id FROM im_accounts WHERE platform='telegram' AND secret=?", strings.TrimSpace(token)).Scan(&accID)
	if accID != 0 {
		if err := imBindChatToPane(accID, chatID, fullPaneID); err != nil {
			log.Printf("[tg] chat bind failed account=%d chat=%s pane=%s: %v", accID, chatID, paneID, err)
		}
	} else {
		// 兜底(找不到账号行时保持旧行为):账号级绑定
		_, _ = store.Exec("UPDATE im_accounts SET bound_pane_id=?, updated_at="+store.Now()+" WHERE platform='telegram' AND id IN (SELECT id FROM im_accounts WHERE platform='telegram' AND config LIKE ?)", fullPaneID, fmt.Sprintf("%%\"chat_id\":\"%s\"%%", chatID))
	}

	text := fmt.Sprintf(tgT(lang, "switchOK"), paneID)
	telegramEditMessage(token, chatID, msgID, text, [][]map[string]string{
		{{"text": tgT(lang, "btnBack"), "callback_data": fmt.Sprintf("ag_page:%d", fromPage)}},
	})
}

func telegramOpenCLI(token, chatID string, msgID int64, paneID string, fromPage int, lang string) {
	apiToken := loadAPIToken()
	baseURL := fmt.Sprintf("http://127.0.0.1:%s", os.Getenv("PORT"))
	cliURL := fmt.Sprintf("%s/ttyd/%s/?token=%s&lang=%s", strings.TrimRight(baseURL, "/"), paneID, apiToken, tgLang(lang))

	text := fmt.Sprintf("🖥 *%s CLI*", paneID)
	kb := [][]map[string]string{
		{{"text": tgT(lang, "btnOpenTerminal"), "url": cliURL}},
		{{"text": tgT(lang, "btnBack"), "callback_data": fmt.Sprintf("ag_detail:%s:%d", paneID, fromPage)}},
	}
	telegramEditMessage(token, chatID, msgID, text, kb)
}

// telegramSendModelProviders sends the provider selection menu for /model.
// If msgID != 0, it edits the existing message; otherwise sends a new one.
func telegramSendModelProviders(token, chatID string, msgID int64, paneID string, lang string) {
	// Get agent type to filter providers
	var agentType sql.NullString
	var curModel sql.NullString
	var curConfig sql.NullString
	if store != nil {
		_ = store.QueryRow("SELECT COALESCE(agent_type,''), COALESCE(default_model,''), COALESCE(config,'{}') FROM agent_config WHERE pane_id=?", normPaneID(paneID)).
			Scan(&agentType, &curModel, &curConfig)
	}
	providers := runtimeAIProviderOptionsForAgentType(agentType.String)
	curOv := extractRuntimeAIFromConfigJSON(curConfig.String)
	curProvider := ""
	if curOv != nil {
		curProvider = curOv.ProviderName
	}
	defVal := tgT(lang, "defaultVal")
	curProviderLabel := defVal
	if curProvider != "" {
		curProviderLabel = curProvider
	}
	curModelLabel := defVal
	if curModel.String != "" {
		curModelLabel = curModel.String
	}
	text := fmt.Sprintf(tgT(lang, "selectProvider"), paneID, curProviderLabel, curModelLabel)

	var rows [][]map[string]string
	for _, p := range providers {
		label := p.Label
		if p.Key == curProvider {
			label = "✅ " + label
		}
		rows = append(rows, []map[string]string{
			{"text": label, "callback_data": fmt.Sprintf("ml_provider:%s:%s", p.Key, paneID)},
		})
	}

	if msgID != 0 {
		telegramEditMessage(token, chatID, msgID, text, rows)
	} else {
		body := map[string]any{"chat_id": chatID, "text": text}
		if len(rows) > 0 {
			body["reply_markup"] = map[string]any{"inline_keyboard": rows}
		}
		data, _ := json.Marshal(body)
		resp, err := http.Post(fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", token),
			"application/json", strings.NewReader(string(data)))
		if err != nil {
			log.Printf("[im] telegramSendModelProviders sendMessage err: %v", err)
		} else {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if !strings.Contains(string(b), `"ok":true`) {
				log.Printf("[im] telegramSendModelProviders sendMessage resp: %s", string(b))
			}
		}
	}
}

// telegramSendModelModels sends the model selection menu for a chosen provider.
func telegramSendModelModels(token, chatID string, msgID int64, providerKey, paneID string, lang string) {
	provider, ok := loadProviderByKey(providerKey)
	if !ok {
		telegramEditMessage(token, chatID, msgID, tgT(lang, "providerNotFound"), nil)
		return
	}
	var curModel sql.NullString
	if store != nil {
		_ = store.QueryRow("SELECT COALESCE(default_model,'') FROM agent_config WHERE pane_id=?", normPaneID(paneID)).Scan(&curModel)
	}

	text := fmt.Sprintf(tgT(lang, "selectModel"), paneID, provider.Name)
	var rows [][]map[string]string
	models := provider.Models
	if len(models) == 0 && provider.DefaultModel != "" {
		models = []string{provider.DefaultModel}
	}
	for _, m := range models {
		label := m
		if m == curModel.String {
			label = "✅ " + m
		}
		rows = append(rows, []map[string]string{
			{"text": label, "callback_data": fmt.Sprintf("ml_model:%s:%s:%s", providerKey, m, paneID)},
		})
	}
	rows = append(rows, []map[string]string{
		{"text": tgT(lang, "btnBack"), "callback_data": fmt.Sprintf("ml_back:%s", paneID)},
	})
	telegramEditMessage(token, chatID, msgID, text, rows)
}

// telegramApplyModel applies the selected provider+model to the bound agent.
func telegramApplyModel(token, chatID string, msgID int64, providerKey, model, paneID string, lang string) {
	if store == nil {
		return
	}
	fullPaneID := normPaneID(paneID)
	// Update default_model
	_, err := store.Exec("UPDATE agent_config SET default_model=?, updated_at="+store.Now()+" WHERE pane_id=?", model, fullPaneID)
	if err != nil {
		telegramEditMessage(token, chatID, msgID, fmt.Sprintf(tgT(lang, "modelUpdateFail"), err), nil)
		return
	}
	// Update runtime_ai in config
	var configStr sql.NullString
	_ = store.QueryRow("SELECT COALESCE(config,'{}') FROM agent_config WHERE pane_id=?", fullPaneID).Scan(&configStr)
	ov := &runtimeAIOverride{ProviderName: providerKey}
	nextConfig, err := mergeRuntimeAIIntoConfigJSON(configStr.String, ov)
	if err == nil {
		_, _ = store.Exec("UPDATE agent_config SET config=?, updated_at="+store.Now()+" WHERE pane_id=?", nextConfig, fullPaneID)
	}

	text := fmt.Sprintf(tgT(lang, "modelUpdated"), paneID, providerKey, model)
	telegramEditMessage(token, chatID, msgID, text, nil)
}

// runAgentSummary runs agent-summary for the given paneID and returns the output.
func runAgentSummary(paneID string) (string, error) {
	short := shortPaneID(paneID)
	// Generate AI summary (writes to .cicy/history/summary/)
	cmd := exec.Command("agent-summary", short, "--ai")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	// Read the generated summary file
	home, _ := os.UserHomeDir()
	summaryFile := filepath.Join(home, "cicy-ai", "workers", short, ".cicy", "history", "summary", "current.summary.md")
	if data, err := os.ReadFile(summaryFile); err == nil {
		if s := strings.TrimSpace(string(data)); s != "" {
			return s, nil
		}
	}
	// Fallback to stdout output
	if s := strings.TrimSpace(string(out)); s != "" {
		return s, nil
	}
	return "(no summary generated)", nil
}

// telegramSendText sends a new message (not edit) with optional inline keyboard.
func telegramSendText(token, chatID, text string, keyboard [][]map[string]string) {
	// Truncate to 4096 chars (Telegram limit)
	runes := []rune(text)
	if len(runes) > 4096 {
		runes = runes[:4093]
		text = string(runes) + "..."
	}
	body := map[string]any{"chat_id": chatID, "text": text}
	if len(keyboard) > 0 {
		body["reply_markup"] = map[string]any{"inline_keyboard": keyboard}
	}
	data, _ := json.Marshal(body)
	resp, err := http.Post(fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", token),
		"application/json", strings.NewReader(string(data)))
	if err != nil {
		log.Printf("[im] telegramSendText err: %v", err)
		return
	}
	resp.Body.Close()
}
