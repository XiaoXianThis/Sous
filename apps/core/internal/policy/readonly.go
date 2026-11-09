package policy

import (
	"path/filepath"
	"strconv"
	"strings"
)

// options consumes every argument. Unknown options never inherit Read.
func options(args []string, flags, valued string, positionals bool) bool {
	has := func(list, v string) bool { return strings.Contains(" "+list+" ", " "+v+" ") }
	for i := 0; i < len(args); i++ {
		v := args[i]
		key, val, eq := strings.Cut(v, "=")
		if has(flags, key) && !eq {
			continue
		}
		if has(valued, key) {
			if !eq {
				i++
				if i >= len(args) {
					return false
				}
				val = args[i]
			}
			if val == "" || strings.HasPrefix(val, "-") {
				return false
			}
			continue
		}
		if strings.HasPrefix(v, "-") {
			return false
		}
		if !positionals {
			return false
		}
	}
	return true
}
func readOnlyInvocation(f []string) bool {
	if len(f) == 0 {
		return false
	}
	n := filepath.Base(f[0])
	as := f[1:]
	switch n {
	case "hostname":
		return options(as, "-s --short -f --fqdn -d --domain -i --ip-address -I --all-ip-addresses -A --all-fqdns -a --alias --help --version", "", false)
	case "who":
		return options(as, "-a -b -d -H -l -m -p -q -r -s -t -T -u --help --version", "", false)
	case "w":
		return options(as, "-h -s -f -i -o --help --version", "", true)
	case "last", "lastb":
		return options(as, "-a -d -F -i -R -w -x --help --version", "-n --limit -s --since -t --until", true)
	case "lsblk":
		return options(as, "-a -b -d -f -l -n -p -r -t -J --json --fs --list --noheadings --help --version", "-o --output -e --exclude -I --include -s --sort", true)
	case "lscpu":
		return options(as, "-a -b -c -e -J --json --help --version", "--extended --parse", false)
	case "getent":
		if len(as) < 1 {
			return false
		}
		switch as[0] {
		case "hosts", "ahosts", "passwd", "group", "services", "protocols", "networks":
			return options(as[1:], "", "", true)
		}
		return false
	case "journalctl":
		if !options(as, "--no-pager --utc -q --quiet -r --reverse --list-boots --disk-usage -k --dmesg -f --follow -b --boot", "-u --unit --since --until -n --lines -p --priority -o --output --identifier -t", false) {
			return false
		}
		for i, v := range as {
			if v == "-o" || v == "--output" {
				if i+1 >= len(as) || !oneOf(as[i+1], "short", "short-iso", "cat", "json") {
					return false
				}
			} else if strings.HasPrefix(v, "--output=") && !oneOf(strings.TrimPrefix(v, "--output="), "short", "short-iso", "cat", "json") {
				return false
			}
		}
		return true
	case "ip":
		for len(as) > 0 && oneOf(as[0], "-4", "-6", "-br", "-brief", "-o", "-oneline", "-s", "-j") {
			as = as[1:]
		}
		if len(as) == 0 || !oneOf(as[0], "addr", "address", "a", "route", "r", "link", "l") {
			return false
		}
		as = as[1:]
		if len(as) > 0 && as[0] == "show" {
			as = as[1:]
		}
		return options(as, "up down primary secondary scope global link host", "dev to table", false)
	case "iptables", "ip6tables":
		action := 0
		for _, v := range as {
			if oneOf(v, "-S", "--list-rules", "-L", "--list") {
				action++
			}
		}
		return action == 1 && options(as, "-S --list-rules -L --list -n --numeric -v --verbose -x --exact --line-numbers", "-t --table", true)
	case "nft":
		return strings.Join(as, " ") == "list ruleset" || strings.Join(as, " ") == "-n list ruleset"
	case "ufw":
		return len(as) >= 1 && as[0] == "status" && (len(as) == 1 || len(as) == 2 && oneOf(as[1], "verbose", "numbered"))
	case "nginx":
		return len(as) == 1 && as[0] == "-t"
	case "crontab":
		return len(as) == 1 && as[0] == "-l"
	case "systemctl":
		// No pager is allowed by the execution environment; no --root/--host.
		for len(as) > 0 && oneOf(as[0], "--no-pager", "--plain", "--all", "--no-legend") {
			as = as[1:]
		}
		return len(as) > 0 && oneOf(as[0], "status", "is-active", "list-units", "show", "cat", "list-timers", "list-unit-files", "is-enabled", "get-default") && options(as[1:], "--no-pager --plain --all --no-legend", "--type --state --property -p", true)
	case "docker", "podman":
		if len(as) < 1 {
			return false
		}
		if as[0] == "top" {
			return len(as) == 2 && !strings.HasPrefix(as[1], "-")
		}
		if as[0] == "stats" {
			has := false
			for _, v := range as[1:] {
				has = has || v == "--no-stream"
			}
			return has && options(as[1:], "--no-stream --no-trunc --all", "", true)
		}
	case "git":
		if len(as) < 1 {
			return false
		}
		switch as[0] {
		case "rev-list":
			return options(as[1:], "--all --count --left-right --cherry-pick --no-merges --reverse --objects --oneline", "--max-count -n", true)
		case "shortlog":
			return options(as[1:], "-s -n -e --all --summary --numbered --email", "", true)
		case "reflog":
			if len(as) > 1 && as[1] == "show" {
				as = append([]string{as[0]}, as[2:]...)
			}
			for _, v := range as[1:] {
				if oneOf(v, "delete", "expire", "drop", "write") {
					return false
				}
			}
			return options(as[1:], "--all --oneline --no-abbrev", "-n --max-count", true)
		}
	case "xxd":
		pos := 0
		for i := 0; i < len(as); i++ {
			v := as[i]
			if oneOf(v, "-a", "-b", "-u", "-p", "-ps", "-i") {
				continue
			}
			if oneOf(v, "-c", "-g", "-l", "-s") {
				i++
				if i >= len(as) {
					return false
				}
				if _, err := strconv.ParseUint(as[i], 0, 32); err != nil {
					return false
				}
				continue
			}
			if strings.HasPrefix(v, "-") {
				return false
			}
			pos++
		}
		return pos <= 1
	case "zcat":
		return options(as, "-f -q --quiet --help --version --", "", true)
	case "zgrep":
		return options(as, "-i -n -v -E -F -c -l -h -H -w -x --", "-e -f -m -A -B -C", true)
	case "column":
		return options(as, "-t -x -J --table --json --help --version", "-s -o -c -N -H", true)
	case "tr":
		return options(as, "-c -C -d -s -cd -cs -ds --complement --delete --squeeze-repeats --truncate-set1", "", true)
	case "nl":
		return options(as, "--help --version", "-b -d -f -h -i -l -n -s -v -w", true)
	case "od":
		return options(as, "-a -b -c -d -f -h -i -l -o -s -x -v -A --help --version", "-j -N -t -w", true)
	case "strings":
		return options(as, "-a -d -f -o --help --version", "-n -t -e", true)
	case "sous":
		return len(as) == 1 && oneOf(as[0], "status", "version", "--help")
	}
	return false
}
func oneOf(s string, vs ...string) bool {
	for _, v := range vs {
		if v == s {
			return true
		}
	}
	return false
}
