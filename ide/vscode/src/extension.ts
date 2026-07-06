// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import * as vscode from "vscode";

// cicy-code is a SaaS: there is no local backend to spawn. A user manually
// creates/joins a *team* (a hosted cicy-code instance) which has a base URL +
// API token. This extension is a thin shell — same idea as the cicy-desktop
// Electron client — that renders a configured team's web workspace inside a
// VS Code webview. Onboarding mirrors cicy-desktop's `cicy://addTeam` upsert:
//   - dedup by URL
//   - if the URL already exists, update the token when it differs
//   - never overwrite an existing team's display name
//
// Tokens are kept in VS Code SecretStorage (per-URL key), never in settings
// (which would sync to other machines in plaintext).

interface Team {
  name?: string;
  url: string;
}

const CFG = "cicyCode";
const TOKEN_KEY = (url: string) => `cicyCode.token:${normalizeUrl(url)}`;

function normalizeUrl(raw: string): string {
  let u = (raw || "").trim();
  if (!u) return u;
  if (!/^https?:\/\//i.test(u)) u = "https://" + u;
  // Drop trailing slash so dedup-by-url is stable.
  return u.replace(/\/+$/, "");
}

function getTeams(): Team[] {
  return vscode.workspace.getConfiguration(CFG).get<Team[]>("teams", []) ?? [];
}

async function setTeams(teams: Team[]): Promise<void> {
  await vscode.workspace
    .getConfiguration(CFG)
    .update("teams", teams, vscode.ConfigurationTarget.Global);
}

function getActiveUrl(): string {
  return normalizeUrl(
    vscode.workspace.getConfiguration(CFG).get<string>("activeTeamUrl", "") ?? ""
  );
}

async function setActiveUrl(url: string): Promise<void> {
  await vscode.workspace
    .getConfiguration(CFG)
    .update("activeTeamUrl", normalizeUrl(url), vscode.ConfigurationTarget.Global);
}

// Upsert a team into settings + stash its token in SecretStorage. Mirrors
// local-teams.addTeam semantics from cicy-desktop.
async function upsertTeam(
  secrets: vscode.SecretStorage,
  spec: { url: string; token?: string; name?: string }
): Promise<Team> {
  const url = normalizeUrl(spec.url);
  if (!url) throw new Error("team url is required");

  const teams = getTeams();
  const idx = teams.findIndex((t) => normalizeUrl(t.url) === url);

  if (idx === -1) {
    const team: Team = { url, name: spec.name?.trim() || undefined };
    teams.push(team);
    await setTeams(teams);
  } else if (spec.name && !teams[idx].name) {
    // Only fill a missing name; never overwrite an existing one.
    teams[idx].name = spec.name.trim();
    await setTeams(teams);
  }

  // Update token only when provided AND different (or not yet stored).
  if (spec.token) {
    const existing = await secrets.get(TOKEN_KEY(url));
    if (existing !== spec.token) await secrets.store(TOKEN_KEY(url), spec.token);
  }

  await setActiveUrl(url);
  return teams[idx === -1 ? teams.length - 1 : idx];
}

function originOf(url: string): string {
  try {
    return new URL(url).origin;
  } catch {
    return "";
  }
}

function buildSrc(url: string, token: string | undefined): string {
  const base = normalizeUrl(url);
  if (!token) return base;
  const sep = base.includes("?") ? "&" : "?";
  return `${base}${sep}token=${encodeURIComponent(token)}`;
}

function shellHtml(message: string): string {
  return `<!DOCTYPE html><html><head>
    <meta charset="utf-8" />
    <meta http-equiv="Content-Security-Policy"
          content="default-src 'none'; style-src 'unsafe-inline';" />
    <style>
      body{font-family:var(--vscode-font-family);color:var(--vscode-foreground);
           padding:24px;line-height:1.6}
      code{background:var(--vscode-textCodeBlock-background);padding:2px 4px;border-radius:3px}
    </style></head>
    <body>${message}</body></html>`;
}

function teamHtml(url: string, token: string | undefined): string {
  const origin = originOf(url);
  const src = buildSrc(url, token);
  // frame-src must allow the team origin. We additionally allow localhost so a
  // self-hosted/dev team works. The team server must permit being framed
  // (no X-Frame-Options:DENY / restrictive frame-ancestors) — that is a
  // server-side requirement on the cicy-code SaaS, not something the webview
  // can override.
  const frameSrc = [origin, "http://localhost:*", "http://127.0.0.1:*"]
    .filter(Boolean)
    .join(" ");
  return `<!DOCTYPE html><html><head>
    <meta charset="utf-8" />
    <meta http-equiv="Content-Security-Policy"
          content="default-src 'none'; frame-src ${frameSrc}; style-src 'unsafe-inline';" />
    <style>html,body,iframe{margin:0;padding:0;border:0;width:100%;height:100vh;display:block}</style>
    </head><body>
    <iframe src="${src}" allow="clipboard-read; clipboard-write; microphone; camera"></iframe>
    </body></html>`;
}

class WorkspaceViewProvider implements vscode.WebviewViewProvider {
  private view?: vscode.WebviewView;
  constructor(private readonly secrets: vscode.SecretStorage) {}

  resolveWebviewView(view: vscode.WebviewView): void {
    this.view = view;
    view.webview.options = { enableScripts: true };
    void this.render();
  }

  async render(): Promise<void> {
    if (!this.view) return;
    const url = getActiveUrl();
    if (!url) {
      this.view.webview.html = shellHtml(
        `<h3>No team configured</h3>
         <p>Run <code>cicy-code: Set Team (URL + token)</code> from the Command
         Palette, or open a <code>vscode://cicy.cicy-code/addTeam?url=…&token=…</code>
         link from your cicy-code team page.</p>`
      );
      return;
    }
    const token = await this.secrets.get(TOKEN_KEY(url));
    this.view.webview.html = teamHtml(url, token);
  }
}

export function activate(context: vscode.ExtensionContext): void {
  const provider = new WorkspaceViewProvider(context.secrets);

  context.subscriptions.push(
    vscode.window.registerWebviewViewProvider("cicyCode.workspace", provider, {
      webviewOptions: { retainContextWhenHidden: true },
    })
  );

  const refresh = () => provider.render();

  context.subscriptions.push(
    vscode.commands.registerCommand("cicy-code.setTeam", async () => {
      const url = await vscode.window.showInputBox({
        prompt: "cicy-code team URL",
        placeHolder: "https://team.example.com",
        ignoreFocusOut: true,
      });
      if (!url) return;
      const token = await vscode.window.showInputBox({
        prompt: "API token for this team",
        password: true,
        ignoreFocusOut: true,
      });
      const name = await vscode.window.showInputBox({
        prompt: "Display name (optional)",
        ignoreFocusOut: true,
      });
      await upsertTeam(context.secrets, { url, token: token || undefined, name: name || undefined });
      await refresh();
      vscode.window.showInformationMessage(`cicy-code: team set → ${normalizeUrl(url)}`);
    }),

    vscode.commands.registerCommand("cicy-code.switchTeam", async () => {
      const teams = getTeams();
      if (teams.length === 0) {
        await vscode.commands.executeCommand("cicy-code.setTeam");
        return;
      }
      const pick = await vscode.window.showQuickPick(
        teams.map((t) => ({ label: t.name || t.url, description: t.url })),
        { placeHolder: "Select active cicy-code team" }
      );
      if (!pick) return;
      await setActiveUrl(pick.description!);
      await refresh();
    }),

    vscode.commands.registerCommand("cicy-code.openPanel", async () => {
      const url = getActiveUrl();
      if (!url) {
        await vscode.commands.executeCommand("cicy-code.setTeam");
        return;
      }
      const panel = vscode.window.createWebviewPanel(
        "cicyCode.panel",
        "cicy-code",
        vscode.ViewColumn.Active,
        { enableScripts: true, retainContextWhenHidden: true }
      );
      const token = await context.secrets.get(TOKEN_KEY(url));
      panel.webview.html = teamHtml(url, token);
    }),

    vscode.commands.registerCommand("cicy-code.reload", refresh),

    vscode.commands.registerCommand("cicy-code.signOut", async () => {
      const url = getActiveUrl();
      if (url) await context.secrets.delete(TOKEN_KEY(url));
      await refresh();
      vscode.window.showInformationMessage("cicy-code: token cleared");
    }),

    // vscode://cicy.cicy-code/addTeam?url=…&token=…&name=…
    // Mirrors the cicy://addTeam deeplink so the SaaS "Add to IDE" button works.
    vscode.window.registerUriHandler({
      handleUri: async (uri: vscode.Uri) => {
        if (uri.path !== "/addTeam") return;
        const q = new URLSearchParams(uri.query);
        const url = q.get("url");
        if (!url) {
          vscode.window.showErrorMessage("cicy-code: addTeam link missing url");
          return;
        }
        await upsertTeam(context.secrets, {
          url,
          token: q.get("token") || undefined,
          name: q.get("name") || q.get("title") || undefined,
        });
        await refresh();
        await vscode.commands.executeCommand("workbench.view.extension.cicyCode");
        vscode.window.showInformationMessage(`cicy-code: team added → ${normalizeUrl(url)}`);
      },
    })
  );
}

export function deactivate(): void {
  /* no-op: nothing to tear down — the backend is remote */
}
