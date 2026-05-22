package skillcmd

import (
	"fmt"
	"os"
)

const Usage = `cicy-code skill — manage cicy skills

Usage:
  cicy-code skill list [--query <q>] [--category <c>] [--agent <id>] [--json]
  cicy-code skill search <query> [--json]
  cicy-code skill info <name>[@<version>] [--json]
  cicy-code skill install <name>[@<version>] [--json]
  cicy-code skill update <name> [--json]
  cicy-code skill update --all [--json]
  cicy-code skill remove <name> [--json]
  cicy-code skill installed [--json]
  cicy-code skill dev <path> [--json]
  cicy-code skill --help

Environment:
  CICY_SKILLS_REGISTRY   Override registry base URL (default: https://skills.cicy-ai.com)
  CICY_SKILLS_ROOT       Override skills root dir (default: ~/cicy-ai/skills)

Source: github.com/cicy-ai/cicy-skills
Spec:   docs/skills-v2-design.md
`

// Run is the entry point invoked from main.go when args[0] == "skill".
// args is everything AFTER the "skill" word.
func Run(args []string) {
	if len(args) == 0 {
		fmt.Print(Usage)
		os.Exit(2)
	}
	cmd := args[0]
	rest := args[1:]

	var err error
	switch cmd {
	case "-h", "--help", "help":
		fmt.Print(Usage)
		return
	case "list", "ls":
		err = cmdList(rest)
	case "search":
		err = cmdSearch(rest)
	case "info", "show":
		err = cmdInfo(rest)
	case "install", "add":
		err = cmdInstall(rest)
	case "update", "upgrade":
		err = cmdUpdate(rest)
	case "remove", "uninstall", "rm":
		err = cmdRemove(rest)
	case "installed":
		err = cmdInstalled(rest)
	case "dev":
		err = cmdDev(rest)
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n\n", cmd)
		fmt.Fprint(os.Stderr, Usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "skill: %v\n", err)
		os.Exit(1)
	}
}
