# cicy-code for VS Code

Embed your **cicy-code team workspace** — multi-agent terminals, native files,
artifacts — directly inside VS Code.

cicy-code is a SaaS. This extension does **not** run anything locally; it is a
thin shell that renders a team you've already created/joined. Editing, LSP and
debugging stay in your real VS Code; the cicy-code panel gives you the agent
workspace next to it.

## Setup

1. Create or join a team in cicy-code and copy its **URL + API token**.
2. In VS Code: Command Palette → **cicy-code: Set Team (URL + token)**.
3. Open the cicy-code icon in the Activity Bar.

Or click an **Add to IDE** link from your team page:

```
vscode://cicy.cicy-code/addTeam?url=https://team.example.com&token=…&name=My%20Team
```

(mirrors cicy-desktop's `cicy://addTeam` — dedup by URL, updates the token if it
changed, never overwrites an existing team name.)

## Commands

| Command | Action |
|---|---|
| `cicy-code: Set Team (URL + token)` | add/update a team, store token securely |
| `cicy-code: Switch Team` | pick the active team |
| `cicy-code: Open Workspace in Editor` | open as an editor tab instead of the sidebar |
| `cicy-code: Reload Workspace` | reload the webview |
| `cicy-code: Sign Out (clear token)` | remove the stored token for the active team |

## Notes

- Tokens are stored in VS Code **SecretStorage**, never in synced settings.
- The team server must allow being framed (no `X-Frame-Options: DENY`). This is
  a server-side requirement on the cicy-code SaaS.

## License

MIT
