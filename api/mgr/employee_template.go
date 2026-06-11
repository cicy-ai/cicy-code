package main

// 员工模版配置(employee templates) —— 每个岗位的 工具选择 / 开场白 / 人设,集中在
// 一个 YAML 文件里,热加载(改文件下次请求即生效,不用重启)。
//
// 分工:
//   - 工具的「定义」(argv/params/安全 L1–L4)留在 lite-config.json,这里不碰；
//     模版的 `tools` 只是「选」哪几个组(组名引用 lite-config.json 里定义的组)。
//   - 「开场白」从角色 .md 的 `## 开场白` 搬到这里(开场白与 role 正文分离)。
//   - 「人设」可选地覆盖 ~/cicy-ai/memory/agents/<slug>.md 的正文。
//
// 文件:~/cicy-ai/db/employees.yaml(与 lite-config.json 同放在 db/ 下;旧的
// ~/cicy-ai/employees.yaml 会在首次启动时自动迁移过去)
//   templates:
//     项目经理: { tools: ["coordinate"], greeting: "...", prompt: "..." }
//     人力资源: { tools: ["coordinate", "onboard"], greeting: "..." }

import (
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"
)

// employeeTemplate is one position's configurable bits. All fields optional;
// an empty field means "fall back to the legacy source" (role .md / frontmatter).
type employeeTemplate struct {
	Tools    []string `yaml:"tools,omitempty"`    // group-name SELECTION (groups defined in lite-config.json)
	Greeting string   `yaml:"greeting,omitempty"` // opening line shown on an empty chat
	Prompt   string   `yaml:"prompt,omitempty"`   // optional persona override (else role .md body)
}

type employeeTemplatesFile struct {
	// Templates: per-ROLE defaults, keyed by role-template slug (项目经理 / 人力资源 …).
	Templates map[string]employeeTemplate `yaml:"templates"`
}

func employeeTemplatesPath() string {
	return filepath.Join(cicyRootDir, "db", "employees.yaml")
}

// legacyEmployeeTemplatesPath is the pre-migration location (~/cicy-ai/employees.yaml).
// Kept only so ensureEmployeeTemplates can relocate an existing file into db/.
func legacyEmployeeTemplatesPath() string {
	return filepath.Join(cicyRootDir, "employees.yaml")
}

var (
	empTplMu     sync.Mutex
	empTplCache  *employeeTemplatesFile
	empTplMtime  int64
	empTplLoaded bool
)

// loadEmployeeTemplates reads ~/cicy-ai/employees.yaml, cached by mtime so every
// request sees the latest without a restart. Missing/invalid file → empty set
// (callers then fall back to the legacy role .md / frontmatter sources).
func loadEmployeeTemplates() *employeeTemplatesFile {
	empTplMu.Lock()
	defer empTplMu.Unlock()

	var mtime int64
	if fi, err := os.Stat(employeeTemplatesPath()); err == nil {
		mtime = fi.ModTime().UnixNano()
	}
	if empTplLoaded && empTplCache != nil && mtime == empTplMtime {
		return empTplCache
	}

	out := &employeeTemplatesFile{Templates: map[string]employeeTemplate{}}
	if raw, err := os.ReadFile(employeeTemplatesPath()); err == nil && len(raw) > 0 {
		var f employeeTemplatesFile
		if yaml.Unmarshal(raw, &f) == nil && f.Templates != nil {
			out = &f
		}
	}
	empTplCache = out
	empTplMtime = mtime
	empTplLoaded = true
	return out
}

