## 协作

通过 `cicy-agent` Skill 与其他 Agent 协作（所有子命令见 `cicy-agent help`）：
- `cicy-agent ls` — 列出 Agent
- `cicy-agent msg <agent> <text>` — 派发任务或请求帮助
- `cicy-agent capture <agent>` — 查看 Agent 进度

跨 Instance 消息必须使用已安装 Skill 的 `bin/cicy-agent`：
- `cicy-agent msg <team.agent> <text>` — 自动完成 Cloud 路由、发送者身份和回复关联
- 禁止直接 `curl`/`POST /api/code/messages`
- 禁止为发送消息而手工读取 `cloud-device.json` Token 或自行拼装消息请求

## 知识

在重新制定规范、排查常见问题或重复既有决策前，先查询团队知识库（所有命令见 `cicy-knowledge help`）：
- `cicy-knowledge recall <keyword>` — 搜索已验证知识（草稿不会出现）
- `cicy-knowledge get <id>` — 读取完整条目
- `cicy-knowledge add "<title>" --body <md> [--tags "a b"]` — 提交待审核知识条目

## 技能

构建、安装或发布 Skill 前，先阅读 `cicy-skill-spec`，其中包含 public、private、team 的规范和脚手架说明。

## 文档

不要把文档随意放在零散路径。其他 Agent 可能需要时，应加入知识库：
- 已完成 → `cicy-knowledge add "<title>" --body <md> --tags "a b"`
- 草稿 → `cicy-knowledge add --draft ...`

用户上传的文档位于 `~/cicy-ai/assets`，应保留供审核。私有草稿只放在自己的 workspace 或 memory 中。

## 对话中的媒体

用户要求查看或接收图片、音频、视频、截图、录音或其他生成文件时，把最终文件放到 `~/cicy-ai/assets/`（或按日期建立的子目录），并在最终回复中提供绝对路径的 Markdown 引用，使聊天界面能够展示或播放。

- 图片：`![说明](/absolute/path/to/cicy-ai/assets/image.png)`
- 音频：`[播放音频](/absolute/path/to/cicy-ai/assets/audio.mp3)`
- 视频：`[播放视频](/absolute/path/to/cicy-ai/assets/video.mp4)`
- 其他文件：`[下载文件](/absolute/path/to/cicy-ai/assets/file.ext)`

必须使用真实绝对路径，不要使用 `/tmp/...`、workspace 私有路径或单独文件名。保留正确扩展名。回复前确认引用的文件存在、可读且非空。最终回复中没有可打开的引用时，不得声称媒体已经附加或发送。

## 约束

- 项目必须创建或克隆到 `~/projects`，不要散落在其他路径。新项目或克隆项目应位于 `~/projects/<name>`。
