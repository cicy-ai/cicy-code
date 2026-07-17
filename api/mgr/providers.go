// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// providersFileMu serializes read-modify-write cycles on ~/cicy-ai/global.json.
var providersFileMu sync.Mutex

var providerKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// agent types exposed in the provider manager defaults editor.
var providerDefaultAgentTypes = []string{"claude", "cicy", "codex", "opencode"}

// Official cicy-shipped providers — protected from deletion via API.
var protectedProviderKeys = map[string]bool{
	"defaultAnthropic": true,
	"defaultOpenAi":    true,
}

func isProtectedProviderKey(key string) bool {
	return protectedProviderKeys[strings.TrimSpace(key)]
}

func writeGlobalJSONConfig(cfg map[string]any) error {
	path := globalJSONPath()
	if path == "" {
		return fmt.Errorf("global.json path not configured")
	}
	body, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, append(body, '\n'), 0600); err != nil { // holds api_token + registry token
		return err
	}
	return os.Rename(tmpPath, path)
}

// defaultProvidersBlockJSON is the providers block seeded into global.json the
// first time an instance boots without any configured providers — it wires
// claude/codex/opencode to the CiCyAi gateway by default.
const defaultProvidersBlockJSON = `{
  "default": {
    "claude": "deepseek_claude",
    "cicy": "opencodeZen",
    "codex": "opencodeZen",
    "opencode": "opencodeZen",
    "stt": "groqStt",
    "vision": "zhipuVision",
    "voice": "doubaoVoice"
  },
  "items": [
    {
      "key": "defaultAnthropic",
      "name": "CiCyAi",
      "apiKey": "",
      "defaultModel": "deepseek-v4-pro",
      "models": ["deepseek-v4-pro", "deepseek-v4-flash"],
      "protocol": "anthropic",
      "url": "https://gateway.cicy-ai.com"
    },
    {
      "key": "defaultOpenAi",
      "name": "CiCyAi",
      "apiKey": "",
      "defaultModel": "deepseek-v4-pro",
      "models": ["deepseek-v4-pro", "deepseek-v4-flash"],
      "protocol": "openai",
      "url": "https://gateway.cicy-ai.com"
    },
    {
      "key": "deepseek_claude",
      "name": "DeepSeek",
      "apiKey": "",
      "defaultModel": "deepseek-v4-pro",
      "models": ["deepseek-v4-pro", "deepseek-v4-flash"],
      "protocol": "anthropic",
      "url": "https://api.deepseek.com/anthropic"
    },
    {
      "key": "deepseek",
      "name": "DeepSeek",
      "apiKey": "",
      "defaultModel": "deepseek-v4-pro",
      "models": ["deepseek-v4-pro", "deepseek-v4-flash"],
      "protocol": "openai",
      "url": "https://api.deepseek.com"
    },
    {
      "key": "groqStt",
      "name": "Groq Whisper",
      "apiKey": "",
      "defaultModel": "whisper-large-v3-turbo",
      "models": ["whisper-large-v3", "whisper-large-v3-turbo"],
      "protocol": "openai",
      "url": "https://api.groq.com/openai/v1"
    },
    {
      "key": "zhipuVision",
      "name": "智谱 GLM Vision",
      "apiKey": "",
      "defaultModel": "glm-4v-flash",
      "models": ["glm-4v-flash", "glm-4.6v-flash", "glm-4.1v-thinking-flash"],
      "protocol": "openai",
      "url": "https://open.bigmodel.cn/api/paas/v4"
    },
    {
      "key": "doubaoVoice",
      "name": "豆包语音",
      "apiKey": "",
      "defaultModel": "zh_female_shuangkuaisisi_uranus_bigtts",
      "models": ["zh_female_shuangkuaisisi_uranus_bigtts", "zh_female_tianmeixiaoyuan_uranus_bigtts", "zh_female_cancan_uranus_bigtts", "zh_female_xiaohe_uranus_bigtts", "zh_female_vv_uranus_bigtts", "zh_male_taocheng_uranus_bigtts"],
      "protocol": "voice",
      "url": "https://openspeech.bytedance.com"
    }
  ]
}`

// zhipuVisionProviderJSON is the seeded 智谱 vision provider — the vision
// counterpart of groqStt: a keyless dedicated-capability provider, routed via
// providers.default["vision"], flagged by the sidebar's red needs-key badge
// until the operator fills the key. Vision-only model list on purpose; these
// VLM ids are excluded from the chat model pickers the same way whisper is
// (app/src/lib/modelTag.tsx isChatModel/isVisionModel).
const zhipuVisionProviderJSON = `{
  "key": "zhipuVision",
  "name": "智谱 GLM Vision",
  "apiKey": "",
  "defaultModel": "glm-4v-flash",
  "models": ["glm-4v-flash", "glm-4.6v-flash", "glm-4.1v-thinking-flash"],
  "protocol": "openai",
  "url": "https://open.bigmodel.cn/api/paas/v4"
}`

