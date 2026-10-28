package application

import (
	"bytes"
	"context"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"time"
)

// A deployment application token is shared by all tenants and API processes.
// Issuing another token invalidates the old token, so an in-memory/per-project
// cache is unsafe. Private receipts provide the existing durable global media
// transaction in both single-process file mode and multi-instance PostgreSQL.
type douyinTicketGuardKey struct{}

func douyinTicketAuthorized(ctx context.Context) bool {
	guard, ok := ctx.Value(douyinTicketGuardKey{}).(func() bool)
	return ctx.Err() == nil && ok && guard()
}

type douyinTicketProvider interface {
	Ready() bool
	Ticket(context.Context) (string, time.Time, error)
}
type douyinTicketCache struct {
	Phase             string `json:"phase"`
	Attempt           string `json:"attempt"`
	ClientToken       string `json:"client_token,omitempty"`
	TokenExpires      int64  `json:"token_expires,omitempty"`
	Ticket            string `json:"ticket,omitempty"`
	TicketExpires     int64  `json:"ticket_expires,omitempty"`
	RetryAfter        int64  `json:"retry_after,omitempty"`
	SecretFingerprint string `json:"secret_fingerprint"`
}
type douyinTickets struct {
	store                            *MediaStore
	key                              cipher.AEAD
	clientKey, clientSecret, cacheID string
	client                           *http.Client
	now                              func() time.Time
}

var errDouyinTicket = errors.New("Douyin ticket unavailable; operator configuration or reconciliation required")