// ensureEmployeeTemplates writes ~/cicy-ai/employees.yaml on first boot by
// extracting each embedded role template's tool selection (frontmatter `tools:`)
// and opening line (`## 开场白`). Never clobbers an existing file — once it's
// there, the operator edits the YAML directly and that's the live source. Prompt
// is left empty so it falls back to the role .md body (no charter duplication).
func ensureEmployeeTemplates() {
	path := employeeTemplatesPath()
	if _, err := os.Stat(path); err == nil {
		return // never overwrite operator edits
	}
	// Migrate the legacy ~/cicy-ai/employees.yaml → ~/cicy-ai/db/employees.yaml,
	// preserving any operator edits, before falling back to re-seeding from the
	// embedded templates.
	if legacy := legacyEmployeeTemplatesPath(); legacy != path {
		if _, err := os.Stat(legacy); err == nil {
			if mkErr := os.MkdirAll(filepath.Dir(path), 0755); mkErr == nil {
				if os.Rename(legacy, path) == nil {
					return
				}
			}
		}
	}
	entries, err := agentRoleTemplatesFS.ReadDir("embed/agent-roles")
	if err != nil {
		return
	}
	out := employeeTemplatesFile{Templates: map[string]employeeTemplate{}}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".md" {
			continue
		}
		raw, err := agentRoleTemplatesFS.ReadFile("embed/agent-roles/" + e.Name())
		if err != nil {
			continue
		}
		slug := sanitizeTemplateSlug(e.Name())
		t := employeeTemplate{Greeting: extractOpeningSection(string(raw))}
		if fm := parseLiteFrontmatter(string(raw)); fm.hasTools {
			t.Tools = fm.tools
		}
		out.Templates[slug] = t
	}
	if len(out.Templates) == 0 {
		return
	}
	data, err := yaml.Marshal(out)
	if err != nil {
		return
	}
	header := []byte("# 员工模版配置 —— 每个岗位的 工具选择 / 开场白 / 人设,改完下次对话即生效。\n" +
		"# tools 只是「选」工具组(组的定义在 db/lite-config.json);greeting 为空则回退角色 .md 的 ## 开场白;\n" +
		"# prompt 为空则用角色 .md 正文。也可加 employees: 段按员工 id 覆盖。\n\n")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return
	}
	_ = os.WriteFile(path, append(header, data...), 0644)
}

func resetEmployeeTemplatesCache() {
	empTplMu.Lock()
	defer empTplMu.Unlock()
	empTplCache = nil
	empTplLoaded = false
	empTplMtime = 0
}

// employeeTemplateFor returns the template for a role slug, plus whether it
// exists in the config at all.
func employeeTemplateFor(slug string) (employeeTemplate, bool) {
	clean := sanitizeTemplateSlug(slug)
	if clean == "" {
		return employeeTemplate{}, false
	}
	t, ok := loadEmployeeTemplates().Templates[clean]
	return t, ok
}

// employeeRoleSlug looks up an employee's role-template slug from agent_config.
// nil-safe (returns "" when the store isn't initialised, e.g. in unit tests).
func employeeRoleSlug(shortID string) string {
	if store == nil {
		return ""
	}
	var rt string
	_ = store.QueryRow("SELECT COALESCE(role_template,'') FROM agent_config WHERE pane_id=?", shortID+":main.0").Scan(&rt)
	return sanitizeTemplateSlug(rt)
}

// employeeTemplateGreeting returns the configured opening line for a role slug,
// or "" when unset (caller falls back to the role .md `## 开场白`).
func employeeTemplateGreeting(slug string) string {
	if t, ok := employeeTemplateFor(slug); ok {
		return t.Greeting
	}
	return ""
}

// employeeTemplateTools returns the configured tool-group SELECTION for a role
// slug, or nil when unset (caller falls back to AGENTS.md frontmatter / profile).
func employeeTemplateTools(slug string) []string {
	if t, ok := employeeTemplateFor(slug); ok {
		return t.Tools
	}
	return nil
}

// employeeTemplatePrompt returns the persona-override text for a role slug, or
// "" when unset (caller falls back to the role .md body).
func employeeTemplatePrompt(slug string) string {
	if t, ok := employeeTemplateFor(slug); ok {
		return t.Prompt
	}
	return ""
}