// doubaoVoiceProviderJSON is the seeded 豆包语音 (volcengine speech) provider —
// protocol "voice": NOT chat-completions. TTS is POST
// /api/v3/tts/unidirectional and ASR a binary websocket, both under this base
// with X-Api-Key auth (the UUID key from console.volcengine.com 语音技术 — a
// different key family from 方舟 Ark's "ark-*"). "models" holds the SPEAKER
// (音色) ids, not model ids — that is what an operator picks between. The
// runtime speech client is a separate concern; this entry gives the config
// surface: key storage, the voice route, the needs-key badge and the console
// link in the dashboard.
const doubaoVoiceProviderJSON = `{
  "key": "doubaoVoice",
  "name": "豆包语音",
  "apiKey": "",
  "defaultModel": "zh_female_shuangkuaisisi_uranus_bigtts",
  "models": ["zh_female_shuangkuaisisi_uranus_bigtts", "zh_female_tianmeixiaoyuan_uranus_bigtts", "zh_female_cancan_uranus_bigtts", "zh_female_xiaohe_uranus_bigtts", "zh_female_vv_uranus_bigtts", "zh_male_taocheng_uranus_bigtts"],
  "protocol": "voice",
  "url": "https://openspeech.bytedance.com"
}`

// ensureVoiceProvider backfills the 豆包语音 provider + "voice" route on
// EXISTING installs — same shape as ensureVisionProvider below. An operator
// with an openspeech.bytedance.com provider under any key keeps theirs as the
// route target; an operator-set voice route is never touched.
func ensureVoiceProvider() {
	ensureCapabilityProvider("voice", "openspeech.bytedance.com", "doubaoVoice", doubaoVoiceProviderJSON)
}

// ensureVisionProvider backfills the vision provider + route on EXISTING
// installs — ensureDefaultProviders never touches a config that already has
// providers, so without this only fresh installs would get it.
func ensureVisionProvider() {
	ensureCapabilityProvider("vision", "bigmodel.cn", "zhipuVision", zhipuVisionProviderJSON)
}

// ensureCapabilityProvider backfills a capability provider (vision / voice) +
// its providers.default[route] on existing installs. If the operator already
// has a provider whose url contains hostMarker (under any key), that provider
// becomes the route target instead of seeding a keyless duplicate next to it;
// an operator-set route is authoritative and never touched. Idempotent; runs
// on every boot after ensureClientProviders.
func ensureCapabilityProvider(route, hostMarker, seedKey, seedJSON string) {
	providersFileMu.Lock()
	defer providersFileMu.Unlock()

	cfg := readGlobalJSONConfig()
	if cfg == nil {
		cfg = map[string]any{}
	}
	block := providersBlock(cfg)
	defaults, _ := block["default"].(map[string]any)
	if defaults == nil {
		defaults = map[string]any{}
	}
	if s, _ := defaults[route].(string); strings.TrimSpace(s) != "" {
		return
	}

	items := providersItemsSlice(block)
	target := ""
	for _, it := range items {
		m, _ := it.(map[string]any)
		if m == nil {
			continue
		}
		url, _ := m["url"].(string)
		if strings.EqualFold(providerItemKey(it), seedKey) || strings.Contains(strings.ToLower(url), hostMarker) {
			target = providerItemKey(it)
			break
		}
	}
	if target == "" {
		var def map[string]any
		if err := json.Unmarshal([]byte(seedJSON), &def); err != nil {
			log.Printf("[setup] %s provider seed parse failed: %v", route, err)
			return
		}
		items = append(items, def)
		block["items"] = items
		target = seedKey
	}
	defaults[route] = target
	block["default"] = defaults
	cfg["providers"] = block
	if err := writeGlobalJSONConfig(cfg); err != nil {
		log.Printf("[setup] %s provider write failed: %v", route, err)
		return
	}
	log.Printf("[setup] ensured %s provider (route %s → %s)", route, route, target)
}

