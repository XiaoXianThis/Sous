// Package project holds pure helpers shared by the Hub and the node: how a
// git remote identifies a project, and which project names are valid.
package project

import (
	"regexp"
	"strings"
)

// MiscID is the built-in project for tasks that belong to no repository.
const MiscID = "misc"

// Layouts a project can have.
const (
	LayoutWokspace = "wokspace" // https://github.com/XiaoXianThis/wokspace
)

var scpLike = regexp.MustCompile(`^([^@/:]+@)?([^/:]+):(.+)$`)

// Key normalizes a git remote so that the forms one repository goes by
// compare equal: the scheme, user and port are dropped, scp-style
// "git@host:a/b" becomes "host/a/b", the host is lowercased and a trailing
// ".git" or "/" removed. The path keeps its case (some hosts are case
// sensitive). An empty or unparsable remote gives "".
func Key(remote string) string {
	s := strings.TrimSpace(remote)
	if s == "" {
		return ""
	}
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
		if at := strings.LastIndex(strings.SplitN(s, "/", 2)[0], "@"); at >= 0 {
			s = s[at+1:]
		}
		host, path, _ := strings.Cut(s, "/")
		if h, _, ok := strings.Cut(host, ":"); ok {
			host = h
		}
		s = strings.ToLower(host) + "/" + path
	} else if m := scpLike.FindStringSubmatch(s); m != nil && !strings.HasPrefix(s, "/") {
		s = strings.ToLower(m[2]) + "/" + strings.TrimPrefix(m[3], "/")
	} else {
		return "" // a local path: no identity across machines
	}
	s = strings.TrimRight(s, "/")
	s = strings.TrimSuffix(s, ".git")
	s = strings.TrimRight(s, "/")
	if !strings.Contains(s, "/") || strings.HasSuffix(s, "/") {
		return ""
	}
	return s
}

var nameRE = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// reserved are Windows device names, never valid as a directory name.
var reserved = map[string]bool{"con": true, "prn": true, "aux": true, "nul": true}

// ValidName reports whether name can be a new project's directory: lowercase
// kebab-case ASCII, at most 64 characters, not a Windows reserved name.
func ValidName(name string) bool {
	if len(name) == 0 || len(name) > 64 || !nameRE.MatchString(name) || reserved[name] {
		return false
	}
	if len(name) == 4 && (strings.HasPrefix(name, "com") || strings.HasPrefix(name, "lpt")) && name[3] >= '1' && name[3] <= '9' {
		return false
	}
	return true
}

// NameFromKey suggests a project name from a remote key: its last segment.
func NameFromKey(key string) string {
	if i := strings.LastIndex(key, "/"); i >= 0 {
		return key[i+1:]
	}
	return key
}
