package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strings"
	"syscall"
	"time"

	"ttyd-go/mgr/audit"
	"ttyd-go/mgr/mitm"
	ttydserver "ttyd-go/server"
	"ttyd-go/skillcmd"

	"github.com/gorilla/websocket"
)

var (
	upgrader      = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	publicMode    bool
	devMode       bool
	labMode       bool
	desktopMode   bool
	auditMode     bool
	previewMode   bool
	hotMode       bool
	cdnMode       bool
	containerMode bool
	helperMode    bool // --helper=1 → ships a single headless cicy 团队助手 on w-1001
	desktopCmd    *exec.Cmd
	portFlag      string // --port N / --port=N → overrides PORT env (default 8008)
)

const version = "2.3.70"

// resolvePort returns the effective API port: --port flag > PORT env > 8008.
// Single source of truth so the value pinned into PORT (before worker boot) and
// the value the listener binds to can never diverge.
func resolvePort() string {
	if portFlag != "" {
		return portFlag
	}
	if p := os.Getenv("PORT"); p != "" {
		return p
	}
	return "8008"
}

// agentsFlag holds --agents=hermes,... for non-interactive setup
var agentsFlag string

func main() {
	// OS-specific process setup (Windows: locate the bundled MSYS2 runtime and
	// prepend its usr\bin to PATH so tmux/sh/bash resolve). Before subcommand
	// dispatch — subcommands shell out too.
	initPlatform()
	// Subcommand dispatch — must run before flag parsing.
	if len(os.Args) >= 2 && os.Args[1] == "skill" {
		skillcmd.Run(os.Args[2:])
		return
	}
	if len(os.Args) >= 2 && os.Args[1] == "audit" {
		os.Exit(audit.RunCLI(os.Args[2:]))
	}
	if len(os.Args) >= 2 && os.Args[1] == "mitm" {
		os.Exit(mitm.RunCLI(os.Args[2:]))
	}
	if len(os.Args) >= 2 && (os.Args[1] == "cicy-repl" || os.Args[1] == "dispatcher-repl") {
		os.Exit(runCicyREPL(os.Args[2:])) // "dispatcher-repl" kept as legacy alias
	}
	if len(os.Args) >= 2 && os.Args[1] == "reseed-memory" {
		os.Exit(runReseedMemory(os.Args[2:]))
	}

	// The SERVER must never route its own outbound HTTP through the per-agent
	// MITM proxy. If it was launched from an agent's shell (操作事故 2026-06-05:
	// a restart via an agent pane leaked HTTPS_PROXY=http://w-10064:x@127.0.0.1:1087
	// into the process), every gateway upstream call gets re-intercepted by our
	// own MITM and double-audited under THAT agent's identity — wrong-agent
	// current.json/usage entries and a flood of fresh conversation_ids. Strip
	// any loopback agent-MITM proxy env up front.
	sanitizeAgentMitmProxyEnv()

	cliArgs := os.Args[1:]
	for i := 0; i < len(cliArgs); i++ {
		arg := cliArgs[i]
		switch {
		case arg == "--version" || arg == "-v":
			fmt.Println("cicy-code " + version)
			os.Exit(0)
		case arg == "--help" || arg == "-h":
			fmt.Printf(`cicy-code - AI agent collaboration tool (local, SQLite)

Usage: cicy-code [options]
       cicy-code skill <subcommand> [args]

Subcommands:
  skill list                 List available skills from registry
  skill info <name>          Show skill detail
  skill install <name>       Install a skill
  skill remove <name>        Uninstall a skill
  skill installed            List locally installed skills
  skill --help               Detailed skill help

Options:
  --help, -h              Show this help
  --version, -v           Show version
  --dev                   Development mode
  --preview               Serve app/dist from disk (run 'npm run build' to refresh)
  --hot                   Proxy the UI to the vite dev server on :8022 (HMR)
  --cdn                   Serve the App SPA + ttyd bundle from Cloudflare R2
                          (default: embedded local assets). The R2 version
                          prefixes are baked into every build; this flag only
                          activates them.
  --lab                   Enable lab mode
  --public                Bind 0.0.0.0 (expose to the network). Default is
                          127.0.0.1 (loopback only) — INCLUDING inside
                          containers. Only pass this when you intend to expose
                          the API, and use a strong, non-default api_token.
  --port N                API port (overrides PORT env; default: 8008)
  --audit                 Enable audit mode
  --helper=1              Team-Helper mode: ship a single headless cicy
                          "团队助手" on w-1001 that installs Docker + cicy-code
                          and scales the local team.

	Environment:
	  PORT          API port (default: 8008)
	  SQLITE_PATH   SQLite database file (default: %s)`, defaultSQLitePath())
			os.Exit(0)
		case arg == "--dev":
			devMode = true
		case arg == "--preview":
			previewMode = true
		case arg == "--hot":
			hotMode = true
		case arg == "--cdn":
			cdnMode = true
		case arg == "--lab":
			labMode = true
		case arg == "--public":
			publicMode = true
		case arg == "--audit":
			auditMode = true
			os.Setenv("AUDIT_MODE", "1")
		case arg == "--helper" || arg == "--helper=1":
			helperMode = true
			os.Setenv("CICY_HELPER", "1")
		case arg == "--desktop" || arg == "--desktop=1":
			// Launched by cicy-desktop (vs a plain server / container cicy-code).
			desktopMode = true
		case arg == "--port":
			// space form: --port 8208
			if i+1 < len(cliArgs) {
				portFlag = cliArgs[i+1]
				i++
			}
		case strings.HasPrefix(arg, "--port="):
			portFlag = strings.TrimPrefix(arg, "--port=")
		}
	}

	// --cdn activates the baked-in R2 prefixes for the ttyd bundle (the App SPA
	// is handled in serveUI via cdnMode). Off → ttydStaticPrefix() returns ".".
	ttydserver.CDNEnabled = cdnMode

	// Pin PORT to the port THIS instance listens on, BEFORE any worker-booting
	// path (startWatcher/startAutonomy below) regenerates pane boot.sh. Every
	// runtime base-url derivation (runtimeAPIBasePort → ai-gateway / reply-history
	// URLs, the CICY_API_PORT exported for cicy-agent) reads PORT; doing this only
	// at the listener setup further down is TOO LATE — boot.sh would already be
	// written with the 8008 fallback, routing a `--port 8208` instance's LLM +
	// agent traffic into the host instance on 8008.
	os.Setenv("PORT", resolvePort())

	ensureRuntimeUnprivileged()
	initKV()
	initRedis()
	initDB()
	store.Migrate()
	defer store.Close()

	// Ensure the file-backed team knowledge store skeleton exists so it shows up
	// as an fs root in the FileExplorer even before the first entry is written.
	if err := knowledgeEnsureRoot(); err != nil {
		log.Printf("[knowledge] ensure root: %v", err)
	}

	// Audit is always on — there is no master off-switch. The pipeline
	// initializes unconditionally; collection + scanning are always active.
	if err := audit.Init(); err != nil {
		log.Printf("[audit] init failed: %v", err)
	} else {
		log.Printf("[audit] init ok — collection + scanning active")
	}
	ensureMITMConfig() // seed ~/cicy-ai/mitm/config.json (enabled) before startMITM reads it
	startMITM()
	ensureMITMCAInSystemTrust() // trust the (now-generated) MITM CA for codex/kiro Rust TLS
	startAutonomy()

	// containerMode is still tracked for runtimeMode reporting, but it no longer
	// forces a public bind: the listener defaults to 127.0.0.1 everywhere
	// (including containers). Exposing the API to the network is now an explicit
	// opt-in via --public. To reach a container's loopback-bound API from the
	// host, port-forward to 127.0.0.1 inside the container, or pass --public
	// (with a strong api_token) when you genuinely want it on the network.
	containerMode = isContainerRuntime()
	checkEnv()

	go startWatcher()
	go startTmuxHealth()
	go startDesktopSnapshots()
	startSystemResourceMonitor()
	if _, err := syncMachinesFromConfig(); err != nil {
		log.Printf("[machines] initial sync error: %v", err)
	}

	// Health
	http.HandleFunc("/health", w(handleHealth))
	http.HandleFunc("/api/health", w(handleHealth))
	http.HandleFunc("/api/ping", w(handlePing))
	http.HandleFunc("/api/poll", authM(handlePoll))
	http.HandleFunc("/api/stt", authM(handleSTT))

	// Auth — local token management
	http.HandleFunc("/api/auth/verify", w(handleAuthVerify))
	http.HandleFunc("/api/auth/verify-token", w(handleAuthVerifyToken))
	http.HandleFunc("/api/auth/tokens", wa(handleAuthTokens))
	http.HandleFunc("/api/auth/tokens/", wa(handleAuthTokenDelete))

	// Proxy (mihomo controller pass-through + lifecycle)
	http.HandleFunc("/api/proxy/list", authM(handleProxyList))
	http.HandleFunc("/api/proxy/test", authM(handleProxyTest))
	http.HandleFunc("/api/proxy/status", authM(handleProxyStatus))
	http.HandleFunc("/api/proxy/lifecycle", authM(handleProxyLifecycle))
	http.HandleFunc("/api/proxy/bind-mode", authM(handleProxyBindMode))
	http.HandleFunc("/api/proxy/export", authM(handleProxyExport))
	http.HandleFunc("/api/proxy/exit-info", authM(handleProxyExitInfo))
	http.HandleFunc("/api/proxy/select", authM(handleProxySelect))
	http.HandleFunc("/api/proxy/node-config", authM(handleProxyNodeConfig))
	http.HandleFunc("/api/proxy/config/reset", authM(handleProxyConfigReset))
	http.HandleFunc("/api/proxy-ssh/list", authM(handleProxySshList))
	http.HandleFunc("/api/proxy-ssh/show", authM(handleProxySshShow))
	http.HandleFunc("/api/proxy-ssh/lifecycle", authM(handleProxySshLifecycle))
	http.HandleFunc("/api/proxy-ssh/test", authM(handleProxySshTest))
	http.HandleFunc("/api/frp-server/status", authM(handleFrpServerStatus))
	http.HandleFunc("/api/frp-server/lifecycle", authM(handleFrpServerLifecycle))
	http.HandleFunc("/api/frp-server/connections", authM(handleFrpServerConnections))
	http.HandleFunc("/api/frp-server/clients", authM(handleFrpServerClients))
	http.HandleFunc("/api/frp-server/logs", authM(handleFrpServerLogs))
	http.HandleFunc("/api/frp-server/install-info", authM(handleFrpServerInstallInfo))

	// Audit — forensic event viewer + policy editor + mitmproxy ingest webhook.
	// (Autonomy decision routes are registered separately in startAutonomy.)
	http.HandleFunc("/api/audit/events", wa(handleAuditEvents))
	http.HandleFunc("/api/audit/events/", wa(handleAuditEventByID))
	http.HandleFunc("/api/audit/triage", wa(handleAuditTriage))
	http.HandleFunc("/api/audit/snapshot", wa(handleAuditSnapshot))
	http.HandleFunc("/api/audit/stats", wa(handleAuditStats))
	http.HandleFunc("/api/audit/rules", wa(handleAuditRules))
	http.HandleFunc("/api/audit/rules/test", wa(handleAuditRulesTest))
	http.HandleFunc("/api/audit/agents", wa(handleAuditAgents))
	http.HandleFunc("/api/audit/ingest", wa(handleAuditIngest))
	http.HandleFunc("/api/audit/allowlist/content", wa(handleAuditAllowlistContent))
	http.HandleFunc("/api/audit/allowlist", wa(handleAuditAllowlist))
	http.HandleFunc("/api/audit/policy", wa(handleAuditPolicyGlobal))
	http.HandleFunc("/api/audit/policy/agents/", wa(handleAuditPolicyAgent))
	http.HandleFunc("/api/audit/policy/effective/", wa(handleAuditPolicyEffective))
	http.HandleFunc("/api/audit/ack", w(handleAuditAck)) // public: the HMAC-signed token is the auth
	http.HandleFunc("/api/audit/readiness", wa(handleAuditReadiness))
	http.HandleFunc("/api/audit/notify", wa(handleAuditNotify))
	http.HandleFunc("/api/audit/channels/test", wa(handleAuditChannelsTest))
	http.HandleFunc("/api/im/wechat/prompt", wa(handleWeChatBindPrompt))

	// Panes
	http.HandleFunc("/api/panes", authM(handlePanes))
	http.HandleFunc("/api/panes/create", authM(handleCreatePane))
	http.HandleFunc("/api/panes/", authM(handlePaneByID))
	http.HandleFunc("/api/panes/restart-all", authM(handleRestartAll))
	// Legacy panes routes (frontend compatibility)
	http.HandleFunc("/api/tmux/panes", authM(handlePanes))
	http.HandleFunc("/api/tmux/panes/", authM(handlePaneByID))
	http.HandleFunc("/api/tmux/create", authM(handleCreatePane))
	http.HandleFunc("/api/tmux/fork", authM(handleForkPane))
	http.HandleFunc("/api/tmux/fork/preview", authM(handleForkPreview))
	http.HandleFunc("/api/tmux/restart_all", authM(handleRestartAll))

	// Tmux
	http.HandleFunc("/api/tmux/send", authM(handleSend))
	http.HandleFunc("/api/tmux/send-keys", authM(handleSendKeys))
	http.HandleFunc("/api/cicy/cancel", authM(handleCicyCancel)) // 打断 headless cicy 正在跑的 turn
	http.HandleFunc("/api/cicy/retry", authM(handleCicyRetry))   // 重跑 headless cicy 最近一次取消/失败的 turn
	http.HandleFunc("/api/cicy/clear", authM(handleCicyClear))   // 清空 headless cicy 会话(内存+conversation.json+快照)
	http.HandleFunc("/api/tmux/reply_text", authM(handleAgentReplyText))
	http.HandleFunc("/api/tmux/chat_history", authM(handleAgentChatHistory))
	http.HandleFunc("/api/agent/messages", authM(handleAgentMessages)) // cross-agent message link view (JOIN history_turns)
	http.HandleFunc("/api/knowledge", authM(handleKnowledge))          // team knowledge Layer 2 store: GET list/recall, POST add
	http.HandleFunc("/api/knowledge/", authM(handleKnowledgeByID))     // GET one / PATCH promote|reject|supersede
	http.HandleFunc("/api/tmux/client-trace", authM(handleTmuxClientTrace))
	// http.HandleFunc("/api/tmux/send_wait", authM(handleSendWait)) // TODO: implement handleSendWait
	http.HandleFunc("/api/tmux/capture", authM(handleCapture))
	http.HandleFunc("/api/tmux/windows", authM(handleWindows))
	http.HandleFunc("/api/tmux/tree", authM(handleTree))
	http.HandleFunc("/api/tmux/status", authM(handleStatus))
	http.HandleFunc("/api/tmux/mouse", authM(handleMouseToggle))
	http.HandleFunc("/api/tmux/mouse/on", authM(handleMouseToggle))
	http.HandleFunc("/api/tmux/mouse/off", authM(handleMouseToggle))
	http.HandleFunc("/api/tmux/mouse/status", authM(handleMouseStatus))
	http.HandleFunc("/api/tmux/ttyd/status", authM(handleTtydStatus))
	http.HandleFunc("/api/tmux/ttyd/status/", authM(handleTtydStatus))
	http.HandleFunc("/api/tmux/list", authM(handleTmuxList))
	http.HandleFunc("/api/tmux/clear", authM(handleClear))
	http.HandleFunc("/api/tmux/capture_pane", authM(handleCapture))

	// Chat
	http.HandleFunc("/api/chat/push", wa(handleChatPush))
	http.HandleFunc("/api/chat/ping-client", wa(handleChatPingClient))
	http.HandleFunc("/api/chat/exec-js", wa(handleChatExecJS))
	http.HandleFunc("/api/chat/code-open", wa(handleChatCodeOpen))
	http.HandleFunc("/api/chat/ws", handleChatWS)
	http.HandleFunc("/api/chat/clients", wa(handleWsClients))
	http.HandleFunc("/api/chat/debug", wa(handleChatDebug))
	http.HandleFunc("/api/chat/webhook", corsM(handleChatWebhook))
	http.HandleFunc("/api/openclaw/message/send", wa(handleOpenClawMessageSend))

	// Desktop snapshots (periodic win/mac/linux screen captures → 桌面 tab)
	http.HandleFunc("/api/desktop/snapshots", wa(handleDesktopSnapshots))
	http.HandleFunc("/api/desktop/snapshot-image", wa(handleDesktopSnapshotImage))
	http.HandleFunc("/api/desktop/snapshot-now", wa(handleDesktopSnapshotNow))

	// Native files (replaces code-server file viewer/editor; see docs/native-files-plan.md)
	http.HandleFunc("/api/fs/roots", wa(handleFsRoots))
	http.HandleFunc("/api/fs/list", wa(handleFsList))
	http.HandleFunc("/api/fs/read", wa(handleFsRead))
	http.HandleFunc("/api/fs/write", wa(handleFsWrite))
	http.HandleFunc("/api/fs/stat", wa(handleFsStat))
	http.HandleFunc("/api/fs/download", authM(handleFsDownload))
	http.HandleFunc("/api/fs/upload", authM(handleFsUpload))
	http.HandleFunc("/api/fs/send-path", wa(handleFsSendPath))
	http.HandleFunc("/api/fs/rename", wa(handleFsRename))
	http.HandleFunc("/api/fs/delete", wa(handleFsDelete))
	http.HandleFunc("/api/fs/mkdir", wa(handleFsMkdir))
	http.HandleFunc("/api/fs/touch", wa(handleFsTouch))
	http.HandleFunc("/api/fs/favorites/list", wa(handleFsFavoritesList))
	http.HandleFunc("/api/fs/favorites/add", wa(handleFsFavoritesAdd))
	http.HandleFunc("/api/fs/favorites/remove", wa(handleFsFavoritesRemove))
	http.HandleFunc("/api/fs/search", wa(handleFsSearch))
	http.HandleFunc("/api/fs/grep", wa(handleFsGrep))
	http.HandleFunc("/api/fs/diff", wa(handleFsDiff))
	http.HandleFunc("/api/fs/watch", authM(handleFsWatch))
	http.HandleFunc("/api/runtime/flags", wa(handleRuntimeFlags))

	// Stats
	http.HandleFunc("/api/system/resources", wa(handleSystemResources))

	// Notifications
	http.HandleFunc("/api/notify", wa(handleNotify))
	http.HandleFunc("/api/cicy/files", wa(handleCicyFiles))
	http.HandleFunc("/api/cicy/file", wa(handleCicyFile))
	http.HandleFunc("/assets/files", wa(handleAssetFileUpload))
	// GET 取文件公开(不带 cicy token):文件名自带 64-bit 随机前缀,不可猜的随机路径即
	// capability。这样附件 URL(进聊天消息、发给模型)不含任何 secret,出站审计不会把它当
	// cicy token 拦截;<img> 也能直接加载(无需在 URL 里塞 API token)。上传(POST)仍需认证。
	http.HandleFunc("/assets/files/", corsM(handleAssetFile))
	http.HandleFunc("/api/notify/stream", corsM(func(w http.ResponseWriter, r *http.Request) {
		t := r.URL.Query().Get("token")
		if t == "" || !verifyToken(t) {
			httpErr(w, 401, "Not authenticated")
			return
		}
		handleNotifyStream(w, r)
	}))

	// Memory templates (global + project, backs create-agent dialog)
	http.HandleFunc("/api/memory/templates", wa(handleMemoryTemplates))
	http.HandleFunc("/api/memory/templates/", wa(handleMemoryTemplateByName))
	// Projects (first-class: name + dir + rules; per-project shared claude memory)
	http.HandleFunc("/api/projects", wa(handleProjects))

	// Custom agents (user-authored cicy personas, ~/cicy-ai/agents/<slug>/AGENT.md)
	http.HandleFunc("/api/custom-agents", wa(handleCustomAgents))
	http.HandleFunc("/api/custom-agents/", wa(handleCustomAgentAction))

	// Todo
	http.HandleFunc("/api/todo/list", wa(handleTodoList))
	http.HandleFunc("/api/todo/add", wa(handleTodoAdd))
	http.HandleFunc("/api/todo/counts", wa(handleTodoCounts))
	http.HandleFunc("/api/todo/", wa(handleTodoByID))

	// Queue
	http.HandleFunc("/api/queue", wa(handleQueue))
	http.HandleFunc("/api/queue/push", wa(handleQueuePush))
	http.HandleFunc("/api/queue/list", wa(handleQueueList))
	http.HandleFunc("/api/queue/", wa(handleQueueByID))
	// Legacy queue routes
	http.HandleFunc("/api/workers/queue", wa(handleQueue))
	http.HandleFunc("/api/workers/queue/", wa(handleQueueByID))

	// Agents
	http.HandleFunc("/api/agents/by-pane", wa(handleAgentsByPane))
	http.HandleFunc("/api/agents/by-pane/", wa(handleAgentsByPane))
	http.HandleFunc("/api/agents/pane/", wa(handleAgentsByPane))
	http.HandleFunc("/api/agents/inspector/", wa(handleAgentInspectorByPane))
	http.HandleFunc("/api/agents/greeting/", wa(handleAgentGreeting))
	http.HandleFunc("/api/agents/install-status", authM(handleAgentInstallStatus)) // is the coding CLI installed?
	http.HandleFunc("/api/agents/install", authM(handleAgentInstall))              // SSE: run the install
	http.HandleFunc("/api/agents/current-history/", wa(handleAgentCurrentHistoryByPane))
	http.HandleFunc("/api/agents/usage-log/", wa(handleAgentUsageLogByPane))
	http.HandleFunc("/api/agents/usage-analysis/", wa(handleAgentUsageAnalysisByPane))
	http.HandleFunc("/api/agents/usage-block/", wa(handleAgentUsageBlockByPane))
	http.HandleFunc("/api/agents/current-history-tool/", wa(handleAgentCurrentHistoryToolDetailByPane))
	http.HandleFunc("/api/agents/history-ids/", wa(handleAgentHistoryIDsByPane))
	http.HandleFunc("/api/agents/current-reply/", wa(handleAgentCurrentReplyByPane))
	http.HandleFunc("/api/agents/current-reply-batch", wa(handleAgentCurrentReplyBatch))
	http.HandleFunc("/api/agents/history-turn/", wa(handleAgentHistoryTurnByPane))
	http.HandleFunc("/api/agents/history-sync/", wa(handleAgentHistorySyncByPane))
	http.HandleFunc("/api/agents/history-view/", wa(handleAgentHistoryViewByPane))
	http.HandleFunc("/api/agents/bind", wa(handleAgentBind))
	http.HandleFunc("/api/agents/unbind", wa(handleAgentUnbind))
	http.HandleFunc("/api/agents/unbind/", wa(handleAgentUnbind))
	http.HandleFunc("/api/agents/reorder", wa(handleAgentReorder))
	http.HandleFunc("/api/agents/reparent", wa(handleAgentReparent))

	// Groups
	http.HandleFunc("/api/groups", wa(handleGroups))
	http.HandleFunc("/api/groups/", wa(handleGroupByID))

	// Nodes / Machines
	http.HandleFunc("/api/nodes", wa(handleNodes))
	http.HandleFunc("/api/nodes/exec", wa(handleNodeExec))
	http.HandleFunc("/api/machines", wa(handleMachines))
	http.HandleFunc("/api/machines/register", wa(handleMachineRegister))
	http.HandleFunc("/api/machines/sync", wa(handleMachineSync))
	http.HandleFunc("/api/machines/", wa(handleMachinePanes))

	// Collab / Skills
	http.HandleFunc("/api/collab/steps", wa(handleCollabSteps))
	http.HandleFunc("/api/collab/steps/", wa(handleCollabStepByID))
	http.HandleFunc("/api/collab/workflows", wa(handleCollabWorkflows))
	http.HandleFunc("/api/collab/workflows/", wa(handleCollabWorkflowByID))
	http.HandleFunc("/api/skills", wa(handleSkills))
	http.HandleFunc("/api/skills/run", wa(handleSkillRun))
	http.HandleFunc("/api/skill-market", wa(handleSkillMarketList))
	http.HandleFunc("/api/skill-market/", wa(handleSkillMarketAction))
	http.HandleFunc("/api/skill-registries", wa(handleSkillRegistries))
	http.HandleFunc("/api/skill-registries/", wa(handleSkillRegistries))
	http.HandleFunc("/api/local-registry", wa(handleLocalRegistry))
	http.HandleFunc("/api/local-registry/", wa(handleLocalRegistry))
	// Private registry mounted on the daemon's own port (no daemon auth wrapper;
	// it enforces its own read token). Share "<daemon URL>/registry" + token.
	http.HandleFunc(localRegMountPrefix+"/", serveLocalRegistry)
	http.HandleFunc("/api/skill-config/google", wa(handleGoogleSkillConfig))
	http.HandleFunc("/api/skill-config/google/connect", wa(handleGoogleSkillConfig))
	http.HandleFunc("/api/skill-config/google/device-connect", wa(handleGoogleSkillConfig))
	http.HandleFunc("/api/skill-config/google/device-poll", wa(handleGoogleSkillConfig))
	// Callback is intentionally unauthed — Google won't carry our Bearer token.
	// State token enforces same-origin/no-CSRF.
	http.HandleFunc("/api/skill-config/google/callback", handleGoogleSkillCallback)

	// Runtime aliases
	http.HandleFunc("/api/runtime/instances", wa(handleRuntimeInstances))
	http.HandleFunc("/api/runtime/instances/register", wa(handleRuntimeInstanceRegister))
	http.HandleFunc("/api/runtime/sessions/", wa(handleRuntimeSessionEvents))
	http.HandleFunc("/api/runtime/tasks", wa(handleRuntimeTasks))
	http.HandleFunc("/api/runtime/tasks/", wa(handleRuntimeTaskByID))
	http.HandleFunc("/api/runtime/artifacts", wa(handleRuntimeArtifacts))

	// Shared workspace bridge
	http.HandleFunc("/api/shared-workspace", wa(handleSharedWorkspace))
	http.HandleFunc("/api/shared-workspace/work-items", wa(handleSharedWorkItems))
	http.HandleFunc("/api/shared-workspace/work-items/", wa(handleSharedWorkItems))
	http.HandleFunc("/api/shared-workspace/artifacts", wa(handleSharedArtifacts))
	http.HandleFunc("/api/shared-workspace/artifacts/", wa(handleSharedArtifacts))
	http.HandleFunc("/api/shared-workspace/handoffs", wa(handleSharedHandoffs))
	http.HandleFunc("/api/shared-workspace/handoffs/", wa(handleSharedHandoffs))

	// Settings
	http.HandleFunc("/api/settings", wa(handleSettings))
	http.HandleFunc("/api/settings/global", wa(handleSettings))
	// Settings → General: email (SMTP) config + API-token show/rotate-and-email.
	http.HandleFunc("/api/settings/email", wa(handleEmailConfig))
	http.HandleFunc("/api/settings/token", wa(handleTokenShow))
	http.HandleFunc("/api/settings/token/refresh", wa(handleTokenRefresh))

	// AI providers (global.json)
	http.HandleFunc("/api/providers", wa(handleProviders))
	http.HandleFunc("/api/providers/", wa(handleProvidersSub))

	// IM platforms (Telegram / WeChat)
	http.HandleFunc("/api/im/", wa(handleIMRoute))

	http.HandleFunc("/api/file-exists", wa(handleFileExists))
	http.HandleFunc("/api/utils/file/exists", wa(handleFileExists))
	http.HandleFunc("/api/utils/translateText", wa(handleTranslateText))
	http.HandleFunc("/api/correctEnglish", wa(handleCorrectEnglish))

	// TTS
	http.HandleFunc("/api/tts", wa(handleTTS))

	// Telegram (notify-only)
	http.HandleFunc("/api/tg/send", wa(handleTGSend))
	http.HandleFunc("/api/tg/photo", wa(handleTGPhoto))

	// Pair
	http.HandleFunc("/api/pair", apiOnlyUnsupported(handlePair))
	http.HandleFunc("/api/tmux/pair", apiOnlyUnsupported(handlePair))

	http.HandleFunc("/api/openclaw/gateway", wa(handleOpenClawGatewayInfo))
	http.HandleFunc("/api/openclaw/provider/", handleOpenClawProviderProxy)
	// More specific than the "/api/ai-gateway/" proxy prefix below, so it wins.
	http.HandleFunc("/api/ai-gateway/provider-balance", authM(handleProviderBalance))
	http.HandleFunc("/api/ai-gateway/", handleAIGatewayProxy)
	http.HandleFunc("/api/cicy/chat", handleCicyChat)       // loopback-only, like the AI gateway
	http.HandleFunc("/api/dispatcher/chat", handleCicyChat) // legacy alias (kept for in-flight REPLs)
	http.HandleFunc("/api/cicy/history", handleCicyHistory) // loopback-only, read conversation.json (replaces tmux capture)
	http.HandleFunc("/mitm/", handleMitmproxyAuth)
	http.HandleFunc("/mitm", handleMitmproxyAuth)
	http.HandleFunc("/openclaw/", handleOpenClawAuth)
	http.HandleFunc("/openclaw", handleOpenClawAuth)

	// In-process ttyd terminals (no per-pane port; see ttyd_inline.go)
	http.HandleFunc("/ttyd/", handleTtydProxy)
	http.HandleFunc("/ttyd-shell/", handleTtydShellProxy)

	// UI (SPA)
	http.Handle("/", serveUI())

	// Port precedence: --port flag > PORT env > 8008 default. Already pinned into
	// PORT near the top of main (before worker boot); resolvePort() is the single
	// source of truth.
	port := resolvePort()

	kvMode := "memory"
	if useRedis {
		kvMode = "redis"
	} else if kv.file != "" {
		kvMode = "file:" + kv.file
	}
	runtimeMode := "local"
	if containerMode {
		runtimeMode = "container"
	}
	log.Printf("[startup] mode=%s port=%s db=%s kv=%s", runtimeMode, port, store.Driver, kvMode)

	// Headless cicy: warm every local cicy agent's server-side session so they're
	// online and message-ready without a tmux pane.
	warmCicySessions()

	// Hook: thinking → idle
	RegisterHook(func(paneID string, old, new paneSt) {
		if old.Status != nil && *old.Status == "thinking" && new.Status != nil && *new.Status == "idle" {
			go dispatchQueue(paneID)

			shortPane := shortPaneID(paneID)
			rows, err := store.Query(`SELECT pa.pane_id FROM pane_agents pa WHERE pa.agent_name=? AND pa.status='active'`, shortPane)
			if err != nil {
				return
			}
			defer rows.Close()
			for rows.Next() {
				var masterPane string
				rows.Scan(&masterPane)
				hub.broadcast(masterPane, ChatEvent{
					Type: "worker_idle",
					Data: M{
						"protocol": "cicy/v1",
						"from":     shortPane,
						"type":     "task_result",
						"data": M{
							"worker":  shortPane,
							"status":  "idle",
							"message": fmt.Sprintf("Worker %s finished (thinking → idle)", shortPane),
						},
					},
				})
				log.Printf("[hook] notified master %s (chatbus): worker %s idle", masterPane, shortPane)
			}
		}
	})

	go syncTelegramPollers()
	go imManagerStart()
	// Repair <parent>/workers/<child> symlinks on every boot. Without this
	// they only get rebuilt on bind/unbind, so a corrupted/missing link
	// (or one written to the wrong workersDir because workspace was wrong)
	// would persist indefinitely. Cheap idempotent walk: skip when target
	// already matches; remove stale managed links whose binding is gone.
	go func() {
		if err := syncAllBoundAgentWorkspaceLinks(); err != nil {
			log.Printf("[startup] syncAllBoundAgentWorkspaceLinks failed: %v", err)
		}
	}()

	bind := "127.0.0.1"
	if publicMode { // --public → expose on all interfaces; default stays loopback
		bind = "0.0.0.0"
		log.Printf("[startup] WARNING: --public binds 0.0.0.0 (network-exposed). Ensure a strong, non-default api_token is set.")
	}
	log.Printf("cicy-code starting on %s:%s", bind, port)
	token := getFirstToken()
	openHost := bind
	if openHost == "0.0.0.0" {
		openHost = "127.0.0.1"
	}
	openURL := fmt.Sprintf("http://%s:%s/?token=%s", openHost, port, token)
	log.Printf("")
	log.Printf("============================================================")
	log.Printf("")
	log.Printf("  >>> CICY CODE v%s <<<", version)
	log.Printf("============================================================")
	log.Printf("  Token: %s", token)
	// Actual listen address — 0.0.0.0:port under --public (network-exposed),
	// 127.0.0.1:port otherwise (loopback). Shown ABOVE the URL so you can tell at
	// a glance whether this instance is public; the URL line below stays 127.0.0.1
	// because that's the address you click locally even when bound to 0.0.0.0.
	log.Printf("  Listen: %s:%s%s", bind, port, map[bool]string{true: "  (--public)", false: "  (loopback)"}[publicMode])
	log.Printf("  URL:   %s", openURL)
	log.Printf("============================================================")
	log.Printf("")
	go func() {
		if os.Getenv("CICY_NO_BROWSER") == "1" {
			return
		}
		// if err := openDefaultBrowser(openURL); err != nil {
		// 	log.Printf("[startup] open browser failed: %v", err)
		// } else {
		// 	log.Printf("[startup] browser opened")
		// }
	}()
	if auditMode {
		log.Printf("[startup] audit mode enabled")
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	signal.Ignore(syscall.SIGHUP) // ignore SIGHUP when parent terminal closes
	go func() {
		<-sigCh
		log.Println("[shutdown] stopping...")
		os.Exit(0)
	}()

	ensureLocalRegistry() // always-on self-hosted skill registry at /registry

	// Every turn (cicy in-process or CLI-via-gateway) is audited by THIS daemon,
	// so after a restart no prior turn can still be streaming. Finalize any
	// reply.json left non-terminal by the previous process — without this its
	// agent shows busy (yellow) in the TeamPanel and never completes. Runs
	// synchronously BEFORE listen: no new turn can be writing yet, so the sweep
	// can't race a live one.
	aiGatewaySweepStaleReplies()

	// Private heap/goroutine profiling endpoint for leak diagnosis. Bound to
	// 127.0.0.1 ONLY — never the public listener, so it is unreachable via :8008
	// or any tunnel. Uses runtime/pprof (not net/http/pprof) so DefaultServeMux
	// (the public mux) is never polluted with /debug/pprof handlers.
	//   curl 127.0.0.1:6060/heap > heap.prof && go tool pprof heap.prof
	// Port defaults to 6060; override with CICY_PPROF_PORT so a second instance on
	// the same machine doesn't collide (set it to "off"/"0" to disable entirely).
	pprofPort := strings.TrimSpace(os.Getenv("CICY_PPROF_PORT"))
	if pprofPort == "" {
		pprofPort = "6060"
	}
	if pprofPort != "off" && pprofPort != "0" {
		go func() {
			mux := http.NewServeMux()
			mux.HandleFunc("/heap", func(w http.ResponseWriter, r *http.Request) {
				runtime.GC()
				_ = pprof.WriteHeapProfile(w)
			})
			mux.HandleFunc("/goroutine", func(w http.ResponseWriter, r *http.Request) {
				_ = pprof.Lookup("goroutine").WriteTo(w, 0)
			})
			mux.HandleFunc("/allocs", func(w http.ResponseWriter, r *http.Request) {
				_ = pprof.Lookup("allocs").WriteTo(w, 0)
			})
			_ = http.ListenAndServe("127.0.0.1:"+pprofPort, mux)
		}()
	}

	log.Fatal(http.ListenAndServe(bind+":"+port, globalCORS(withGzip(http.DefaultServeMux))))
}

func getFirstToken() string {
	gpath := cicyGlobalJSONPath
	if token := strings.TrimSpace(loadAPIToken()); token != "" {
		_, _ = ensureGlobalAPIToken(gpath, token)
		return token
	}
	token, err := ensureGlobalAPIToken(gpath)
	if err == nil && strings.TrimSpace(token) != "" {
		return token
	}
	b := make([]byte, 16)
	rand.Read(b)
	token = "cicy_" + hex.EncodeToString(b)
	_, _ = ensureGlobalAPIToken(gpath, token)
	return token
}

func ensureGlobalAPIToken(globalPath string, preferredToken ...string) (string, error) {
	cfg := map[string]interface{}{}
	if data, err := os.ReadFile(globalPath); err == nil {
		if strings.TrimSpace(string(data)) != "" {
			if err := json.Unmarshal(data, &cfg); err != nil {
				return "", err
			}
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}

	currentToken := ""
	if t, ok := cfg["api_token"].(string); ok && strings.TrimSpace(t) != "" {
		currentToken = strings.TrimSpace(t)
	}

	token := ""
	if len(preferredToken) > 0 {
		token = strings.TrimSpace(preferredToken[0])
	}
	if token == "" {
		token = currentToken
	}
	if token == "" {
		b := make([]byte, 16)
		rand.Read(b)
		token = "cicy_" + hex.EncodeToString(b)
	}
	cfg["api_token"] = token

	if err := os.MkdirAll(filepath.Dir(globalPath), 0755); err != nil {
		return "", err
	}
	if currentToken == token {
		return token, nil
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", err
	}
	data = append(data, '\n')
	tmpPath := globalPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return "", err
	}
	if err := os.Rename(tmpPath, globalPath); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}
	return token, nil
}

