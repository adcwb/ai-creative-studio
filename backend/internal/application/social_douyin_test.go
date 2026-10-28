package application

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestDouyinHandoffOfficialSignatureAndGates(t *testing.T) {
	now := time.Unix(1650941858, 0)
	c := DouyinHandoffConfig{Approved: true, Scopes: []string{"h5.share", "aweme.share", "open.get.ticket"}, ClientKey: "synthetic-app", Ticket: "@ml6sqYBGgTKmQNajnKNkaj8yksCAY++adIhlGIqfTiKyvBqOIkzdJ6WRgP+nO+wtVItqKbX4iZ+mFIYkyPJjpQ==", TicketExpiresAt: now.Add(time.Hour), MediaPrefix: "https://media.example.test/approved/"}
	v, e := buildDouyinHandoff(c, now, "Wm3WZYTPz0wzccnW", "https://media.example.test/approved/a.mp4?x=a%2Bb", "video", "合成测试")
	if e != nil {
		t.Fatal(e)
	}
	u, _ := url.Parse(v)
	if u.Query().Get("signature") != "3f7b739a91a52cb7d85c4f89c5f611fe" || u.Query().Get("title") != "合成测试" || strings.Contains(v, c.Ticket) {
		t.Fatal("signature/encoding/secret exposure")
	}
	for _, bad := range []string{"https://media.example.test.evil/approved/a.mp4", "https://media.example.test/approved-other/a.mp4", "https://media.example.test/approved/%2e%2e/private", "http://media.example.test/approved/a.mp4", "https://user@media.example.test/approved/a.mp4"} {
		if _, e = buildDouyinHandoff(c, now, "Wm3WZYTPz0wzccnW", bad, "video", ""); e == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
	c.Approved = false
	if _, e = buildDouyinHandoff(c, now, "Wm3WZYTPz0wzccnW", "https://media.example.test/approved/a.mp4", "video", ""); e == nil {
		t.Fatal("approval bypass")
	}
	c.Approved = true
	c.Scopes = nil
	if _, e = buildDouyinHandoff(c, now, "Wm3WZYTPz0wzccnW", "https://media.example.test/approved/a.mp4", "video", ""); e == nil {
		t.Fatal("scope bypass")
	}
}
