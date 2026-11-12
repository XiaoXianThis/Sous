package policy

import "testing"

func TestReadOnlyCompleteArguments(t *testing.T) {
	needShell(t)
	p, _ := New(Config{})
	groups := []struct{ name, good, bad, mixed string }{
		{"hostname", "hostname -f", "hostname new-name", "hostname -f --file names"},
		{"who", "who -a", "who --bad", "who -a --bad"},
		{"w", "w -h user", "w --bad", "w -h --bad"},
		{"last", "last -n 5", "last --bad", "last -n 5 --bad"},
		{"lastb", "lastb -n 5", "lastb --bad", "lastb -n 5 --bad"},
		{"lsblk", "lsblk --json", "lsblk --bad", "lsblk --json --bad"},
		{"lscpu", "lscpu -e", "lscpu --bad", "lscpu -e --bad"},
		{"getent", "getent hosts localhost", "getent shadow", "getent hosts --service=evil"},
		{"journalctl", "journalctl -u ssh --since today --no-pager", "journalctl --rotate", "journalctl -u ssh --sync"},
		{"ip", "ip -br addr show dev eth0", "ip link set eth0 down", "ip addr show --batch file"},
		{"iptables", "iptables -t filter -S", "iptables -F", "iptables -S -A INPUT"},
		{"ip6tables", "ip6tables -L -n", "ip6tables -X", "ip6tables -L --flush"},
		{"nft", "nft -n list ruleset", "nft flush ruleset", "nft list ruleset -f file"},
		{"ufw", "ufw status verbose", "ufw enable", "ufw status enable"},
		{"nginx", "nginx -t", "nginx -s reload", "nginx -t -g daemon"},
		{"crontab", "crontab -l", "crontab -r", "crontab -l -e"},
		{"systemctl", "systemctl --no-pager list-timers --all", "systemctl restart app", "systemctl list-timers --host=remote"},
		{"docker", "docker stats --no-stream app", "docker run app", "docker stats --no-stream --format=evil"},
		{"podman", "podman top app", "podman exec app sh", "podman top app --bad"},
		{"git-rev-list", "git rev-list --count HEAD", "git rev-list --output=file HEAD", "git rev-list --count --output=file HEAD"},
		{"git-shortlog", "git shortlog -s HEAD", "git shortlog --bad", "git shortlog -s --bad"},
		{"git-reflog", "git reflog show --oneline", "git reflog expire --all", "git reflog show --output=file"},
		{"xxd", "xxd -l 16 input", "xxd -r input", "xxd -l 16 input output"},
		{"zcat", "zcat -q input.gz", "zcat --bad", "zcat -q --bad"},
		{"zgrep", "zgrep -i pattern input.gz", "zgrep --bad", "zgrep -i --bad"},
		{"column", "column -t input", "column --bad", "column -t --bad"},
		{"tr", "tr -d x", "tr --bad", "tr -d --bad"},
		{"nl", "nl -b a input", "nl --bad", "nl -b a --bad"},
		{"od", "od -x input", "od --bad", "od -x --bad"},
		{"strings", "strings -a input", "strings --bad", "strings -a --bad"},
		{"sous", "sous status", "sous service start", "sous status --bad"},
	}
	for _, g := range groups {
		t.Run(g.name, func(t *testing.T) {
			for i, cmd := range []string{g.good, g.bad, g.mixed} {
				r := exec(cmd)
				c, _ := p.Classify(r)
				act, _ := Judge(RoleServer, ModeReadOnly, c, p.Assess(r, c))
				if (act == Allow) != (i == 0) {
					t.Fatalf("%q: category=%s action=%s", cmd, c, act)
				}
			}
		})
	}
}
