package deployconfig

import "testing"

func TestExplicitTrustedOriginTuples(t *testing.T) {
	for _, raw := range []string{"*", "null", "https://*.example.test", "https://name:pass@example.test", "https://example.test/path", "https://example.test?q=secret", "https://example.test#x", "http://:80", "file://example.test", "https://example.test:999999", "https://example.test:", "https://a.test https://b.test"} {
		if _, err := CanonicalOrigin(raw); err == nil {
			t.Fatalf("invalid origin accepted %q", raw)
		}
	}
	origins, err := TrustedOrigins("https://Studio.example.test:443", []string{"http://localhost:5175"})
	if err != nil || !origins["https://studio.example.test"] || !origins["http://localhost:5175"] {
		t.Fatal("valid origins failed", err)
	}
	if IsAdminID("", []string{""}) || IsAdminID("different", []string{"fixed"}) || !IsAdminID("fixed", []string{" fixed "}) {
		t.Fatal("immutable administrator match")
	}
}
