package application

import (
	"net/url"
	"strings"
)

const (
	gatewayOpenAI  = "openai-compatible"
	gatewayBailian = "aliyun-bailian"
)

// Missing vendors preserve the protocol of snapshots written before vendors existed.
func gatewayVendor(v string) string {
	if v == "" {
		return gatewayOpenAI
	}
	return v
}

// Each vendor owns its API prefix. Accept the documented root and compatibility
// prefix as input for Bailian, but persist its native API prefix consistently.
func normalizeVendorGatewayURL(vendor, raw string) (string, error) {
	if gatewayVendor(vendor) == gatewayOpenAI {
		return normalizeGatewayURL(raw)
	}
	if vendor != gatewayBailian {
		return "", errModelInput
	}
	normalized, err := normalizeGatewayURL(raw)
	if err != nil {
		return "", err
	}
	u, _ := url.Parse(normalized)
	switch u.Path {
	case "/v1", "/compatible-mode/v1", "/api/v1":
		u.Path = "/api/v1"
	default:
		if !strings.HasSuffix(u.Path, "/api/v1") {
			return "", errModelInput
		}
	}
	return u.String(), nil
}
