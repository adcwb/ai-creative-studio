package application

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func ticketContext() context.Context {
	return context.WithValue(context.Background(), douyinTicketGuardKey{}, func() bool { return true })
}
func ticketTestProvider(t *testing.T, m *MediaStore, transport socialRoundTrip, now func() time.Time) *douyinTickets {
	r := testSocialRuntime(t, m, transport)
	return &douyinTickets{store: m, key: r.key, clientKey: "synthetic-douyin-client", clientSecret: "synthetic-douyin-secret", cacheID: "__social_platform__:douyin:" + authDigest("synthetic-douyin-client"), client: r.client, now: now}
}
func ticketReply(token bool, ttl int64) *http.Response {
	data := map[string]any{"error_code": 0, "expires_in": ttl}
	if token {
		data["access_token"] = "synthetic-douyin-token"
	} else {
		data["ticket"] = "synthetic-douyin-ticket"
	}
	return socialJSON(map[string]any{"data": data})
}
func TestDouyinTicketCacheProtocolExpiryAndRestart(t *testing.T) {
	dir := t.TempDir()
	m, _ := openMediaStore(dir)
	clock := time.Now()
	tokens, tickets := 0, 0
	client := socialRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "https" || r.URL.Host != "open.douyin.com" || r.URL.RawQuery != "" || r.Header.Get("Content-Type") != "application/json" {
			t.Fatal("unsafe endpoint")
		}
		if r.URL.Path == "/oauth/client_token/" {
			tokens++
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if r.Method != "POST" || body["grant_type"] != "client_credential" || body["client_secret"] != "synthetic-douyin-secret" {
				t.Fatal("token schema")
			}
			return ticketReply(true, 7200), nil
		}
		tickets++
		if r.URL.Path != "/open/getticket/" || r.Method != "GET" || r.Header.Get("access-token") != "synthetic-douyin-token" {
			t.Fatal("ticket schema")
		}
		return ticketReply(false, 600), nil
	})
	p := ticketTestProvider(t, m, client, func() time.Time { return clock })
	if !p.Ready() || tokens != 0 {
		t.Fatal("readiness sent request")
	}
	ticket, expiry, e := p.Ticket(ticketContext())
	if e != nil || ticket != "synthetic-douyin-ticket" || !expiry.Equal(time.Unix(clock.Unix()+600, 0)) {
		t.Fatal(ticket, expiry, e)
	}
	_, _, e = p.Ticket(ticketContext())
	if e != nil || tokens != 1 || tickets != 1 {
		t.Fatal("cache miss", e)
	}
	raw, _ := os.ReadFile(filepath.Join(m.dir, "state.json"))
	if strings.Contains(string(raw), "synthetic-douyin-token") || strings.Contains(string(raw), "synthetic-douyin-ticket") || strings.Contains(string(raw), "synthetic-douyin-secret") {
		t.Fatal("plaintext cache")
	}
	m.close()
	m, e = openMediaStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer m.close()
	p.store = m
	_, _, e = p.Ticket(ticketContext())
	if e != nil || tokens != 1 {
		t.Fatal("restart reissued token", e)
	}
	clock = clock.Add(550 * time.Second)
	_, _, e = p.Ticket(ticketContext())
	if e != nil || tokens != 1 || tickets != 2 {
		t.Fatal("ticket refresh did not reuse token", e)
	}
	clock = clock.Add(7200 * time.Second)
	_, _, e = p.Ticket(ticketContext())
	if e != nil || tokens != 2 || tickets != 3 {
		t.Fatal("expired token not refreshed", e)
	}
}
func TestDouyinTicketUnknownNeverReissuesAndGETCooldown(t *testing.T) {
	for _, failure := range []string{"token", "ticket"} {
		t.Run(failure, func(t *testing.T) {
			m, _ := openMediaStore(t.TempDir())
			defer m.close()
			clock := time.Now()
			tokens, tickets := 0, 0
			p := ticketTestProvider(t, m, func(r *http.Request) (*http.Response, error) {
				if r.Method == "POST" {
					tokens++
					if failure == "token" {
						return nil, errors.New("synthetic uncertainty")
					}
					return ticketReply(true, 7200), nil
				}
				tickets++
				if tickets == 1 {
					return nil, errors.New("synthetic timeout")
				}
				return ticketReply(false, 7200), nil
			}, func() time.Time { return clock })
			if _, _, e := p.Ticket(ticketContext()); e == nil {
				t.Fatal("failed request succeeded")
			}
			if _, _, e := p.Ticket(ticketContext()); e == nil {
				t.Fatal("blind retry")
			}
			if tokens != 1 {
				t.Fatal("token reissued")
			}
			clock = clock.Add(61 * time.Second)
			_, _, e := p.Ticket(ticketContext())
			if failure == "token" {
				if e == nil || tokens != 1 {
					t.Fatal("unknown token replay")
				}
			} else if e != nil || tokens != 1 || tickets != 2 {
				t.Fatal("GET retry failed withouttokenreissue", e)
			}
		})
	}
}
func TestDouyinTicketRejectMalformedAndRevokedActor(t *testing.T) {
	for _, bad := range []string{`{"data":{"error_code":1,"expires_in":7200,"access_token":"bad"}}`, `{"data":{"expires_in":7200,"access_token":"bad"}}`, `{"data":{"error_code":0,"expires_in":7201,"access_token":"bad"}}`, `{"data":{"error_code":0,"expires_in":120,"access_token":"bad"}}`} {
		m, _ := openMediaStore(t.TempDir())
		p := ticketTestProvider(t, m, func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(bad))}, nil
		}, time.Now)
		if _, _, e := p.Ticket(ticketContext()); e == nil {
			t.Fatal("invalid token response accepted")
		}
		m.close()
	}
	m, _ := openMediaStore(t.TempDir())
	defer m.close()
	allowed := true
	calls := 0
	p := ticketTestProvider(t, m, func(*http.Request) (*http.Response, error) {
		calls++
		allowed = false
		return ticketReply(true, 7200), nil
	}, time.Now)
	ctx := context.WithValue(context.Background(), douyinTicketGuardKey{}, func() bool { return allowed })
	if _, _, e := p.Ticket(ctx); e == nil || calls != 1 {
		t.Fatal("actor revocation ignored")
	}
	allowed = true
	if _, _, e := p.Ticket(ctx); e == nil || calls != 1 {
		t.Fatal("revoked issuance outcome replayed")
	}
}
func TestPostgresDouyinTicketOneIssuanceAcrossInstances(t *testing.T) {
	db := postgresTestDB(t)
	a, b := postgresTestStore(t, db), postgresTestStore(t, db)
	var tokens, tickets atomic.Int32
	fn := socialRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.Method == "POST" {
			tokens.Add(1)
			time.Sleep(20 * time.Millisecond)
			return ticketReply(true, 7200), nil
		}
		tickets.Add(1)
		return ticketReply(false, 7200), nil
	})
	pa, pb := ticketTestProvider(t, a.media, fn, time.Now), ticketTestProvider(t, b.media, fn, time.Now)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p := pa
			if i%2 == 1 {
				p = pb
			}
			_, _, _ = p.Ticket(ticketContext())
		}(i)
	}
	wg.Wait()
	if tokens.Load() != 1 || tickets.Load() != 1 {
		t.Fatal("crossinstance duplicate issuance", tokens.Load(), tickets.Load())
	}
	ticket, _, e := pb.Ticket(ticketContext())
	if e != nil || ticket != "synthetic-douyin-ticket" {
		t.Fatal("PG encrypted cache recovery", e)
	}
	pb.clientSecret = "different-secret"
	if _, _, e = pb.Ticket(ticketContext()); e == nil || tokens.Load() != 1 {
		t.Fatal("secret change bypassed reconciliation")
	}
}
func TestDouyinTicketHonorsRateLimitAndReadinessIsInert(t *testing.T) {
	m, _ := openMediaStore(t.TempDir())
	defer m.close()
	clock := time.Now()
	gets := 0
	p := ticketTestProvider(t, m, func(r *http.Request) (*http.Response, error) {
		if r.Method == "POST" {
			return ticketReply(true, 7200), nil
		}
		gets++
		return &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": []string{"600"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	}, func() time.Time { return clock })
	_, _, _ = p.Ticket(ticketContext())
	clock = clock.Add(61 * time.Second)
	_, _, _ = p.Ticket(ticketContext())
	if gets != 1 {
		t.Fatal("ignored upstream cooldown")
	}
	if socialRetryDelay("999999", clock) != time.Hour || socialRetryDelay("nonsense", clock) != time.Minute {
		t.Fatal("unbounded retry delay")
	}
	t.Setenv("SOCIAL_DOUYIN_TICKET_FETCH_ENABLED", "true")
	t.Setenv("SOCIAL_DOUYIN_CLIENT_SECRET", "")
	disabled := newDouyinTicketProviderFromEnv(&FileStore{media: m}, p.key, "synthetic")
	if disabled == nil || disabled.Ready() {
		t.Fatal("invalid auto mode fell back")
	}
}
