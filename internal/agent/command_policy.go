package agent

import (
	"path/filepath"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

var readOnlyCommands = map[string]bool{
	"basename": true, "column": true, "cut": true, "df": true,
	"dirname": true, "du": true, "file": true, "free": true, "getent": true,
	"groups": true, "host": true, "id": true,
	"iostat": true, "ipcs": true, "last": true, "lscpu": true,
	"lsblk": true, "lsmod": true, "lsof": true, "mpstat": true, "netstat": true,
	"nproc": true, "nslookup": true, "pgrep": true, "pidof": true, "ping": true,
	"pwd": true, "readlink": true, "realpath": true, "ss": true,
	"stat": true, "top": true, "traceroute": true, "uname": true,
	"uptime": true, "users": true, "vmstat": true, "w": true, "wc": true,
	"who": true, "whoami": true, "which": true,
}

var sensitiveArgumentFragments = []string{
	"/etc/shad", "/etc/gshadow", "/etc/sudoers", "/etc/ssh/", "/etc/ssl/private",
	"/proc/self/environ", "/proc/1/environ", ".ssh/", "id_rsa", "id_ed25519",
	".aws/", ".kube/config", ".docker/config", ".git-credentials", ".npmrc",
	".pypirc", ".netrc", "credential", "password", "private_key", "secret", "token", "shadow",
	"/.env", "\\.env",
}

// commandRequiresApproval only skips confirmation for commands that can be
// classified as ordinary, non-sensitive inspection. Unknown shell syntax and
// commands remain approval-gated.
func commandRequiresApproval(command string) bool {
	segments, ok := readOnlyPipeline(command)
	if !ok {
		return true
	}
	for _, fields := range segments {
		if len(fields) == 0 || hasSensitiveArguments(fields[1:]) {
			return true
		}
		name, ok := trustedExecutable(fields[0])
		if !ok || !readOnlyInvocation(name, fields[1:]) {
			return true
		}
	}
	return false
}

func readOnlyPipeline(command string) ([][]string, bool) {
	command = strings.TrimSpace(command)
	if command == "" || len(command) > 6000 {
		return nil, false
	}
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(command), "agent-command")
	if err != nil || len(file.Stmts) != 1 {
		return nil, false
	}
	commands := make([][]string, 0, 4)
	valid := true
	syntax.Walk(file, func(node syntax.Node) bool {
		if !valid || node == nil {
			return valid
		}
		switch current := node.(type) {
		case *syntax.Stmt:
			if current.Semicolon.IsValid() || current.Negated || current.Background || current.Coprocess || current.Disown {
				valid = false
			}
		case *syntax.BinaryCmd:
			if current.Op != syntax.AndStmt && current.Op != syntax.Pipe {
				valid = false
			}
		case *syntax.CallExpr:
			if len(current.Assigns) > 0 || len(current.Args) == 0 {
				valid = false
				return false
			}
			arguments := make([]string, 0, len(current.Args))
			for _, word := range current.Args {
				value, ok := staticShellWord(word)
				if !ok {
					valid = false
					return false
				}
				arguments = append(arguments, value)
			}
			commands = append(commands, arguments)
		case *syntax.Redirect:
			if current.Op != syntax.RdrOut || current.N == nil || current.N.Value != "2" {
				valid = false
				return false
			}
			target, ok := staticShellWord(current.Word)
			if !ok || target != "/dev/null" {
				valid = false
				return false
			}
		case *syntax.CmdSubst, *syntax.ProcSubst, *syntax.ParamExp, *syntax.ArithmExp,
			*syntax.BraceExp, *syntax.ExtGlob, *syntax.Subshell, *syntax.Block,
			*syntax.IfClause, *syntax.WhileClause, *syntax.ForClause, *syntax.FuncDecl,
			*syntax.ArithmCmd, *syntax.LetClause, *syntax.DeclClause, *syntax.CaseClause,
			*syntax.TestClause, *syntax.TimeClause, *syntax.CoprocClause:
			valid = false
			return false
		}
		return valid
	})
	return commands, valid && len(commands) > 0
}

func staticShellWord(word *syntax.Word) (string, bool) {
	if word == nil {
		return "", false
	}
	var value strings.Builder
	var appendParts func([]syntax.WordPart) bool
	appendParts = func(parts []syntax.WordPart) bool {
		for _, part := range parts {
			switch current := part.(type) {
			case *syntax.Lit:
				value.WriteString(current.Value)
			case *syntax.SglQuoted:
				if current.Dollar {
					return false
				}
				value.WriteString(current.Value)
			case *syntax.DblQuoted:
				if current.Dollar || !appendParts(current.Parts) {
					return false
				}
			default:
				return false
			}
		}
		return true
	}
	if !appendParts(word.Parts) {
		return "", false
	}
	return value.String(), true
}

