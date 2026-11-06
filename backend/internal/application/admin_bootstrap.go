package application

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// BootstrapLocalAccount creates a new local identity only through an offline
// deployment command. It never selects/overwrites an account by email or grants
// privileges; the operator must pin the returned ID in deployment configuration.
func BootstrapLocalAccount(dir string, db *sql.DB, email, password string) (AuthUser, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return AuthUser{}, err
	}
	lock, err := os.OpenFile(filepath.Join(dir, "store.lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return AuthUser{}, err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return AuthUser{}, errors.New("stop every API/worker before bootstrapping")
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	var auth *AuthStore
	if db == nil {
		auth, err = OpenAuthStore(dir)
	} else {
		auth, err = OpenPostgresAuthStore(dir, db)
	}
	if err != nil {
		return AuthUser{}, err
	}
	user, token, _, err := auth.login(email, password, true)
	if err != nil {
		return AuthUser{}, err
	}
	auth.mu.Lock()
	d := auth.clone()
	delete(d.Sessions, authDigest(token))
	err = auth.commit(d)
	auth.mu.Unlock()
	if err != nil {
		return AuthUser{}, err
	}
	return user, nil
}
