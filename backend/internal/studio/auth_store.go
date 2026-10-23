package studio

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// AuthStore is intentionally local and protected by the enclosing FileStore lock.
// Its file must be backed up with the entire DATA_DIR. It is not a multi-instance store.
type AuthStore struct {
	mu       sync.Mutex
	path     string
	data     authSnapshot
	failed   error
	attempts map[string]authAttempt
}
type authAttempt struct {
	Count int
	Until time.Time
}
type AuthUser struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}
type authAccount struct {
	AuthUser
	Salt         string `json:"salt"`
	PasswordHash string `json:"password_hash"`
}
type AuthOrganization struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	OwnerID string            `json:"owner_id"`
	Members map[string]string `json:"members"`
}
type authSession struct {
	UserID  string    `json:"user_id"`
	CSRF    string    `json:"csrf"`
	Expires time.Time `json:"expires"`
}
type authSnapshot struct {
	Version       int                         `json:"version"`
	Users         map[string]authAccount      `json:"users"`
	Organizations map[string]AuthOrganization `json:"organizations"`
	Sessions      map[string]authSession      `json:"sessions"`
}

func OpenAuthStore(dir string) (*AuthStore, error) {
	s := &AuthStore{path: filepath.Join(dir, "auth.json"), attempts: map[string]authAttempt{}, data: authSnapshot{Version: 1, Users: map[string]authAccount{}, Organizations: map[string]AuthOrganization{}, Sessions: map[string]authSession{}}}
	b, err := os.ReadFile(s.path)
	if err == nil {
		var restored authSnapshot
		err = json.Unmarshal(b, &restored)
		s.data = restored
	} else if os.IsNotExist(err) {
		err = nil
	}
	if err != nil {
		return nil, fmt.Errorf("read authentication state (refusing to reset): %w", err)
	}
	if s.data.Version != 1 || s.data.Users == nil || s.data.Organizations == nil || s.data.Sessions == nil {
		return nil, errors.New("invalid authentication state")
	}
	for id, u := range s.data.Users {
		salt, e := hex.DecodeString(u.Salt)
		hash, h := hex.DecodeString(u.PasswordHash)
		if id != u.ID || u.Email == "" || e != nil || h != nil || len(salt) != 16 || len(hash) != 32 {
			return nil, errors.New("invalid authentication account")
		}
	}
	for id, o := range s.data.Organizations {
		if id != o.ID || o.Members == nil || o.Members[o.OwnerID] != "owner" {
			return nil, errors.New("invalid organization owner")
		}
		for uid, role := range o.Members {
			if _, ok := s.data.Users[uid]; !ok || !validRole(role) {
				return nil, errors.New("invalid organization membership")
			}
		}
	}
	for _, ss := range s.data.Sessions {
		if _, ok := s.data.Users[ss.UserID]; !ok || len(ss.CSRF) != 64 || ss.Expires.IsZero() {
			return nil, errors.New("invalid authentication session")
		}
	}
	return s, nil
}
func validRole(role string) bool {
	return role == "owner" || role == "admin" || role == "editor" || role == "viewer"
}
func authRandom(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func authDigest(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}
func authPassword(password, salt string) string {
	b, _ := hex.DecodeString(salt)
	h, err := pbkdf2.Key(sha256.New, password, b, 600000, 32)
	if err != nil {
		panic(err)
	}
	return hex.EncodeToString(h)
}
func (s *AuthStore) clone() authSnapshot {
	b, _ := json.Marshal(s.data)
	var d authSnapshot
	_ = json.Unmarshal(b, &d)
	return d
}
func (s *AuthStore) commit(d authSnapshot) error {
	if s.failed != nil {
		return s.failed
	}
	b, err := json.Marshal(d)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".auth-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(name, s.path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(s.path))
	if err == nil {
		err = dir.Sync()
		_ = dir.Close()
	}
	if err != nil {
		s.failed = err
		return err
	}
	s.data = d
	return nil
}
func (s *AuthStore) authenticate(token string) (AuthUser, authSession, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed != nil || len(token) != 64 {
		return AuthUser{}, authSession{}, false
	}
	ss, ok := s.data.Sessions[authDigest(token)]
	u, exists := s.data.Users[ss.UserID]
	return u.AuthUser, ss, ok && exists && time.Now().Before(ss.Expires)
}
func (s *AuthStore) allowAttempt(ip string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for k, v := range s.attempts {
		if now.After(v.Until) {
			delete(s.attempts, k)
		}
	}
	if len(s.attempts) >= 1024 {
		return false
	}
	a := s.attempts[ip]
	if a.Until.IsZero() {
		a.Until = now.Add(15 * time.Minute)
	}
	a.Count++
	s.attempts[ip] = a
	return a.Count <= 20
}
func (s *AuthStore) login(email, password string, register bool) (AuthUser, string, authSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	email = strings.ToLower(strings.TrimSpace(email))
	addr, e := mail.ParseAddress(email)
	if e != nil || addr.Address != email || len(email) > 254 || len(password) < 12 || len(password) > 128 {
		return AuthUser{}, "", authSession{}, errors.New("invalid credentials")
	}
	var account authAccount
	found := false
	for _, u := range s.data.Users {
		if u.Email == email {
			account = u
			found = true
			break
		}
	}
	d := s.clone()
	if register {
		if found || len(d.Users) >= 1000 {
			return AuthUser{}, "", authSession{}, errors.New("registration unavailable")
		}
		account = authAccount{AuthUser: AuthUser{ID: authRandom(16), Email: email}, Salt: authRandom(16)}
		account.PasswordHash = authPassword(password, account.Salt)
		d.Users[account.ID] = account
		orgID := "org-" + authRandom(16)
		d.Organizations[orgID] = AuthOrganization{ID: orgID, Name: "我的工作空间", OwnerID: account.ID, Members: map[string]string{account.ID: "owner"}}
	} else {
		salt := account.Salt
		if !found {
			salt = strings.Repeat("0", 32)
		}
		actual := authPassword(password, salt)
		if !found || subtle.ConstantTimeCompare([]byte(actual), []byte(account.PasswordHash)) != 1 {
			return AuthUser{}, "", authSession{}, errors.New("invalid credentials")
		}
	}
	now := time.Now()
	count := 0
	for k, ss := range d.Sessions {
		if now.After(ss.Expires) {
			delete(d.Sessions, k)
		} else if ss.UserID == account.ID {
			count++
		}
	}
	if count >= 20 || len(d.Sessions) >= 10000 {
		return AuthUser{}, "", authSession{}, errors.New("session limit reached")
	}
	token := authRandom(32)
	ss := authSession{UserID: account.ID, CSRF: authRandom(32), Expires: now.Add(12 * time.Hour)}
	d.Sessions[authDigest(token)] = ss
	if err := s.commit(d); err != nil {
		return AuthUser{}, "", authSession{}, err
	}
	return account.AuthUser, token, ss, nil
}
func (s *AuthStore) role(user, org string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed != nil {
		return ""
	}
	return s.data.Organizations[org].Members[user]
}
func (s *AuthStore) spaces(user string) []map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []map[string]string{}
	for _, o := range s.data.Organizations {
		if role := o.Members[user]; role != "" {
			out = append(out, map[string]string{"id": o.ID, "name": o.Name, "type": "team", "owner_id": o.OwnerID, "role": role})
		}
	}
	return out
}
