package application

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DouyinHandoffConfig is operator supplied. Neither ticket nor approval can be
// supplied by a browser. A media broker must resolve an owned, consented asset
// to an expiring URL under an approved origin/path before calling this adapter.
// This adapter never fetches a URL or claims that the App published a post.
type DouyinHandoffConfig struct {
	Approved        bool
	Scopes          []string
	ClientKey       string
	Ticket          string
	TicketExpiresAt time.Time
	MediaPrefix     string
}

func buildDouyinHandoff(c DouyinHandoffConfig, now time.Time, nonce, source, kind, caption string) (string, error) {
	reject := errors.New("douyin_handoff_not_configured")
	scopes := map[string]bool{}
	for _, s := range c.Scopes {
		scopes[s] = true
	}
	if !c.Approved || !scopes["h5.share"] || !scopes["aweme.share"] || !scopes["open.get.ticket"] || c.ClientKey == "" || len(c.ClientKey) > 128 || c.Ticket == "" || len(c.Ticket) > 4096 || !c.TicketExpiresAt.After(now.Add(time.Minute)) {
		return "", reject
	}
	if !shotIDPattern.MatchString(nonce) || len(nonce) < 16 || !socialText(caption, 90) {
		return "", mediaInvalid("invalid Douyin share input")
	}
	prefix, err := url.Parse(c.MediaPrefix)
	if err != nil || prefix.Scheme != "https" || prefix.Host == "" || prefix.User != nil || prefix.RawQuery != "" || prefix.Fragment != "" || !strings.HasSuffix(prefix.Path, "/") {
		return "", reject
	}
	u, err := url.Parse(source)
	if err != nil || u.Scheme != "https" || u.Host != prefix.Host || u.User != nil || u.Fragment != "" || !strings.HasPrefix(u.EscapedPath(), prefix.EscapedPath()) || strings.Contains(u.Path, "..") || strings.ContainsAny(u.Path, "\\\r\n") {
		return "", mediaInvalid("unapproved social media URL")
	}
	timestamp := strconv.FormatInt(now.Unix(), 10)
	digest := md5.Sum([]byte("nonce_str=" + nonce + "&ticket=" + c.Ticket + "&timestamp=" + timestamp))
	q := url.Values{"share_type": {"h5"}, "client_key": {c.ClientKey}, "nonce_str": {nonce}, "timestamp": {timestamp}, "signature": {hex.EncodeToString(digest[:])}, "share_to_type": {"0"}}
	switch kind {
	case "video":
		q.Set("video_path", source)
		q.Set("share_to_publish", "1")
	case "image":
		q.Set("image_path", source)
	default:
		return "", mediaInvalid("unsupported Douyin media kind")
	}
	if caption != "" {
		q.Set("title", caption)
	}
	return "snssdk1128://openplatform/share?" + q.Encode(), nil
}
