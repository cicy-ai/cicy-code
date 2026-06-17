package main

// Pure tmux-arg parsing/translation for the native (ptymux) backend. These have
// NO platform dependencies (string logic only), so they live outside the
// //go:build windows files — that lets them be unit-tested on any OS even though
// the ptmManager that calls them is Windows-only. Off Windows nothing calls
// these; they exist only to be exercised by ptymux_parse_test.go.

import "strings"

// ptmSessionOf extracts the session name from a paneID like "w-1001:main.0".
func ptmSessionOf(paneID string) string {
	if i := strings.IndexByte(paneID, ':'); i >= 0 {
		return paneID[:i]
	}
	return paneID
}

// ptmFromPosix converts an MSYS path (/c/Users/x) back to a Windows path
// (C:\Users\x) for ConPTY's working directory. cicy passes -c in MSYS form.
func ptmFromPosix(p string) string {
	if len(p) >= 2 && p[0] == '/' &&
		((p[1] >= 'a' && p[1] <= 'z') || (p[1] >= 'A' && p[1] <= 'Z')) &&
		(len(p) == 2 || p[2] == '/') {
		drive := strings.ToUpper(string(p[1]))
		rest := strings.ReplaceAll(p[2:], "/", `\`)
		return drive + ":" + rest
	}
	return p
}

type ptmFlags struct {
	target, sName, window, cwd, format string
	literal                            bool
	positional                         []string
}

func ptmParseFlags(args []string) ptmFlags {
	var f ptmFlags
	i := 0
	for i < len(args) {
		a := args[i]
		switch {
		case a == "--":
			f.positional = append(f.positional, args[i+1:]...)
			return f
		case a == "-t" && i+1 < len(args):
			f.target = args[i+1]
			i += 2
		case a == "-s" && i+1 < len(args):
			f.sName = args[i+1]
			i += 2
		case a == "-n" && i+1 < len(args):
			f.window = args[i+1]
			i += 2
		case a == "-c" && i+1 < len(args):
			f.cwd = args[i+1]
			i += 2
		case a == "-F" && i+1 < len(args):
			f.format = args[i+1]
			i += 2
		case a == "-S" && i+1 < len(args):
			i += 2
		case a == "-l":
			f.literal = true
			i++
		case a == "-d" || a == "-p" || a == "-J" || a == "-e" || a == "-r" ||
			a == "-Zs" || a == "-g" || a == "-o" || a == "-P":
			i++
		case strings.HasPrefix(a, "-") && len(a) > 1 && a[1] >= 'a' && a[1] <= 'z':
			i++
		default:
			f.positional = append(f.positional, a)
			i++
		}
	}
	return f
}

func ptmTranslateKeys(args []string, literal bool) string {
	if literal {
		return strings.Join(args, " ")
	}
	var b strings.Builder
	for _, a := range args {
		switch a {
		case "Enter":
			b.WriteByte('\r')
		case "Tab":
			b.WriteByte('\t')
		case "Space":
			b.WriteByte(' ')
		case "Escape":
			b.WriteByte(0x1b)
		case "BSpace":
			b.WriteByte(0x7f)
		case "Up":
			b.WriteString("\x1b[A")
		case "Down":
			b.WriteString("\x1b[B")
		case "Right":
			b.WriteString("\x1b[C")
		case "Left":
			b.WriteString("\x1b[D")
		case "C-c":
			b.WriteByte(0x03)
		case "C-u":
			b.WriteByte(0x15)
		case "C-l":
			b.WriteByte(0x0c)
		case "C-d":
			b.WriteByte(0x04)
		default:
			b.WriteString(a)
		}
	}
	return b.String()
}