// ensureDefaultProviders seeds the CiCyAi default providers into global.json on
// the first boot of an instance that has no providers.items yet. Idempotent and
// non-destructive: once any providers exist (seeded here or added via the
// dashboard), it leaves them untouched so operator edits are never clobbered.
func ensureDefaultProviders() {
	providersFileMu.Lock()
	defer providersFileMu.Unlock()

	cfg := readGlobalJSONConfig()
	if cfg == nil {
		cfg = map[string]any{}
	}
	if existing, ok := cfg["providers"].(map[string]any); ok {
		if items, ok := existing["items"].([]any); ok && len(items) > 0 {
			return // operator already has providers — never clobber
		}
	}

	var block map[string]any
	if err := json.Unmarshal([]byte(defaultProvidersBlockJSON), &block); err != nil {
		log.Printf("[setup] default providers seed parse failed: %v", err)
		return
	}
	cfg["providers"] = block
	if err := writeGlobalJSONConfig(cfg); err != nil {
		log.Printf("[setup] default providers seed write failed: %v", err)
		return
	}
	log.Printf("[setup] seeded default providers (CiCyAi gateway) into global.json")
}

// opencodeZenProviderJSON is the OpenCode Zen free-tier provider. Its free models
// (big-pickle, *-free) need no real key ("public") and the gateway translates the
// Anthropic-speaking cicy agent down to this openai-protocol upstream.
const opencodeZenProviderJSON = `{
  "key": "opencodeZen",
  "name": "OpenCode Zen",
  "apiKey": "public",
  "defaultModel": "big-pickle",
  "defaultModels": {},
  "modelMapping": {},
  "models": [
    "big-pickle",
    "deepseek-v4-flash-free"
  ],
  "protocol": "openai",
  "url": "https://opencode.ai/zen/v1"
}`

// cloudGatewayKey returns this node's sk-cicy- gateway key for authenticating to
// the cloud (GET /api/providers/client). Prefers the runtime AI config key (per
// the API contract), then the seeded default providers' apiKey, then the gateway
// env that dev.py --docker injects — so it resolves on a real node, a dev
// container, and a fresh install alike.
func cloudGatewayKey() string {
	if k := strings.TrimSpace(os.Getenv("CICY_AI_GATEWAY_LLM_API_KEY")); k != "" {
		return k
	}
	if k := strings.TrimSpace(loadRuntimeAIConfig().APIKey); k != "" {
		return k
	}
	for _, key := range []string{"defaultAnthropic", "defaultOpenAi"} {
		if pc, ok := loadProviderByKey(key); ok {
			if k := strings.TrimSpace(pc.APIKey); k != "" {
				return k
			}
		}
	}
	return ""
}

// cloudGatewayBase returns the gateway base URL that serves /api/providers/client:
// the injected endpoint env wins (dev.py --docker sets it from the host), otherwise
// fall back to the well-known gateway. The endpoint lives on the AI gateway, not on
// cicy-ai.com.
func cloudGatewayBase() string {
	if u := strings.TrimSpace(os.Getenv("CICY_AI_GATEWAY_LLM_ENDPOINT")); u != "" {
		return u
	}
	return "https://gateway.cicy-ai.com"
}

// fetchClientProviders pulls the cloud's default client provider configs using
// this node's gateway key. Returns nil on any failure so the caller falls back to
// its baked-in default — setup must never block on the cloud being reachable.
func fetchClientProviders(cloudBase, gatewayKey string) []map[string]any {
	gatewayKey = strings.TrimSpace(gatewayKey)
	if gatewayKey == "" {
		return nil
	}
	if cloudBase == "" {
		cloudBase = "https://gateway.cicy-ai.com"
	}
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(cloudBase, "/")+"/api/providers/client", nil)
	if err != nil {
		return nil
	}
	req.Header.Set("Authorization", "Bearer "+gatewayKey)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[setup] fetch client providers failed: %v", err)
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Printf("[setup] client providers HTTP %d", resp.StatusCode)
		return nil
	}
	var items []map[string]any
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&items); err != nil {
		log.Printf("[setup] decode client providers: %v", err)
		return nil
	}
	return items
}

