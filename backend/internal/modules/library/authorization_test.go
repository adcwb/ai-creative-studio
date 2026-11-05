package library

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestUseAuthorizationVersionPurposeRevocationAndExpiry(t *testing.T) {
	now := time.Now().UTC()
	expiry := now.Add(time.Hour)
	a := Asset{Kind: "style", Status: "active"}
	in := AuthorizationInput{Status: "granted", VersionIDs: []string{"v2", "v1"}, Purposes: []string{"video_reference", "image_reference"}, Holder: "Synthetic creator", Basis: "own_work", Evidence: "Synthetic original drawings", Attested: true, ExpiresAt: &expiry}
	grant, e := ApplyAuthorization(a, in, "creator", now)
	if e != nil || !grant.Allows("v1", "image_reference", now) || grant.Allows("v3", "image_reference", now) || grant.Allows("v1", "voice_reference", now) || grant.Allows("v1", "image_reference", expiry) {
		t.Fatal(grant, e)
	}
	if in.VersionIDs[0] != "v2" {
		t.Fatal("mutated caller scope")
	}
	a.UseAuthorization = &grant
	if _, e = ApplyAuthorization(a, in, "creator", now); !errors.Is(e, ErrConflict) {
		t.Fatal("CAS", e)
	}
	revoked, e := ApplyAuthorization(a, AuthorizationInput{Expected: 1, Status: "revoked", Reason: "withdrawn"}, "editor", now.Add(time.Minute))
	if e != nil || revoked.Allows("v1", "image_reference", now) || !ValidAuthorization(&revoked) || revoked.Evidence != grant.Evidence || revoked.DeclaredBy != grant.DeclaredBy || revoked.UpdatedBy != "editor" || !reflect.DeepEqual(revoked.VersionIDs, grant.VersionIDs) {
		t.Fatal(revoked, e)
	}
	if grant.Status != "granted" {
		t.Fatal("rewrote historical authorization")
	}
	// Expired declarations remain readable history, while new grants cannot be expired.
	if !ValidAuthorization(&grant) {
		t.Fatal("historical grant invalid")
	}
	in.ExpiresAt = &now
	if _, e = ApplyAuthorization(Asset{Kind: "style", Status: "active"}, in, "creator", now); !errors.Is(e, ErrInput) {
		t.Fatal("expired grant", e)
	}
	for _, change := range []func(*AuthorizationInput){func(i *AuthorizationInput) { i.Attested = false }, func(i *AuthorizationInput) { i.VersionIDs = []string{"v1", "v1"} }, func(i *AuthorizationInput) { i.Purposes = []string{"image_reference", "image_reference"} }, func(i *AuthorizationInput) { i.Holder = "" }, func(i *AuthorizationInput) { i.Basis = "unknown" }} {
		bad := in
		bad.ExpiresAt = &expiry
		change(&bad)
		if _, e := NormalizeAuthorization(bad); e == nil {
			t.Fatal("bad declaration accepted")
		}
	}
	voice := in
	voice.ExpiresAt = &expiry
	if _, e = ApplyAuthorization(Asset{Kind: "voice", Status: "active"}, voice, "creator", now); e == nil {
		t.Fatal("voice image authorization")
	}
}
