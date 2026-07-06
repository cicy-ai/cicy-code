# cicy-code for JetBrains

Embed your **cicy-code team workspace** — multi-agent terminals, native files,
artifacts — inside a JetBrains IDE (IntelliJ IDEA, PyCharm, GoLand, WebStorm, …)
as a right-side tool window, rendered with JCEF.

cicy-code is a SaaS. This plugin runs nothing locally; it points an embedded
Chromium at a team you've already created/joined.

## Setup

1. Create or join a team in cicy-code and copy its **URL + API token**.
2. In the IDE: **Settings → Tools → cicy-code** → paste URL + token.
3. Open the **cicy-code** tool window on the right.

The token is stored in the platform **PasswordSafe**, never in plain settings XML.

## Build

```bash
./gradlew buildPlugin      # -> build/distributions/cicy-code-<version>.zip
./gradlew verifyPlugin     # IntelliJ plugin-verifier (CI gate)
./gradlew runIde           # launch a sandbox IDE with the plugin
```

Requires JDK 17. The Gradle wrapper pins Gradle 8.7 + the IntelliJ Platform
Gradle plugin 2.x.

## Publish

CI (`.github/workflows/ide-extensions.yml`) runs `./gradlew publishPlugin` on
`ide-v*` tags. The **first** version must be uploaded manually on
plugins.jetbrains.com and pass human review; afterwards CI auto-publishes.

Signing/publish credentials come from env (set as CI secrets):
`CERTIFICATE_CHAIN`, `PRIVATE_KEY`, `PRIVATE_KEY_PASSWORD`, `PUBLISH_TOKEN`.

## License

Apache-2.0