func newDouyinTicketProviderFromEnv(s *FileStore, key cipher.AEAD, clientKey string) douyinTicketProvider {
	if os.Getenv("SOCIAL_DOUYIN_TICKET_FETCH_ENABLED") != "true" {
		return nil
	}
	if s == nil || s.media == nil || key == nil || clientKey == "" {
		return &douyinTickets{}
	}
	secret := os.Getenv("SOCIAL_DOUYIN_CLIENT_SECRET")
	if secret == "" {
		return &douyinTickets{}
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	return &douyinTickets{store: s.media, key: key, clientKey: clientKey, clientSecret: secret, cacheID: "__social_platform__:douyin:" + authDigest(clientKey), client: &http.Client{Transport: tr, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, now: time.Now}
}
func (p *douyinTickets) Ready() bool {
	return p != nil && p.store != nil && p.key != nil && p.client != nil && p.now != nil && p.clientKey != "" && p.clientSecret != ""
}
func (p *douyinTickets) load(d mediaDisk) (douyinTicketCache, error) {
	var c douyinTicketCache
	record, exists := d.Receipts[p.cacheID]
	if !exists {
		return c, nil
	}
	b, e := base64.RawStdEncoding.DecodeString(record.SocialSecret)
	if e != nil || len(b) < p.key.NonceSize() {
		return c, errDouyinTicket
	}
	n := p.key.NonceSize()
	raw, e := p.key.Open(nil, b[:n], b[n:], []byte(p.cacheID))
	if e != nil || json.Unmarshal(raw, &c) != nil || c.SecretFingerprint != authDigest(p.clientSecret) {
		return c, errDouyinTicket
	}
	return c, nil
}
func (p *douyinTickets) save(d mediaDisk, c douyinTicketCache) error {
	c.SecretFingerprint = authDigest(p.clientSecret)
	raw, e := json.Marshal(c)
	if e != nil {
		return e
	}
	nonce := make([]byte, p.key.NonceSize())
	if _, e = rand.Read(nonce); e != nil {
		return e
	}
	sealed := p.key.Seal(nonce, nonce, raw, []byte(p.cacheID))
	d.Receipts[p.cacheID] = mediaReceipt{SocialSecret: base64.RawStdEncoding.EncodeToString(sealed)}
	return p.store.commit(d)
}
func (p *douyinTickets) claim() (douyinTicketCache, bool, error) {
	p.store.mu.Lock()
	defer p.store.mu.Unlock()
	d := p.store.clone()
	if p.store.failure != nil {
		return douyinTicketCache{}, false, errDouyinTicket
	}
	c, e := p.load(d)
	if e != nil {
		return c, false, e
	}
	now := p.now().Unix()
	if c.Ticket != "" && c.TicketExpires > now+90 {
		return c, false, nil
	}
	// Never automatically repeat an uncertain token POST, including after restart.
	if c.Phase == "token_issuing" || c.Phase == "token_unknown" {
		return c, false, errDouyinTicket
	}
	if c.RetryAfter > now {
		return c, false, errDouyinTicket
	}
	if c.ClientToken == "" || c.TokenExpires <= now+90 {
		c.Phase = "token_issuing"
		c.Attempt = mediaID()
		c.ClientToken = ""
		c.Ticket = ""
		c.RetryAfter = 0
		return c, true, p.save(d, c)
	}
	// A crashed GET may be retried only after its lease; no new token is issued.
	c.Phase = "ticket_fetching"
	c.Attempt = mediaID()
	c.RetryAfter = now + 60
	return c, true, p.save(d, c)
}
func (p *douyinTickets) update(attempt string, change func(*douyinTicketCache)) (douyinTicketCache, error) {
	p.store.mu.Lock()
	defer p.store.mu.Unlock()
	d := p.store.clone()
	c, e := p.load(d)
	if e != nil || c.Attempt != attempt {
		return c, errDouyinTicket
	}
	change(&c)
	return c, p.save(d, c)
}

type douyinTicketResponse struct {
	Data struct {
		ErrorCode   *int64 `json:"error_code"`
		AccessToken string `json:"access_token"`
		Ticket      string `json:"ticket"`
		Expires     int64  `json:"expires_in"`
	} `json:"data"`
}

func (p *douyinTickets) request(ctx context.Context, token string) (douyinTicketResponse, error) {
	var out douyinTicketResponse
	if !douyinTicketAuthorized(ctx) {
		return out, errDouyinTicket
	}
	method, path := http.MethodGet, "/open/getticket/"
	var body io.Reader
	if token == "" {
		method, path = http.MethodPost, "/oauth/client_token/"
		b, _ := json.Marshal(map[string]string{"grant_type": "client_credential", "client_key": p.clientKey, "client_secret": p.clientSecret})
		body = bytes.NewReader(b)
	}
	req, e := http.NewRequestWithContext(ctx, method, "https://open.douyin.com"+path, body)
	if e != nil {
		return out, errDouyinTicket
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("access-token", token)
	}
	res, e := p.client.Do(req)
	if e != nil {
		return out, errDouyinTicket
	}
	defer res.Body.Close()
	if !douyinTicketAuthorized(ctx) {
		return out, errDouyinTicket
	}
	if res.StatusCode == http.StatusTooManyRequests {
		return out, socialRateLimit{socialRetryDelay(res.Header.Get("Retry-After"), p.now())}
	}
	if res.StatusCode != 200 {
		return out, errDouyinTicket
	}
	raw, e := io.ReadAll(io.LimitReader(res.Body, 32<<10))
	if e != nil || len(raw) >= 32<<10 || json.Unmarshal(raw, &out) != nil || out.Data.ErrorCode == nil || *out.Data.ErrorCode != 0 || out.Data.Expires <= 120 || out.Data.Expires > 7200 {
		return out, errDouyinTicket
	}
	value := out.Data.Ticket
	if token == "" {
		value = out.Data.AccessToken
	}
	if len(value) == 0 || len(value) > 4096 {
		return out, errDouyinTicket
	}
	return out, nil
}
func (p *douyinTickets) Ticket(ctx context.Context) (string, time.Time, error) {
	if !p.Ready() || !douyinTicketAuthorized(ctx) {
		return "", time.Time{}, errDouyinTicket
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	c, fetch, e := p.claim()
	if e != nil {
		return "", time.Time{}, e
	}
	if !fetch {
		return c.Ticket, time.Unix(c.TicketExpires, 0), nil
	}
	if c.Phase == "token_issuing" {
		out, e := p.request(ctx, "")
		if e != nil || !douyinTicketAuthorized(ctx) {
			_, _ = p.update(c.Attempt, func(v *douyinTicketCache) { v.Phase = "token_unknown" })
			return "", time.Time{}, errDouyinTicket
		}
		c, e = p.update(c.Attempt, func(v *douyinTicketCache) {
			v.ClientToken = out.Data.AccessToken
			v.TokenExpires = p.now().Add(time.Duration(out.Data.Expires) * time.Second).Unix()
			v.Phase = "ticket_fetching"
			v.RetryAfter = p.now().Add(60 * time.Second).Unix()
		})
		if e != nil {
			return "", time.Time{}, e
		}
	}
	out, e := p.request(ctx, c.ClientToken)
	if e != nil || !douyinTicketAuthorized(ctx) {
		delay := time.Minute
		var rate socialRateLimit
		if errors.As(e, &rate) {
			delay = rate.Delay
		}
		_, _ = p.update(c.Attempt, func(v *douyinTicketCache) {
			v.Phase = "ticket_unavailable"
			v.RetryAfter = p.now().Add(delay).Unix()
		})
		return "", time.Time{}, errDouyinTicket
	}
	c, e = p.update(c.Attempt, func(v *douyinTicketCache) {
		v.Ticket = out.Data.Ticket
		v.TicketExpires = p.now().Add(time.Duration(out.Data.Expires) * time.Second).Unix()
		v.Phase = "ready"
		v.RetryAfter = 0
	})
	if e != nil {
		return "", time.Time{}, e
	}
	return c.Ticket, time.Unix(c.TicketExpires, 0), nil
}
