// src/types.ts
//
// Shared types used by handlers and lib.
// Mirrors schemas/manifest.schema.json from cicy-ai/cicy-skills.

export interface Env {
  SKILLS_KV: KVNamespace;
  ADMIN_TOKEN: string;
  DEFAULT_REPO: string;
  SCHEMA_VERSION: string;
}

export interface ManifestPublish {
  published_at: string;
  sha256: string;
  size: number;
  download_url: string;
  source: {
    type: 'github' | 'url' | 'git';
    repository?: string;
    tag?: string;
    commit?: string;
  };
  signature: string | null;
}

export interface Manifest {
  $schema?: string;
  name: string;
  version: string;
  title: string;
  description: string;
  /** Localized title/description keyed by BCP-47 tag, e.g. "zh-CN" */
  i18n?: Record<string, { title?: string; description?: string }>;
  category:
    | 'network'
    | 'cloud'
    | 'ai'
    | 'dev'
    | 'system'
    | 'productivity'
    | 'agent'
    | 'infra'
    | 'other';
  tags?: string[];
  author: string;
  homepage?: string;
  license: string;
  runtime: { node: string };
  system_requirements?: string[];
  npm_dependencies?: boolean;
  entry: string;
  bin_aliases?: string[];
  config?: {
    path: string;
    permissions?: string;
    secret_fields?: string[];
    schema?: string;
  };
  permissions?: string[];
  compatible_agents?: string[];
  files?: {
    skill_md?: string;
    help_md?: string;
    tools_md?: string;
    readme?: string;
  };
  publish?: ManifestPublish;
  yanked?: boolean;
}

export interface PublishRequest {
  manifest: Manifest;
  verify: {
    download_url: string;
    sha256: string;
    size: number;
  };
}

export interface ApiResult<T> {
  ok: true;
  data: T;
  error: null;
}

export interface ApiError {
  ok: false;
  data: null;
  error: { code: string; message: string };
}

export type ApiResponse<T> = ApiResult<T> | ApiError;

export interface SkillSummary {
  name: string;
  version: string;
  title: string;
  description: string;
  /** Present when lang fallback resolves to a non-English value */
  title_localized?: string;
  description_localized?: string;
  category: string;
  tags: string[];
  author: string;
  license: string;
  compatible_agents: string[];
  size: number;
  published_at: string;
}

export interface VersionEntry {
  version: string;
  published_at: string;
  size: number;
  yanked?: boolean;
}