// ensureClientProviders merges the cloud's client-provider list into global.json.
// It fetches GET /api/providers/client (authed with this node's gateway key); on
// success it upserts every returned provider by key, using each entry verbatim —
// except a cloud default shipped with an empty apiKey (defaultAnthropic/
// defaultOpenAi), whose key+endpoint are filled from the injected gateway env
// (CICY_AI_GATEWAY_LLM_API_KEY / _ENDPOINT), falling back to this node's own.
// When the cloud is unreachable it falls back to the baked-in two-model OpenCode
// Zen default (seeding it if absent; the cloud gateway defaults are already seeded
// + key-filled by ensureDefaultProviders/applyGatewayEnvToDefaultProviders). Runs
// on every boot after those; never touches providers.default routing.
func ensureClientProviders() {
	// Fetch outside the file lock — a 10s network call must not hold it.
	cloud := fetchClientProviders(cloudGatewayBase(), cloudGatewayKey())

	providersFileMu.Lock()
	defer providersFileMu.Unlock()

	cfg := readGlobalJSONConfig()
	if cfg == nil {
		cfg = map[string]any{}
	}
	block := providersBlock(cfg)
	items := providersItemsSlice(block)

	indexOf := func(key string) int {
		for i, it := range items {
			if strings.EqualFold(providerItemKey(it), key) {
				return i
			}
		}
		return -1
	}

	if len(cloud) == 0 {
		// Cloud unreachable: ensure the baked-in OpenCode Zen default exists so the
		// node still has it offline. The gateway defaults are seeded + key-filled by
		// the earlier setup steps, so they need no fallback here.
		if indexOf("opencodeZen") < 0 {
			var def map[string]any
			if err := json.Unmarshal([]byte(opencodeZenProviderJSON), &def); err != nil {
				log.Printf("[setup] opencodeZen default parse failed: %v", err)
				return
			}
			items = append(items, def)
			block["items"] = items
			cfg["providers"] = block
			if err := writeGlobalJSONConfig(cfg); err != nil {
				log.Printf("[setup] opencodeZen default write failed: %v", err)
				return
			}
			log.Printf("[setup] seeded OpenCode Zen provider (baked-in default; cloud unreachable)")
		}
		return
	}

	for _, ci := range cloud {
		key := providerItemKey(ci)
		if key == "" {
			continue
		}
		// A cloud default ships with an empty apiKey — fill key + endpoint from the
		// injected gateway env (cloudGatewayKey/cloudGatewayBase are env-first).
		if k, _ := ci["apiKey"].(string); strings.TrimSpace(k) == "" {
			if v := cloudGatewayKey(); v != "" {
				ci["apiKey"] = v
			}
			if v := cloudGatewayBase(); v != "" {
				ci["url"] = v
			}
		}
		if idx := indexOf(key); idx >= 0 {
			items[idx] = ci
		} else {
			items = append(items, ci)
		}
	}
	block["items"] = items
	cfg["providers"] = block
	if err := writeGlobalJSONConfig(cfg); err != nil {
		log.Printf("[setup] client providers write failed: %v", err)
		return
	}
	log.Printf("[setup] merged %d cloud client provider(s) into global.json", len(cloud))
}

// applyGatewayEnvToDefaultProviders forces the two platform-managed default
// providers (defaultAnthropic / defaultOpenAi) to use the gateway endpoint and
// key from the environment — CICY_AI_GATEWAY_LLM_ENDPOINT and
// CICY_AI_GATEWAY_LLM_API_KEY. Containers/runtime set these so the gateway URL +
// team key define the defaults (superseding the retired CICY_API_KEY path);
// plain desktop installs leave them unset, so this is a no-op there and the
// desktop/operator-managed keys stay authoritative.
//
// Runs on every boot (after ensureDefaultProviders), so a rotated key in the
// container env is picked up on restart. Only the two managed keys' url/apiKey
// are touched — operator-added providers and other fields are never clobbered.
func applyGatewayEnvToDefaultProviders() {
	endpoint := strings.TrimSpace(os.Getenv("CICY_AI_GATEWAY_LLM_ENDPOINT"))
	apiKey := strings.TrimSpace(os.Getenv("CICY_AI_GATEWAY_LLM_API_KEY"))
	if endpoint == "" && apiKey == "" {
		return // nothing to inject — leave global.json untouched
	}

	providersFileMu.Lock()
	defer providersFileMu.Unlock()

	cfg := readGlobalJSONConfig()
	if cfg == nil {
		cfg = map[string]any{}
	}
	block := providersBlock(cfg)
	items := providersItemsSlice(block)

	managed := map[string]bool{"defaultAnthropic": true, "defaultOpenAi": true}
	changed := false
	for _, it := range items {
		m := providerItemMap(it)
		if m == nil || !managed[providerItemKey(it)] {
			continue
		}
		if endpoint != "" && m["url"] != endpoint {
			m["url"] = endpoint
			changed = true
		}
		if apiKey != "" && m["apiKey"] != apiKey {
			m["apiKey"] = apiKey
			changed = true
		}
	}
	if !changed {
		return
	}
	block["items"] = items
	cfg["providers"] = block
	if err := writeGlobalJSONConfig(cfg); err != nil {
		log.Printf("[setup] gateway env apply write failed: %v", err)
		return
	}
	log.Printf("[setup] applied gateway env to default providers (endpoint set=%v, apiKey set=%v)", endpoint != "", apiKey != "")
}

func providersBlock(cfg map[string]any) map[string]any {
	raw, ok := cfg["providers"]
	if ok {
		if m, ok := raw.(map[string]any); ok && m != nil {
			return m
		}
	}
	m := map[string]any{}
	cfg["providers"] = m
	return m
}

