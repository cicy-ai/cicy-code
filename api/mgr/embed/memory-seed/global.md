## Collaboration

Work with other agents via the `cicy-agent` skill (`cicy-agent help` for all subcommands):
- `cicy-agent ls` — list agents
- `cicy-agent msg <agent> <text>` — dispatch a task or ask for help
- `cicy-agent capture <agent>` — check an agent's progress

## Knowledge

Check the team knowledge base before reinventing conventions, pitfalls, or prior decisions (`cicy-knowledge help` for all commands):
- `cicy-knowledge recall <keyword>` — search the canon (verified facts only; drafts never surface)
- `cicy-knowledge get <id>` — read a full entry
- `cicy-knowledge add "<title>" --body <md> [--tags "a b"]` — propose an entry (lands pending for review)

## Skills

Building, installing, or publishing a skill? Read `cicy-skill-spec` first — it covers the public / private / team conventions and scaffolding.

## Documents

Don't drop docs at random paths. If another agent might need it, add it to the knowledge base, not a stray `.md`:
- Finished → `cicy-knowledge add "<title>" --body <md> --tags "a b"`
- Draft → `cicy-knowledge add --draft ...`

Uploaded docs (`~/cicy-ai/assets`): leave for review. Private scratch: your own workspace/memory only.

## Media in chat

When the user asks to see or receive an image, audio clip, video, screenshot, recording, or other generated file, do not only say where it was saved. Put the finished file under `~/cicy-ai/assets/` (or a dated subdirectory) and include a Markdown reference to its **absolute path** in the final reply so the chat UI can render or play it.

- Image: `![description](/absolute/path/to/cicy-ai/assets/image.png)`
- Audio: `[Play audio](/absolute/path/to/cicy-ai/assets/audio.mp3)`
- Video: `[Play video](/absolute/path/to/cicy-ai/assets/video.mp4)`
- Other file: `[Download file](/absolute/path/to/cicy-ai/assets/file.ext)`

Use a real absolute path such as `/home/runner/cicy-ai/assets/screenshot.png`, not `/tmp/...`, a workspace-private path, or a bare filename. Keep the correct file extension because the UI uses it to choose the image, audio, or video renderer. Supported common media include PNG/JPEG/GIF/WebP/SVG, MP3/WAV/OGG/Opus/M4A/AAC/FLAC, and MP4/MOV/M4V/AVI/MKV/WebM/OGV.

Before replying, verify that the referenced file exists, is non-empty, and is readable. For multiple outputs, include one Markdown reference per file. Never claim that media was attached or sent unless the final reply contains the reference the user can open.

## Constraints

- Projects: always create and clone into `~/projects` — never scatter repos at arbitrary paths. A new or cloned project lives at `~/projects/<name>`.
