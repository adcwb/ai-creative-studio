package application

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Refresh is admitted only from an explicitly authenticated mutation, never from
// a metadata GET, readiness probe, a background loop or a publish retry.
type socialRefreshAllowedKey struct{}

func socialActorOnly(ctx context.Context) context.Context {
	return context.WithValue(ctx, socialConnectionKey{}, socialConnectionScope{})
}
func validSocialToken(t socialToken) bool {
	return t.Error == "" && t.TokenType == "Bearer" && len(t.Access) > 0 && len(t.Access) <= 8192 && len(t.Refresh) > 0 && len(t.Refresh) <= 8192 && len(t.OpenID) > 0 && len(t.OpenID) <= 1024 && t.Expires > 90 && t.Expires <= 86400 && t.RefreshExpires > 90 && t.RefreshExpires <= 365*86400 && (socialScope(t.Scope, "video.publish") || socialScope(t.Scope, "video.upload"))
}
func stampSocialToken(t *socialToken, now time.Time) {
	t.AccessExpiresAt = now.Add(time.Duration(t.Expires) * time.Second).Unix()
	t.RefreshExpiresAt = now.Add(time.Duration(t.RefreshExpires) * time.Second).Unix()
	t.RefreshAttempt = ""
}
func socialScopesSubset(next, previous string) bool {
	for _, scope := range strings.Split(next, ",") {
		if scope == "" || !socialScope(previous, scope) {
			return false
		}
	}
	return true
}
func (r *socialRuntime) authorizedToken(ctx context.Context, project, space, id, requiredScope string) (socialToken, error) {
	actorCtx := socialActorOnly(ctx)
	if e := r.checkActor(actorCtx); e != nil {
		return socialToken{}, e
	}
	token, attempt, version, e := r.claimSocialRefresh(actorCtx, project, space, id, requiredScope)
	if e != nil {
		return socialToken{}, e
	}
	if attempt == "" {
		return token, nil
	}
	// Never repeat a refresh grant. It can rotate a single-use refresh token.
	ctx, cancel := context.WithTimeout(actorCtx, 20*time.Second)
	defer cancel()
	next, invalid, e := r.exchangeSocialRefresh(ctx, token, func() bool { return r.socialRefreshActive(project, space, id, attempt, version) })
	if e != nil || !validSocialToken(next) || next.OpenID != token.OpenID || !socialScopesSubset(next.Scope, token.Scope) {
		status := "refresh_unknown"
		if invalid {
			status = "expired"
		}
		_, finishErr := r.finishSocialRefresh(project, space, id, attempt, version, status, socialToken{})
		if finishErr != nil {
			return socialToken{}, finishErr
		}
		return socialToken{}, errSocialUnavailable
	}
	if e = r.checkActor(actorCtx); e != nil {
		_, _ = r.finishSocialRefresh(project, space, id, attempt, version, "refresh_unknown", socialToken{})
		return socialToken{}, e
	}
	stampSocialToken(&next, time.Now())
	next, e = r.finishSocialRefresh(project, space, id, attempt, version, "connected", next)
	if e != nil {
		return socialToken{}, e
	}
	if requiredScope != "" && !socialScope(next.Scope, requiredScope) {
		return socialToken{}, errSocialUnavailable
	}
	return next, nil
}
func (r *socialRuntime) claimSocialRefresh(ctx context.Context, project, space, id, requiredScope string) (socialToken, string, int, error) {
	r.m.mu.Lock()
	defer r.m.mu.Unlock()
	d := r.m.clone()
	k := mediaKey(space, project)
	p := mediaState(d, k)
	ss := socialState(p)
	for i, c := range ss.Connections {
		if c.ID != id || c.Platform != "tiktok" {
			continue
		}
		if c.Status != "connected" {
			return socialToken{}, "", 0, errSocialUnavailable
		}
		private := socialPrivate(space, project, "token", id)
		var token socialToken
		if e := r.open(private, d.Receipts[private].SocialSecret, &token); e != nil {
			return token, "", 0, e
		}
		if requiredScope != "" && !socialScope(token.Scope, requiredScope) {
			return token, "", 0, errSocialUnavailable
		}
		// Pre-upgrade records have no reliable refresh issuance timestamp. Existing
		// access remains usable until its recorded expiry; renew requires reauthorization.
		until, e := time.Parse(time.RFC3339, c.ExpiresAt)
		if e != nil {
			return token, "", 0, errSocialUnavailable
		}
		accessExpiry := until.Unix()
		if token.AccessExpiresAt > 0 && token.AccessExpiresAt < accessExpiry {
			accessExpiry = token.AccessExpiresAt
		}
		now := time.Now().Unix()
		if accessExpiry > now+90 {
			return token, "", 0, nil
		}
		allowed, _ := ctx.Value(socialRefreshAllowedKey{}).(bool)
		if r.auth != nil && !allowed {
			return token, "", 0, errSocialUnavailable
		}
		if e = BeginAuditMutation(ctx); e != nil {
			return token, "", 0, e
		}
		if token.RefreshExpiresAt <= now+90 || token.Refresh == "" {
			ss.Connections[i].Status = "expired"
			for j := range ss.Jobs {
				if ss.Jobs[j].ConnectionID == id && ss.Jobs[j].MediaType == "PHOTO" {
					ss.Jobs[j].PhotoAccessRevoked = true
				}
			}
			ss.Connections[i].Version++
			delete(d.Receipts, private)
			p.Social = &ss
			p.Version++
			d.Projects[k] = p
			return socialToken{}, "", 0, errors.Join(errSocialUnavailable, r.m.commit(d))
		}
		attempt := mediaID()
		token.RefreshAttempt = attempt
		sealed, e := r.seal(private, token)
		if e != nil {
			return token, "", 0, e
		}
		ss.Connections[i].Status = "refreshing"
		ss.Connections[i].RefreshLeaseUntil = time.Now().Add(30 * time.Second).UTC().Format(time.RFC3339)
		ss.Connections[i].Version++
		d.Receipts[private] = mediaReceipt{SocialSecret: sealed}
		p.Social = &ss
		p.Version++
		d.Projects[k] = p
		if e = r.m.commit(d); e != nil {
			return socialToken{}, "", 0, e
		}
		return token, attempt, ss.Connections[i].Version, nil
	}
	return socialToken{}, "", 0, ErrNotFound
}
func (r *socialRuntime) exchangeSocialRefresh(ctx context.Context, old socialToken, active func() bool) (socialToken, bool, error) {
	var next socialToken
	if e := r.checkActor(ctx); e != nil {
		return next, false, e
	}
	form := url.Values{"client_key": {r.clientKey}, "client_secret": {r.clientSecret}, "grant_type": {"refresh_token"}, "refresh_token": {old.Refresh}}
	req, e := http.NewRequestWithContext(ctx, "POST", r.base+"/v2/oauth/token/", strings.NewReader(form.Encode()))
	if e != nil {
		return next, false, errSocialUnavailable
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if !active() {
		return next, false, errSocialUnavailable
	}
	if e = r.checkActor(ctx); e != nil {
		return next, false, e
	}
	res, e := r.client.Do(req)
	if e != nil {
		return next, false, errSocialUnavailable
	}
	defer res.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(res.Body, 32<<10))
	if e != nil || len(raw) >= 32<<10 || json.Unmarshal(raw, &next) != nil {
		return socialToken{}, false, errSocialUnavailable
	}
	if next.Error == "invalid_grant" {
		return socialToken{}, true, errSocialUnavailable
	}
	if res.StatusCode != 200 || next.Error != "" {
		return socialToken{}, false, errSocialUnavailable
	}
	return next, false, nil
}
func (r *socialRuntime) finishSocialRefresh(project, space, id, attempt string, version int, status string, next socialToken) (socialToken, error) {
	r.m.mu.Lock()
	defer r.m.mu.Unlock()
	d := r.m.clone()
	k := mediaKey(space, project)
	p := mediaState(d, k)
	ss := socialState(p)
	private := socialPrivate(space, project, "token", id)
	for i, c := range ss.Connections {
		if c.ID != id {
			continue
		}
		if c.Status != "refreshing" || c.Version != version {
			return socialToken{}, errSocialUnavailable
		}
		var old socialToken
		if e := r.open(private, d.Receipts[private].SocialSecret, &old); e != nil || old.RefreshAttempt != attempt {
			return socialToken{}, errSocialUnavailable
		}
		ss.Connections[i].Status = status
		ss.Connections[i].RefreshLeaseUntil = ""
		ss.Connections[i].Version++
		if status == "connected" {
			sealed, e := r.seal(private, next)
			if e != nil {
				return socialToken{}, e
			}
			d.Receipts[private] = mediaReceipt{SocialSecret: sealed}
			ss.Connections[i].ExpiresAt = time.Unix(next.AccessExpiresAt, 0).UTC().Format(time.RFC3339)
		} else if status == "expired" {
			for j := range ss.Jobs {
				if ss.Jobs[j].ConnectionID == id && ss.Jobs[j].MediaType == "PHOTO" {
					ss.Jobs[j].PhotoAccessRevoked = true
				}
			}
			delete(d.Receipts, private)
		}
		p.Social = &ss
		p.Version++
		d.Projects[k] = p
		return next, r.m.commit(d)
	}
	return socialToken{}, ErrNotFound
}

func (r *socialRuntime) socialRefreshActive(project, space, id, attempt string, version int) bool {
	r.m.mu.Lock()
	defer r.m.mu.Unlock()
	d := r.m.clone()
	p := mediaState(d, mediaKey(space, project))
	for _, c := range socialState(p).Connections {
		if c.ID == id && c.Status == "refreshing" && c.Version == version {
			private := socialPrivate(space, project, "token", id)
			var token socialToken
			return r.open(private, d.Receipts[private].SocialSecret, &token) == nil && token.RefreshAttempt == attempt
		}
	}
	return false
}