func providersDefaultMap(block map[string]any) map[string]any {
	raw, ok := block["default"]
	if ok {
		if m, ok := raw.(map[string]any); ok && m != nil {
			return m
		}
	}
	m := map[string]any{}
	block["default"] = m
	return m
}

func providersItemsSlice(block map[string]any) []any {
	raw, ok := block["items"]
	if ok {
		if arr, ok := raw.([]any); ok {
			return arr
		}
	}
	return []any{}
}

func providerItemMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

func providerItemKey(v any) string {
	m := providerItemMap(v)
	if m == nil {
		return ""
	}
	if s, ok := m["key"].(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

// sanitizeProviderDraft validates and normalizes a provider record coming from
// the API. When forKey is non-empty the record's key must equal it.
func sanitizeProviderDraft(raw map[string]any, existing map[string]any) (map[string]any, error) {
	if raw == nil {
		return nil, fmt.Errorf("provider body required")
	}
	out := map[string]any{}
	if existing != nil {
		for k, v := range existing {
			out[k] = v
		}
	}

	getStr := func(m map[string]any, key string) (string, bool) {
		if m == nil {
			return "", false
		}
		v, ok := m[key]
		if !ok {
			return "", false
		}
		s, _ := v.(string)
		return strings.TrimSpace(s), true
	}

	if v, ok := getStr(raw, "key"); ok {
		out["key"] = v
	}
	key, _ := out["key"].(string)
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, fmt.Errorf("provider key is required")
	}
	if !providerKeyPattern.MatchString(key) {
		return nil, fmt.Errorf("provider key must match %s", providerKeyPattern.String())
	}
	out["key"] = key

	if v, ok := getStr(raw, "name"); ok {
		out["name"] = v
	}
	if name, _ := out["name"].(string); strings.TrimSpace(name) == "" {
		out["name"] = key
	}

	if v, ok := getStr(raw, "url"); ok {
		out["url"] = strings.TrimRight(v, "/")
	}
	if url, _ := out["url"].(string); strings.TrimSpace(url) == "" {
		return nil, fmt.Errorf("provider url is required")
	}

	if v, ok := getStr(raw, "protocol"); ok {
		out["protocol"] = strings.ToLower(v)
	}
	protocol, _ := out["protocol"].(string)
	protocol = strings.ToLower(strings.TrimSpace(protocol))
	// "voice" marks a speech provider (TTS/ASR, e.g. 豆包语音/volcengine) whose
	// endpoints speak a vendor protocol, not chat completions — it is never a
	// chat routing target, but it must remain editable through this API (key
	// fill, voice list) like any other provider.
	if protocol != "openai" && protocol != "anthropic" && protocol != "voice" {
		return nil, fmt.Errorf("provider protocol must be openai, anthropic or voice")
	}
	out["protocol"] = protocol

	if v, ok := getStr(raw, "apiKey"); ok {
		out["apiKey"] = v
	}
	if _, ok := out["apiKey"]; !ok {
		out["apiKey"] = ""
	}

	if v, ok := getStr(raw, "defaultModel"); ok {
		out["defaultModel"] = v
	}
	if _, ok := out["defaultModel"]; !ok {
		out["defaultModel"] = ""
	}


	if rawDM, ok := raw["defaultModels"]; ok {
		dm := map[string]any{}
		if m, ok := rawDM.(map[string]any); ok {
			for at, model := range m {
				modelStr, _ := model.(string)
				modelStr = strings.TrimSpace(modelStr)
				at = strings.TrimSpace(strings.ToLower(at))
				if at == "" || modelStr == "" {
					continue
				}
				dm[at] = modelStr
			}
		}
		out["defaultModels"] = dm
	}

	if rawModels, ok := raw["models"]; ok {
		models := []any{}
		if arr, ok := rawModels.([]any); ok {
			for _, m := range arr {
				if s, ok := m.(string); ok && strings.TrimSpace(s) != "" {
					models = append(models, strings.TrimSpace(s))
				}
			}
		}
		out["models"] = models
	}

	if rawMapping, ok := raw["modelMapping"]; ok {
		mapping := map[string]any{}
		if m, ok := rawMapping.(map[string]any); ok {
			for from, to := range m {
				toStr, _ := to.(string)
				from = strings.TrimSpace(from)
				toStr = strings.TrimSpace(toStr)
				if from == "" || toStr == "" {
					continue
				}
				mapping[from] = toStr
			}
		}
		out["modelMapping"] = mapping
	}

	return out, nil
}

// providersPayload builds the JSON response for GET /api/providers.
func providersPayload(cfg map[string]any) M {
	block := providersBlock(cfg)
	defaults := map[string]string{}
	for k, v := range providersDefaultMap(block) {
		if s, ok := v.(string); ok {
			defaults[strings.TrimSpace(strings.ToLower(k))] = strings.TrimSpace(s)
		}
	}
	items := []any{}
	for _, item := range providersItemsSlice(block) {
		if m := providerItemMap(item); m != nil {
			items = append(items, m)
		}
	}
	return M{
		"defaults":         defaults,
		"items":            items,
		"agent_type_slots": providerDefaultAgentTypes,
		"source":           "global_json",
		"source_path":      globalJSONPath(),
	}
}

func handleProviders(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		providersFileMu.Lock()
		cfg := readGlobalJSONConfig()
		payload := providersPayload(cfg)
		providersFileMu.Unlock()
		J(w, payload)
	case http.MethodPost:
		var body map[string]any
		if err := readBody(r, &body); err != nil {
			httpErr(w, 400, "invalid request body")
			return
		}
		providersFileMu.Lock()
		defer providersFileMu.Unlock()
		cfg := readGlobalJSONConfig()
		block := providersBlock(cfg)
		draft, err := sanitizeProviderDraft(body, nil)
		if err != nil {
			httpErr(w, 400, err.Error())
			return
		}
		key := draft["key"].(string)
		items := providersItemsSlice(block)
		for _, item := range items {
			if strings.EqualFold(providerItemKey(item), key) {
				httpErr(w, 409, "provider key already exists: "+key)
				return
			}
		}
		items = append(items, draft)
		block["items"] = items
		if err := writeGlobalJSONConfig(cfg); err != nil {
			httpErr(w, 500, "failed to persist providers: "+err.Error())
			return
		}
		J(w, M{"success": true, "provider": draft})
	default:
		httpErr(w, 405, "method not allowed")
	}
}

