package config

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

// ExternalMCPClient is an operator-registered public OAuth client. Its ID is
// not a secret. Authorizations to actual workspace data are separately granted
// by the Passkey owner, and always use PKCE with an exact redirect URI.
type ExternalMCPClient struct {
	ID          string `yaml:"id"`
	Name        string `yaml:"name"`
	RedirectURI string `yaml:"redirect_uri"`
}

var clientName = regexp.MustCompile(`^[A-Za-z0-9._-]{3,80}$`)

func validateExternalClients(clients []ExternalMCPClient) error {
	if len(clients) > 16 {
		return errors.New("at most 16 external MCP clients can be registered")
	}
	seen := map[string]bool{}
	for _, c := range clients {
		if !clientName.MatchString(c.ID) || seen[c.ID] || strings.TrimSpace(c.Name) != c.Name || c.Name == "" || len(c.Name) > 120 || strings.ContainsFunc(c.Name, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }) {
			return errors.New("invalid or duplicate external MCP client")
		}
		u, err := url.Parse(c.RedirectURI)
		if err != nil || len(c.RedirectURI) > 1024 || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Path == "" || u.RawQuery != "" || u.ForceQuery || strings.Contains(c.RedirectURI, "#") || u.Opaque != "" {
			return errors.New("external MCP redirect must be a fixed HTTPS URI with a path, no query, credentials or fragment")
		}
		seen[c.ID] = true
	}
	return nil
}