func isContainerRuntime() bool {
	// Intrinsic container detection — the CICY_RUNTIME_KIND env var was retired
	// with the team-bootstrap/master model. Docker images carry /.dockerenv (same
	// signal used in mitm/consent.go); serverless platforms set K_SERVICE.
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	return strings.TrimSpace(os.Getenv("K_SERVICE")) != ""
}

func globalCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if o := r.Header.Get("Origin"); o != "" {
			w.Header().Set("Access-Control-Allow-Origin", o)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Accept")
		}
		if r.Method == "OPTIONS" {
			w.WriteHeader(204)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func w(h http.HandlerFunc) http.HandlerFunc  { return corsM(h) }
func wa(h http.HandlerFunc) http.HandlerFunc { return corsM(authM(h)) }

// processStartedAt is captured at startup so /api/health can report uptime.
var processStartedAt = time.Now()

// handleHealth surfaces what the cicy-desktop homepage card needs to decide
// "is the local engine healthy and what's it doing right now": version,
// uptime, a resident-size proxy via runtime.MemStats.Sys, and a quick agent
// count via listAgentsByPane (same query the chat panel uses). Stays public
// — no auth, no token — because cicy-desktop probes it before the user has
// authenticated against any backend.
func handleHealth(w http.ResponseWriter, r *http.Request) {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	agentsCount := -1
	if rows, err := listAgentsByPane("all"); err == nil {
		agentsCount = len(rows)
	}
	J(w, M{
		"status":       "ok",
		"source":       "cicy-code",
		"version":      version,
		"pid":          os.Getpid(),
		"uptime_sec":   int(time.Since(processStartedAt).Seconds()),
		"mem_bytes":    ms.Sys,
		"goroutines":   runtime.NumGoroutine(),
		"agents_count": agentsCount,
	})
}

func handlePing(w http.ResponseWriter, r *http.Request) {
	J(w, M{"status": "ok", "version": version, "source": "cicy-code"})
}

func openDefaultBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
