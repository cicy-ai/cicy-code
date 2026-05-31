// Package skillcmd implements the `cicy-code skill ...` subcommands.
//
// Mirrors the spec in docs/skills-v2-* of this repo.
package skillcmd

import "time"

// Manifest mirrors schemas/manifest.schema.json from cicy-ai/cicy-skills.
// Only fields we read are typed strictly; extra fields are tolerated.
type Manifest struct {
	Schema             string            `json:"$schema,omitempty"`
	Name               string            `json:"name"`
	Version            string            `json:"version"`
	Title              string            `json:"title"`
	Description        string            `json:"description"`
	Category           string            `json:"category"`
	Tags               []string          `json:"tags,omitempty"`
	Author             string            `json:"author"`
	Homepage           string            `json:"homepage,omitempty"`
	License            string            `json:"license"`
	Runtime            ManifestRuntime   `json:"runtime"`
	SystemRequirements []string          `json:"system_requirements,omitempty"`
	NpmDependencies    bool              `json:"npm_dependencies,omitempty"`
	Entry              string            `json:"entry"`
	BinAliases         []string          `json:"bin_aliases,omitempty"`
	Config             *ManifestConfig   `json:"config,omitempty"`
	Permissions        []string          `json:"permissions,omitempty"`
	CompatibleAgents   []string          `json:"compatible_agents,omitempty"`
	Files              *ManifestFiles    `json:"files,omitempty"`
	Publish            *ManifestPublish  `json:"publish,omitempty"`
	Yanked             bool              `json:"yanked,omitempty"`
}

type ManifestRuntime struct {
	Node string `json:"node"`
}

type ManifestConfig struct {
	Path         string   `json:"path"`
	Permissions  string   `json:"permissions,omitempty"`
	SecretFields []string `json:"secret_fields,omitempty"`
	Schema       string   `json:"schema,omitempty"`
}

type ManifestFiles struct {
	SkillMD string `json:"skill_md,omitempty"`
	HelpMD  string `json:"help_md,omitempty"`
	ToolsMD string `json:"tools_md,omitempty"`
	Readme  string `json:"readme,omitempty"`
}

type ManifestPublish struct {
	PublishedAt string         `json:"published_at"`
	SHA256      string         `json:"sha256"`
	Size        int64          `json:"size"`
	DownloadURL string         `json:"download_url"`
	Source      ManifestSource `json:"source"`
	Signature   *string        `json:"signature,omitempty"`
}

type ManifestSource struct {
	Type       string `json:"type"`
	Repository string `json:"repository,omitempty"`
	Tag        string `json:"tag,omitempty"`
	Commit     string `json:"commit,omitempty"`
}

// SkillSummary matches /v1/skills entries.
type SkillSummary struct {
	Name             string   `json:"name"`
	Version          string   `json:"version"`
	Title            string   `json:"title"`
	Description      string   `json:"description"`
	Category         string   `json:"category"`
	Tags             []string `json:"tags"`
	Author           string   `json:"author"`
	License          string   `json:"license"`
	CompatibleAgents []string `json:"compatible_agents"`
	Size             int64    `json:"size"`
	PublishedAt      string   `json:"published_at"`
}

// AgentsConfig represents ~/cicy-ai/skills/agents.json.
type AgentsConfig struct {
	SchemaVersion int     `json:"schema_version"`
	Agents        []Agent `json:"agents"`
}

type Agent struct {
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	SkillsDir    string       `json:"skills_dir"`
	ManifestFile string       `json:"manifest_file"`
	Detect       *AgentDetect `json:"detect,omitempty"`
	MinVersion   string       `json:"min_version,omitempty"`
}

type AgentDetect struct {
	Command        string `json:"command"`
	VersionFlag    string `json:"version_flag,omitempty"`
	VersionPattern string `json:"version_pattern,omitempty"`
}

// InstalledConfig represents ~/cicy-ai/skills/installed.json.
type InstalledConfig struct {
	SchemaVersion int                `json:"schema_version"`
	Skills        []InstalledSkill   `json:"skills"`
}

type InstalledSkill struct {
	Name         string          `json:"name"`
	Version      string          `json:"version"`
	InstalledAt  time.Time       `json:"installed_at"`
	Source       InstalledSource `json:"source"`
	SHA256       string          `json:"sha256,omitempty"`
	AgentsSynced []string        `json:"agents_synced,omitempty"`
	// InstallDir is the on-disk directory the skill was extracted to. With the
	// source-based layout this is ~/cicy-ai/skills/<name> (public),
	// .../private/<name> (own local registry), or .../team/<src>/<name>.
	// Empty for legacy installs → callers fall back to the flat skillDir(name).
	InstallDir string `json:"install_dir,omitempty"`
}

type InstalledSource struct {
	Type        string `json:"type"`
	DownloadURL string `json:"download_url,omitempty"`
	Repository  string `json:"repository,omitempty"`
	Ref         string `json:"ref,omitempty"`
	Path        string `json:"path,omitempty"` // for local source
}

// RegistryEnvelope wraps Worker responses: { ok, data, error }.
type RegistryEnvelope struct {
	OK    bool             `json:"ok"`
	Data  interface{}      `json:"data"`
	Error *RegistryAPIError `json:"error"`
}

type RegistryAPIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// SkillDetail matches /v1/skills/:name response.
type SkillDetail struct {
	Manifest Manifest          `json:"manifest"`
	Files    map[string]string `json:"files"`
}

// SkillListResp matches /v1/skills.
type SkillListResp struct {
	Skills []SkillSummary `json:"skills"`
	Total  int            `json:"total"`
	Limit  int            `json:"limit"`
	Offset int            `json:"offset"`
}
