package courses

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

type LinkRejection string

const (
	linkRejectedEmpty             LinkRejection = "empty"
	linkRejectedControlCharacter  LinkRejection = "control_character"
	linkRejectedUnsupportedScheme LinkRejection = "unsupported_scheme"
	linkRejectedMissingHost       LinkRejection = "missing_host"
	linkRejectedCredentials       LinkRejection = "credentials"
	linkRejectedInvalidPort       LinkRejection = "invalid_port"
	linkRejectedLocalTarget       LinkRejection = "local_target"
	linkRejectedInvalidMagnet     LinkRejection = "invalid_magnet"
	linkRejectedInvalidURL        LinkRejection = "invalid_url"
)

var (
	btihHexPattern    = regexp.MustCompile(`(?i)^[0-9a-f]{40}$`)
	btihBase32Pattern = regexp.MustCompile(`(?i)^[a-z2-7]{32}$`)
)

func inspectLink(raw string) (parsed *url.URL, canonical string, rejection LinkRejection) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, "", linkRejectedEmpty
	}
	if containsControlCharacter(trimmed) {
		return nil, "", linkRejectedControlCharacter
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		if strings.Contains(err.Error(), "invalid port") {
			return nil, "", linkRejectedInvalidPort
		}
		return nil, "", linkRejectedInvalidURL
	}

	scheme := strings.ToLower(parsed.Scheme)
	switch scheme {
	case "http", "https":
		canonical, rejection = inspectHTTPLink(parsed, scheme)
	case "magnet":
		canonical, rejection = inspectMagnetLink(parsed, trimmed)
	default:
		rejection = linkRejectedUnsupportedScheme
	}
	if rejection != "" {
		return parsed, "", rejection
	}
	return parsed, canonical, ""
}

func linkKey(raw string) (string, error) {
	_, canonical, rejection := inspectLink(raw)
	if rejection != "" {
		return "", errors.New(string(rejection))
	}
	return canonical, nil
}

func linkHash(raw string) (string, error) {
	key, err := linkKey(raw)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:]), nil
}

func inspectHTTPLink(parsed *url.URL, scheme string) (string, LinkRejection) {
	if parsed.User != nil {
		return "", linkRejectedCredentials
	}
	host := parsed.Hostname()
	if host == "" {
		return "", linkRejectedMissingHost
	}
	port, hasPort, ok := linkPort(parsed)
	if !ok {
		return "", linkRejectedInvalidPort
	}
	if isLocalTarget(host) {
		return "", linkRejectedLocalTarget
	}

	canonical := *parsed
	canonical.Scheme = scheme
	canonical.User = nil
	canonical.Host = canonicalHost(host, port, hasPort, scheme)
	canonical.Fragment = ""
	canonical.RawFragment = ""
	return canonical.String(), ""
}

func inspectMagnetLink(parsed *url.URL, trimmed string) (string, LinkRejection) {
	values, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return "", linkRejectedInvalidMagnet
	}
	for _, value := range values["xt"] {
		hash, ok := strings.CutPrefix(strings.ToLower(value), "urn:btih:")
		if !ok {
			continue
		}
		if btihHexPattern.MatchString(hash) || btihBase32Pattern.MatchString(hash) {
			canonical := *parsed
			canonical.Scheme = "magnet"
			canonical.Fragment = ""
			canonical.RawFragment = ""
			return canonical.String(), ""
		}
	}
	return "", linkRejectedInvalidMagnet
}

func containsControlCharacter(value string) bool {
	return strings.ContainsFunc(value, unicode.IsControl)
}

func linkPort(parsed *url.URL) (port int, hasPort, ok bool) {
	if !hasExplicitPort(parsed.Host) {
		return 0, false, true
	}
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	raw := parsed.Port()
	if raw == "" {
		return 0, true, false
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 || value > 65535 {
		return 0, true, false
	}
	return value, true, true
}

func hasExplicitPort(host string) bool {
	if strings.HasPrefix(host, "[") {
		closing := strings.LastIndex(host, "]")
		return closing >= 0 && closing+1 < len(host) && host[closing+1] == ':'
	}
	return strings.LastIndex(host, ":") >= 0
}

func canonicalHost(host string, port int, hasPort bool, scheme string) string {
	host = strings.ToLower(host)
	if hasPort && ((scheme == "http" && port == 80) || (scheme == "https" && port == 443)) {
		hasPort = false
	}
	if !hasPort {
		if strings.Contains(host, ":") {
			return "[" + host + "]"
		}
		return host
	}
	return net.JoinHostPort(host, strconv.Itoa(port))
}

func isLocalTarget(host string) bool {
	normalized := strings.TrimSuffix(strings.ToLower(host), ".")
	if normalized == "localhost" || strings.HasSuffix(normalized, ".localhost") {
		return true
	}
	ip := net.ParseIP(normalized)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() ||
		ip.IsMulticast()
}
