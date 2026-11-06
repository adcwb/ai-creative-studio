package deployconfig

import (
	"errors"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// CanonicalOrigin accepts an origin tuple, never a wildcard, opaque origin or
// URL containing credentials, query, fragment or an application path.
func CanonicalOrigin(raw string) (string, error) {
	if raw == "" || raw != strings.TrimSpace(raw) || strings.ContainsAny(raw, "\r\n\t ,?#") {
		return "", errors.New("invalid trusted origin")
	}
	u, e := url.Parse(raw)
	if e != nil || u.Host == "" || u.Hostname() == "" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (u.Scheme != "http" && u.Scheme != "https") || strings.Contains(u.Host, "*") {
		return "", errors.New("trusted origins require explicit http(s) origins")
	}
	if strings.HasSuffix(u.Host, ":") {
		return "", errors.New("invalid origin port")
	}
	if port := u.Port(); port != "" {
		n, e := strconv.Atoi(port)
		if e != nil || n < 1 || n > 65535 {
			return "", errors.New("invalid origin port")
		}
	}
	host := strings.ToLower(u.Host)
	if u.Scheme == "https" && u.Port() == "443" || u.Scheme == "http" && u.Port() == "80" {
		host = strings.ToLower(u.Hostname())
		if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
	}
	return u.Scheme + "://" + host, nil
}
func TrustedOrigins(primary string, additional []string) (map[string]bool, error) {
	origins := map[string]bool{}
	for _, raw := range append([]string{primary}, additional...) {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		v, e := CanonicalOrigin(strings.TrimSpace(raw))
		if e != nil {
			return nil, e
		}
		origins[v] = true
	}
	if len(origins) == 0 {
		return nil, errors.New("at least one trusted frontend origin is required")
	}
	return origins, nil
}
func AdminUserIDs() []string {
	return strings.Split(strings.TrimSpace(os.Getenv("STUDIO_ADMIN_USER_IDS")), ",")
}
func IsAdminID(id string, ids []string) bool {
	if id == "" {
		return false
	}
	for _, v := range ids {
		if strings.TrimSpace(v) == id {
			return true
		}
	}
	return false
}