func handleProvidersSub(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/providers/")
	rest = strings.Trim(rest, "/")
	switch rest {
	case "":
		handleProviders(w, r)
		return
	case "test":
		if r.Method != http.MethodPost {
			httpErr(w, 405, "method not allowed")
			return
		}
		handleProviderTest(w, r)
		return
	case "defaults":
		if r.Method != http.MethodPut && r.Method != http.MethodPost {
			httpErr(w, 405, "method not allowed")
			return
		}
		handleProviderDefaults(w, r)
		return
	}
	// otherwise treat the remaining segment as the provider key
	if strings.Contains(rest, "/") {
		httpErr(w, 404, "not found")
		return
	}
	handleProviderByKey(w, r, strings.TrimSpace(rest))
}

func handleProviderByKey(w http.ResponseWriter, r *http.Request, key string) {
	switch r.Method {
	case http.MethodGet:
		providersFileMu.Lock()
		cfg := readGlobalJSONConfig()
		block := providersBlock(cfg)
		var found map[string]any
		for _, item := range providersItemsSlice(block) {
			if m := providerItemMap(item); m != nil && strings.EqualFold(strings.TrimSpace(providerItemKey(item)), key) {
				found = m
				break
			}
		}
		providersFileMu.Unlock()
		if found == nil {
			httpErr(w, 404, "provider not found: "+key)
			return
		}
		J(w, M{"provider": found})
	case http.MethodPatch, http.MethodPut:
		var body map[string]any
		if err := readBody(r, &body); err != nil {
			httpErr(w, 400, "invalid request body")
			return
		}
		providersFileMu.Lock()
		defer providersFileMu.Unlock()
		cfg := readGlobalJSONConfig()
		block := providersBlock(cfg)
		items := providersItemsSlice(block)
		idx := -1
		for i, item := range items {
			if strings.EqualFold(strings.TrimSpace(providerItemKey(item)), key) {
				idx = i
				break
			}
		}
		if idx < 0 {
			httpErr(w, 404, "provider not found: "+key)
			return
		}
		existing := providerItemMap(items[idx])
		// key changes are not allowed via PATCH to avoid dangling references
		if v, ok := body["key"]; ok {
			if s, _ := v.(string); strings.TrimSpace(s) != "" && !strings.EqualFold(strings.TrimSpace(s), key) {
				httpErr(w, 400, "provider key cannot be changed")
				return
			}
		}
		body["key"] = key
		draft, err := sanitizeProviderDraft(body, existing)
		if err != nil {
			httpErr(w, 400, err.Error())
			return
		}
		items[idx] = draft
		block["items"] = items
		if err := writeGlobalJSONConfig(cfg); err != nil {
			httpErr(w, 500, "failed to persist providers: "+err.Error())
			return
		}
		J(w, M{"success": true, "provider": draft})
	case http.MethodDelete:
		// Cicy-shipped official providers are not deletable.
		if isProtectedProviderKey(key) {
			httpErr(w, 403, "provider is protected and cannot be deleted: "+key)
			return
		}
		providersFileMu.Lock()
		defer providersFileMu.Unlock()
		cfg := readGlobalJSONConfig()
		block := providersBlock(cfg)
		items := providersItemsSlice(block)
		idx := -1
		for i, item := range items {
			if strings.EqualFold(strings.TrimSpace(providerItemKey(item)), key) {
				idx = i
				break
			}
		}
		if idx < 0 {
			httpErr(w, 404, "provider not found: "+key)
			return
		}
		actualKey := strings.TrimSpace(providerItemKey(items[idx]))
		// reference checks
		var refs []string
		for at, v := range providersDefaultMap(block) {
			if s, ok := v.(string); ok && strings.EqualFold(strings.TrimSpace(s), actualKey) {
				refs = append(refs, "providers.default."+at)
			}
		}
		for _, paneRef := range panesReferencingProvider(actualKey) {
			refs = append(refs, "pane "+paneRef)
		}
		if len(refs) > 0 {
			sort.Strings(refs)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(409)
			json.NewEncoder(w).Encode(M{
				"detail":     "provider is still referenced; remove the references first",
				"references": refs,
			})
			return
		}
		items = append(items[:idx], items[idx+1:]...)
		block["items"] = items
		if err := writeGlobalJSONConfig(cfg); err != nil {
			httpErr(w, 500, "failed to persist providers: "+err.Error())
			return
		}
		J(w, M{"success": true})
	default:
		httpErr(w, 405, "method not allowed")
	}
}

