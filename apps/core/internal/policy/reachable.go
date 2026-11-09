package policy

import (
	"regexp"
	"strings"
)

// The ONLY reachability exemption: a leading, literal missing-env guard that
// exits before anything else runs. No arbitrary control-flow interpretation.
var missingGuard = regexp.MustCompile(`(?s)^(?:(?:[ \t]*#[^\n]*\n)|(?:[ \t]*\n)|(?:[ \t]*set -[eu]+[ \t]*\n))*[ \t]*if[ \t]+\[[ \t]+-z[ \t]+"\$\{([A-Za-z_][A-Za-z0-9_]*):-\}"[ \t]+\];[ \t]*(?:\n[ \t]*)?then[ \t\n]+exit[ \t]+[0-9]+[ \t]*;[ \t\n]*fi(?:[ \t]*\n|[ \t]*;|$)`)

func ReachableShell(cmd string, unset []string) string {
	m := missingGuard.FindStringSubmatch(cmd)
	if len(m) != 2 {
		return cmd
	}
	for _, v := range unset {
		if v == m[1] {
			return "true"
		}
	}
	return cmd
}
func explicitUnset(argv []string) []string {
	if len(argv) == 0 {
		return nil
	}
	if argv[0] == "timeout" {
		if len(argv) < 3 || strings.HasPrefix(argv[1], "-") {
			return nil
		}
		return explicitUnset(argv[2:])
	}
	if argv[0] != "env" {
		return nil
	}
	names := []string{}
	for i := 1; i < len(argv); i++ {
		v := argv[i]
		switch {
		case v == "-u" || v == "--unset":
			if i+1 >= len(argv) {
				return nil
			}
			i++
			names = append(names, argv[i])
		case strings.HasPrefix(v, "--unset="):
			names = append(names, strings.TrimPrefix(v, "--unset="))
		case strings.Contains(v, "="):
			name, _, _ := strings.Cut(v, "=")
			if name == "BASH_ENV" || name == "ENV" {
				return nil
			}
			for j := 0; j < len(names); j++ {
				if names[j] == name {
					names = append(names[:j], names[j+1:]...)
					j--
				}
			}
		case v == "--":
			continue
		case strings.HasPrefix(v, "-"):
			return nil
		default:
			return names
		}
	}
	return nil
}