func trustedExecutable(value string) (string, bool) {
	value = strings.Trim(value, "'\"")
	if value == "" || strings.Contains(value, "=") {
		return "", false
	}
	if strings.Contains(value, "/") {
		directory := filepath.Clean(filepath.Dir(value))
		if directory != "/bin" && directory != "/usr/bin" && directory != "/usr/sbin" && directory != "/sbin" {
			return "", false
		}
	}
	return strings.ToLower(filepath.Base(value)), true
}

func hasSensitiveArguments(arguments []string) bool {
	for _, argument := range arguments {
		value := strings.ToLower(strings.Trim(argument, "'\""))
		if strings.Contains(value, "environ") && strings.Contains(value, "/proc/") {
			return true
		}
		for _, fragment := range sensitiveArgumentFragments {
			if strings.Contains(value, fragment) {
				return true
			}
		}
	}
	return false
}

func readOnlyInvocation(name string, arguments []string) bool {
	if readOnlyCommands[name] {
		return true
	}
	switch name {
	case "ls", "tree":
		return true
	case "cat", "grep", "rg", "less", "more", "head", "tail":
		return !containsGlob(arguments)
	case "find":
		return !containsOption(arguments, "-delete", "-exec", "-execdir", "-ok", "-okdir", "-fls", "-fprint", "-fprint0", "-fprintf")
	case "sort", "uniq":
		return !containsOption(arguments, "-o", "--output")
	case "printf":
		return !containsOption(arguments, "-v")
	case "awk", "gawk":
		return readOnlyAWK(arguments)
	case "ps":
		return !psExposesEnvironment(arguments)
	case "lastlog":
		return !containsOption(arguments, "-c", "--clear", "-s", "--set")
	case "git":
		return hasReadOnlySubcommand(arguments, "status", "diff", "log", "show", "rev-parse", "describe", "ls-files")
	case "docker", "podman":
		return hasReadOnlySubcommand(arguments, "ps", "images", "stats", "version", "info", "top") ||
			hasNestedReadOnlySubcommand(arguments, "container", "ls") ||
			hasNestedReadOnlySubcommand(arguments, "image", "ls")
	case "systemctl":
		return hasReadOnlySubcommand(arguments, "status", "show", "is-active", "is-enabled", "is-failed", "list-units", "list-unit-files")
	case "hostnamectl":
		return len(arguments) == 0 || hasReadOnlySubcommand(arguments, "status")
	case "hostname":
		return len(arguments) == 0 || (len(arguments) == 1 && containsOption(arguments, "-a", "--alias", "-d", "--domain", "-f", "--fqdn", "--long", "-i", "--ip-address", "--all-ip-addresses", "-s", "--short"))
	case "service":
		return len(arguments) >= 2 && strings.EqualFold(strings.Trim(arguments[len(arguments)-1], "'\""), "status")
	case "ip":
		return hasReadOnlySubcommand(arguments, "addr", "address", "link", "route", "neigh", "rule") &&
			!containsOption(arguments, "add", "append", "change", "delete", "del", "flush", "replace", "set")
	case "arp", "route":
		return !containsOption(arguments, "add", "delete", "del", "set", "-d", "-s")
	case "command":
		return len(arguments) >= 2 && arguments[0] == "-v"
	case "type":
		return len(arguments) > 0
	default:
		return false
	}
}

func readOnlyAWK(arguments []string) bool {
	program := strings.ToLower(strings.Join(arguments, " "))
	return !strings.Contains(program, "system(") &&
		!strings.Contains(program, "getline") &&
		!strings.ContainsAny(program, "><`")
}

func psExposesEnvironment(arguments []string) bool {
	for _, argument := range arguments {
		value := strings.ToLower(strings.Trim(argument, "'\""))
		if value == "e" || (!strings.HasPrefix(value, "-") && strings.HasPrefix(value, "a") && strings.Contains(value, "e")) {
			return true
		}
	}
	return false
}

func containsGlob(arguments []string) bool {
	for _, argument := range arguments {
		if strings.ContainsAny(argument, "*?[") {
			return true
		}
	}
	return false
}

func containsOption(arguments []string, blocked ...string) bool {
	for _, argument := range arguments {
		value := strings.ToLower(strings.Trim(argument, "'\";"))
		for _, item := range blocked {
			if value == item || strings.HasPrefix(value, item+"=") {
				return true
			}
		}
	}
	return false
}

func hasReadOnlySubcommand(arguments []string, allowed ...string) bool {
	for _, argument := range arguments {
		value := strings.ToLower(strings.Trim(argument, "'\""))
		if value == "" || strings.HasPrefix(value, "-") {
			continue
		}
		for _, subcommand := range allowed {
			if value == subcommand {
				return true
			}
		}
		return false
	}
	return false
}

func hasNestedReadOnlySubcommand(arguments []string, parent, child string) bool {
	values := make([]string, 0, 2)
	for _, argument := range arguments {
		value := strings.ToLower(strings.Trim(argument, "'\""))
		if value != "" && !strings.HasPrefix(value, "-") {
			values = append(values, value)
		}
		if len(values) == 2 {
			break
		}
	}
	return len(values) == 2 && values[0] == parent && values[1] == child
}