// panesReferencingProvider returns short pane ids whose runtime_ai.provider_name
// equals key.
func panesReferencingProvider(key string) []string {
	key = strings.TrimSpace(key)
	if key == "" || store == nil {
		return nil
	}
	rows, err := store.Query("SELECT pane_id, COALESCE(config, '') FROM agent_config")
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var paneID string
		var cfgStr sql.NullString
		if err := rows.Scan(&paneID, &cfgStr); err != nil {
			continue
		}
		if !cfgStr.Valid || strings.TrimSpace(cfgStr.String) == "" {
			continue
		}
		ov := extractRuntimeAIFromConfigJSON(cfgStr.String)
		if ov != nil && strings.EqualFold(strings.TrimSpace(ov.ProviderName), key) {
			out = append(out, shortPaneID(paneID))
		}
	}
	return out
}

func handleProviderDefaults(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := readBody(r, &body); err != nil {
		httpErr(w, 400, "invalid request body")
		return
	}
	// allow either {"default": {...}} or a flat map of agent_type -> key
	updates, ok := body["default"].(map[string]any)
	if !ok {
		updates = body
	}
	providersFileMu.Lock()
	defer providersFileMu.Unlock()
	cfg := readGlobalJSONConfig()
	block := providersBlock(cfg)
	defaults := providersDefaultMap(block)
	keyProtocol := map[string]string{} // lower(key) -> normalized protocol
	for _, item := range providersItemsSlice(block) {
		m := providerItemMap(item)
		if m == nil {
			continue
		}
		k := strings.ToLower(strings.TrimSpace(providerItemKey(item)))
		if k == "" {
			continue
		}
		proto, _ := m["protocol"].(string)
		keyProtocol[k] = normalizeAIGatewayProvider(proto)
	}
	for at, v := range updates {
		at = strings.TrimSpace(strings.ToLower(at))
		if at == "" {
			continue
		}
		s, _ := v.(string)
		s = strings.TrimSpace(s)
		if s == "" {
			delete(defaults, at)
			continue
		}
		actualProto, known := keyProtocol[strings.ToLower(s)]
		if !known {
			httpErr(w, 400, fmt.Sprintf("unknown provider key %q for agent type %q", s, at))
			return
		}
		// enforce: claude -> anthropic provider, codex/opencode/openclaw/hermes -> openai provider
		if want := runtimeAIExpectedProtocolForAgentType(at); want != "" && actualProto != "" && actualProto != want {
			httpErr(w, 400, fmt.Sprintf("agent type %q requires a %s provider, but %q uses %s", at, want, s, actualProto))
			return
		}
		defaults[at] = s
	}
	block["default"] = defaults
	if err := writeGlobalJSONConfig(cfg); err != nil {
		httpErr(w, 500, "failed to persist providers: "+err.Error())
		return
	}
	J(w, M{"success": true, "defaults": defaults})
}

