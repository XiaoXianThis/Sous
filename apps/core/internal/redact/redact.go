// Package redact hides what looks like a credential in text that is logged
// or shown: URLs with passwords, bearer tokens, well-known key formats,
// NAME=value pairs whose name says secret, and private key blocks. It is a
// best effort for display, not a security boundary.
package redact

import "regexp"

var patterns = []struct {
	re   *regexp.Regexp
	repl string
}{
	{regexp.MustCompile(`(?i)("[A-Z0-9_]*(?:SECRET|TOKEN|PASSWORD|PASSWD|API_?KEY|PRIVATE_KEY|AUTHORIZATION|CREDENTIAL)[A-Z0-9_]*"\s*:\s*)"(?:\\.|[^"\\])*(?:"|$)`), `${1}"***"`},
	{regexp.MustCompile(`(?i)(://[^/\s:@]+:)[^@\s/]+@`), "${1}***@"},
	{regexp.MustCompile(`(?i)\b(bearer|token|basic)\s+[A-Za-z0-9._~+/=-]{12,}`), "${1} ***"},
	{regexp.MustCompile(`(?i)\b(gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|sk-[A-Za-z0-9_-]{16,}|xox[abpr]-[A-Za-z0-9-]{10,}|AKIA[0-9A-Z]{16}|AIza[0-9A-Za-z_-]{30,})`), "***"},
	{regexp.MustCompile(`(?i)\b([A-Z0-9_]*(?:SECRET|TOKEN|PASSWORD|PASSWD|API_?KEY|PRIVATE_KEY)[A-Z0-9_]*\s*[=:]\s*)\S+`), "${1}***"},
	{regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?(-----END [A-Z ]*PRIVATE KEY-----|$)`), "***"},
}

// Mask replaces what looks like a secret in s with ***.
func Mask(s string) string {
	for _, p := range patterns {
		s = p.re.ReplaceAllString(s, p.repl)
	}
	return s
}

// secretName matches argument or field names whose values are credentials.
var secretName = regexp.MustCompile(`(?i)(secret|token|passw|api_?key|apikey|private_?key|credential|cookie|auth)`)

// SecretName reports whether a field called name holds a credential.
func SecretName(name string) bool { return secretName.MatchString(name) }
