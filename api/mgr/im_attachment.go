// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

// IM attachment downloader.
//
// 当 botMsg.Attachments 非空时，imHandleInbound 调 imSaveAttachmentsToInbox
// 把每个附件下载到 <workspace>/.cicy/inbox/<timestamp>-<idx><ext>，
// 并返回相对路径列表（agent 可以用 Read 工具直接读）。
//
// 设计：
//   - 直接 HTTP GET 下载（不解密 — WeChat CDN 有些图片是明文 JPEG/PNG）。
//     如果后续发现是 AES 加密的，再加 transport-specific 解密钩子。
//   - inbox 目录每次保留（不自动清），让 agent / 用户可重复访问。
//   - 文件名格式 <ts>-<idx>-<safe_filename> 避免冲突。
//   - 单文件大小上限 50 MB（防止意外撑爆 workspace）。

import (
	"bytes"
	"crypto/aes"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const imAttachmentMaxBytes = 50 << 20 // 50 MB

// imSaveAttachmentsToInbox 下载所有 attachment 到 paneID workspace 的 .cicy/inbox/，
// 返回相对 workspace 根目录的路径列表（如 ".cicy/inbox/20260520T125930-0-photo.jpg"）。
// 任何下载失败的会被跳过并记日志。
func imSaveAttachmentsToInbox(paneID string, atts []botAttachment) []string {
	workspace := strings.TrimSpace(paneWorkspace(paneID))
	if workspace == "" {
		// fallback：DB 里没记 workspace 时退回 builtin 默认目录。
		workspace = builtinWorkerWorkspace(paneID)
	}
	inboxDir := filepath.Join(workspace, ".cicy", "inbox")
	if err := os.MkdirAll(inboxDir, 0o755); err != nil {
		log.Printf("[im] mkdir inbox failed pane=%s err=%v", shortPaneID(paneID), err)
		return nil
	}
	ts := time.Now().UTC().Format("20060102T150405")
	var paths []string
	for i, att := range atts {
		data := att.Bytes
		if len(data) == 0 && strings.TrimSpace(att.URL) != "" {
			body, err := imDownloadAttachment(att.URL)
			if err != nil {
				log.Printf("[im] attachment download failed pane=%s kind=%s url=%.80s err=%v",
					shortPaneID(paneID), att.Kind, att.URL, err)
				continue
			}
			data = body
		}
		if len(data) == 0 {
			continue
		}
		if len(att.AESKey) == 16 {
			plain, err := imAESDecryptECB(data, att.AESKey)
			if err != nil {
				log.Printf("[im] attachment AES decrypt failed pane=%s kind=%s size=%d err=%v",
					shortPaneID(paneID), att.Kind, len(data), err)
				continue
			}
			log.Printf("[im] attachment decrypted pane=%s kind=%s encrypted=%d plain=%d",
				shortPaneID(paneID), att.Kind, len(data), len(plain))
			data = plain
		}
		name := imSafeAttachmentFilename(att.Filename, att.Kind)
		full := filepath.Join(inboxDir, fmt.Sprintf("%s-%d-%s", ts, i, name))
		if err := os.WriteFile(full, data, 0o644); err != nil {
			log.Printf("[im] write attachment failed pane=%s path=%s err=%v",
				shortPaneID(paneID), full, err)
			continue
		}
		// 给 agent 看的是绝对路径 —— 避免 cwd 不匹配 worktree 后缀（如 w-10018 vs w-10018:main.0）
		// 导致 agent 用错误的相对/绝对路径找文件。
		paths = append(paths, full)
		log.Printf("[im] attachment saved pane=%s kind=%s size=%d path=%s",
			shortPaneID(paneID), att.Kind, len(data), full)
	}
	return paths
}

// imRenderAttachmentNote 把已落盘的相对路径渲染成给 agent 看的 user-message 片段。
// 形如：
//
//	[user uploaded files]
//	./.cicy/inbox/20260520T125930-0-photo.jpg
//	./.cicy/inbox/20260520T125930-1-report.pdf
func imRenderAttachmentNote(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("[user uploaded files]\n")
	for _, p := range paths {
		b.WriteString(p)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// imDownloadAttachment 用普通 HTTP GET 下载 attachment URL，限制 50 MB 大小。
func imDownloadAttachment(downloadURL string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, downloadURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "cicy-code/im")
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, io.LimitReader(resp.Body, imAttachmentMaxBytes+1)); err != nil {
		return nil, err
	}
	if buf.Len() > imAttachmentMaxBytes {
		return nil, fmt.Errorf("attachment too large (>%d bytes)", imAttachmentMaxBytes)
	}
	return buf.Bytes(), nil
}

var imAttachmentNameSafe = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// imSafeAttachmentFilename 把 transport 上送的 filename 处理成可安全写盘的字符串：
// 只保留字母 / 数字 / 点 / 下划线 / 横线；去除 path 分隔符。空 → 用 kind 默认值。
func imSafeAttachmentFilename(name, kind string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		switch kind {
		case "image":
			name = "image.jpg"
		case "video":
			name = "video.mp4"
		default:
			name = "file.bin"
		}
	}
	// 取 base，避免 ../path 注入
	name = filepath.Base(name)
	// 替换非法字符
	name = imAttachmentNameSafe.ReplaceAllString(name, "_")
	if name == "" || name == "." || name == ".." {
		name = "file.bin"
	}
	return name
}


// imAESDecryptECB AES-128-ECB + PKCS7 解密。给 transport agnostic — 任何 IM bot
// 协议（WeChat ilink-bot 等）下载下来的加密媒体只要传入 16-byte raw key 都能解密。
func imAESDecryptECB(ciphertext, key []byte) ([]byte, error) {
	if len(key) != 16 {
		return nil, fmt.Errorf("AES-128 key must be 16 bytes, got %d", len(key))
	}
	if len(ciphertext) == 0 || len(ciphertext)%16 != 0 {
		return nil, fmt.Errorf("ciphertext length %d not a multiple of 16", len(ciphertext))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	plain := make([]byte, len(ciphertext))
	for i := 0; i < len(ciphertext); i += 16 {
		block.Decrypt(plain[i:i+16], ciphertext[i:i+16])
	}
	// PKCS7 unpad
	pad := int(plain[len(plain)-1])
	if pad < 1 || pad > 16 {
		return nil, fmt.Errorf("invalid PKCS7 pad byte: %d", pad)
	}
	return plain[:len(plain)-pad], nil
}