type providerTestResult struct {
	OK         bool   `json:"ok"`
	Status     int    `json:"status,omitempty"`
	DurationMS int64  `json:"duration_ms"`
	Endpoint   string `json:"endpoint,omitempty"`
	Detail     string `json:"detail,omitempty"`
	Model      string `json:"model,omitempty"`
}

func handleProviderTest(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := readBody(r, &body); err != nil {
		httpErr(w, 400, "invalid request body")
		return
	}
	var record map[string]any
	if keyRaw, ok := body["key"]; ok {
		key, _ := keyRaw.(string)
		key = strings.TrimSpace(key)
		if key != "" {
			providersFileMu.Lock()
			cfg := readGlobalJSONConfig()
			block := providersBlock(cfg)
			for _, item := range providersItemsSlice(block) {
				if m := providerItemMap(item); m != nil && strings.EqualFold(strings.TrimSpace(providerItemKey(item)), key) {
					record = m
					break
				}
			}
			providersFileMu.Unlock()
			if record == nil {
				httpErr(w, 404, "provider not found: "+key)
				return
			}
		}
	}
	if record == nil {
		// treat the body itself as a draft (skip strict key validation)
		draft := map[string]any{}
		for k, v := range body {
			draft[k] = v
		}
		if _, ok := draft["key"]; !ok {
			draft["key"] = "draft"
		}
		record = draft
	}

	url := strings.TrimRight(strings.TrimSpace(stringFromAny(record["url"])), "/")
	protocol := strings.ToLower(strings.TrimSpace(stringFromAny(record["protocol"])))
	apiKey := strings.TrimSpace(stringFromAny(record["apiKey"]))
	model := strings.TrimSpace(stringFromAny(body["model"]))
	if model == "" {
		model = strings.TrimSpace(stringFromAny(record["defaultModel"]))
	}
	if url == "" {
		J(w, providerTestResult{OK: false, Detail: "provider url is empty"})
		return
	}
	if protocol == "voice" {
		// A voice provider's connection test is a real minimal TTS synth (one
		// character) with the selected speaker — the vendor's error (wrong key,
		// resource not enabled) comes back verbatim.
		J(w, doubaoTTSProbe(url, apiKey, model))
		return
	}
	if protocol != "openai" && protocol != "anthropic" {
		J(w, providerTestResult{OK: false, Detail: "provider protocol must be openai, anthropic or voice"})
		return
	}

	client := &http.Client{Timeout: 15 * time.Second}
	start := time.Now()

	if protocol == "openai" {
		base := url
		if !strings.HasSuffix(base, "/v1") {
			base = base + "/v1"
		}
		endpoint := base + "/models"
		req, err := http.NewRequest(http.MethodGet, endpoint, nil)
		if err != nil {
			J(w, providerTestResult{OK: false, Detail: err.Error(), Endpoint: endpoint})
			return
		}
		if apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+apiKey)
		}
		resp, err := client.Do(req)
		dur := time.Since(start).Milliseconds()
		if err != nil {
			J(w, providerTestResult{OK: false, Detail: err.Error(), Endpoint: endpoint, DurationMS: dur})
			return
		}
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			J(w, providerTestResult{OK: true, Status: resp.StatusCode, Endpoint: endpoint, DurationMS: dur, Detail: "ok"})
			return
		}
		J(w, providerTestResult{OK: false, Status: resp.StatusCode, Endpoint: endpoint, DurationMS: dur, Detail: truncateForDetail(string(respBody))})
		return
	}

	// anthropic
	if model == "" {
		model = "claude-opus-4-7"
	}
	endpoint := url + "/v1/messages"
	payload := M{
		"model":      model,
		"max_tokens": 1,
		"messages": []M{
			{"role": "user", "content": "ping"},
		},
	}
	bodyBytes, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		J(w, providerTestResult{OK: false, Detail: err.Error(), Endpoint: endpoint})
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	if apiKey != "" {
		req.Header.Set("x-api-key", apiKey)
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := client.Do(req)
	dur := time.Since(start).Milliseconds()
	if err != nil {
		J(w, providerTestResult{OK: false, Detail: err.Error(), Endpoint: endpoint, DurationMS: dur, Model: model})
		return
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		J(w, providerTestResult{OK: true, Status: resp.StatusCode, Endpoint: endpoint, DurationMS: dur, Model: model, Detail: "ok"})
		return
	}
	J(w, providerTestResult{OK: false, Status: resp.StatusCode, Endpoint: endpoint, DurationMS: dur, Model: model, Detail: truncateForDetail(string(respBody))})
}

func stringFromAny(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func truncateForDetail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 600 {
		return s[:600] + "…"
	}
	return s
}
