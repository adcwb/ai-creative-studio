package application

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const maxRotationBytes = 64 << 20

var ErrModelRotation = errors.New("model key rotation refused or failed; verify state before retrying")

type ModelRotationReport struct {
	Applied      bool   `json:"applied"`
	Tenants      int    `json:"changed_tenants"`
	Credentials  int    `json:"reencrypted_credentials"`
	BeforeSHA256 string `json:"before_sha256"`
	AfterSHA256  string `json:"after_sha256"`
}

func rotationAEAD(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, ErrModelRotation
	}
	block, e := aes.NewCipher(key)
	if e != nil {
		return nil, ErrModelRotation
	}
	return cipher.NewGCM(block)
}
func rotationDigest(d modelSnapshot) string {
	raw, _ := json.Marshal(d)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func rotateModelSnapshot(d modelSnapshot, oldKey, newKey, confirmation []byte) (modelSnapshot, ModelRotationReport, error) {
	report := ModelRotationReport{}
	if len(oldKey) != 32 || len(newKey) != 32 || !bytes.Equal(newKey, confirmation) || bytes.Equal(oldKey, newKey) {
		return modelSnapshot{}, report, ErrModelRotation
	}
	old, e := rotationAEAD(oldKey)
	if e != nil {
		return modelSnapshot{}, report, e
	}
	next, _ := rotationAEAD(newKey)
	if d.Version != 1 || d.Tenants == nil {
		return modelSnapshot{}, report, ErrModelRotation
	}
	if validateGatewaySnapshot(d) != nil {
		return modelSnapshot{}, report, ErrModelRotation
	}
	raw, e := json.Marshal(d)
	if e != nil || len(raw) > maxRotationBytes {
		return modelSnapshot{}, report, ErrModelRotation
	}
	var result modelSnapshot
	if json.Unmarshal(raw, &result) != nil {
		return result, report, ErrModelRotation
	}
	report.BeforeSHA256 = rotationDigest(d)
	for tenant, t := range result.Tenants {
		if tenant == "" || len(tenant) > 128 || strings.ContainsRune(tenant, 0) || t.Version < 1 || t.Entries == nil || len(t.Entries) > 100 {
			return modelSnapshot{}, report, ErrModelRotation
		}
		changed := false
		for id, g := range t.Gateways {
			if id != g.Gateway.ID || g.Gateway.Version < 1 {
				return modelSnapshot{}, report, ErrModelRotation
			}
			if g.Gateway.SecretStatus != "encrypted" {
				if g.Ciphertext != "" {
					return modelSnapshot{}, report, ErrModelRotation
				}
				continue
			}
			raw, e := base64.StdEncoding.DecodeString(g.Ciphertext)
			if e != nil || len(raw) < old.NonceSize()+old.Overhead() {
				return modelSnapshot{}, report, ErrModelRotation
			}
			plain, e := old.Open(nil, raw[:old.NonceSize()], raw[old.NonceSize():], gatewayAAD(tenant, g.Gateway))
			if e != nil {
				return modelSnapshot{}, report, ErrModelRotation
			}
			if !validCredential(string(plain)) {
				clear(plain)
				return modelSnapshot{}, report, ErrModelRotation
			}
			nonce := make([]byte, next.NonceSize())
			if _, e = rand.Read(nonce); e != nil {
				clear(plain)
				return modelSnapshot{}, report, ErrModelRotation
			}
			g.Ciphertext = base64.StdEncoding.EncodeToString(next.Seal(nonce, nonce, plain, gatewayAAD(tenant, g.Gateway)))
			clear(plain)
			if g.Gateway.Version == math.MaxInt64 {
				return modelSnapshot{}, report, ErrModelRotation
			}
			g.Gateway.Version++
			g.Gateway.UpdatedAt = time.Now().UTC()
			t.Gateways[id] = g
			report.Credentials++
			changed = true
		}
		for id, m := range t.Entries {
			if id == "" || len(id) > 128 || strings.ContainsRune(id, 0) || m.Entry.ID != id {
				return modelSnapshot{}, report, ErrModelRotation
			}
			a, err := validateModelInput(ModelConfigInput{ID: id, Provider: m.Entry.Provider, Model: m.Entry.Model, Capability: m.Entry.Capability, Region: m.Entry.Region, Enabled: m.Entry.Enabled, IsDefault: m.Entry.IsDefault, Lifecycle: m.Entry.Lifecycle})
			if err != nil || a.Endpoint != m.Entry.Endpoint || a.Protocol != m.Entry.Protocol {
				return modelSnapshot{}, report, ErrModelRotation
			}
			switch m.Entry.SecretStatus {
			case "encrypted":
				if m.Entry.SecretReference != "" {
					return modelSnapshot{}, report, ErrModelRotation
				}
				ciphertext, err := base64.StdEncoding.DecodeString(m.Ciphertext)
				if err != nil || len(ciphertext) < old.NonceSize()+old.Overhead() {
					return modelSnapshot{}, report, ErrModelRotation
				}
				aad := []byte(tenant + "\x00" + id + "\x00" + m.Entry.Provider)
				plain, err := old.Open(nil, ciphertext[:old.NonceSize()], ciphertext[old.NonceSize():], aad)
				if err != nil {
					return modelSnapshot{}, report, ErrModelRotation
				}
				if len(plain) == 0 || len(plain) > 4096 {
					clear(plain)
					return modelSnapshot{}, report, ErrModelRotation
				}
				nonce := make([]byte, next.NonceSize())
				if _, err = rand.Read(nonce); err != nil {
					clear(plain)
					return modelSnapshot{}, report, ErrModelRotation
				}
				sealed := next.Seal(nonce, nonce, plain, aad)
				clear(plain)
				m.Ciphertext = base64.StdEncoding.EncodeToString(sealed)
				m.Entry.UpdatedAt = time.Now().UTC()
				t.Entries[id] = m
				report.Credentials++
				changed = true
			case "revoked", "not_configured":
				if m.Ciphertext != "" || m.Entry.SecretReference != "" || (m.Entry.SecretStatus == "revoked" && (m.Entry.Enabled || m.Entry.IsDefault)) {
					return modelSnapshot{}, report, ErrModelRotation
				}
			case "reference_unverified":
				valid := false
				for _, ref := range a.CredentialRefs {
					valid = valid || ref == m.Entry.SecretReference
				}
				if !valid || m.Ciphertext != "" {
					return modelSnapshot{}, report, ErrModelRotation
				}
			default:
				return modelSnapshot{}, report, ErrModelRotation
			}
		}
		if changed {
			if t.Version == math.MaxInt64 {
				return modelSnapshot{}, report, ErrModelRotation
			}
			t.Version++
			report.Tenants++
			result.Tenants[tenant] = t
		}
	}
	report.AfterSHA256 = rotationDigest(result)
	return result, report, nil
}

// RotateFileModelKey requires all processes stopped; the exclusive application
// lock protects against ordinary writers. No backup is overwritten or revived.
func RotateFileModelKey(dir string, oldKey, newKey, confirmation []byte, stopped, apply bool) (ModelRotationReport, error) {
	report := ModelRotationReport{}
	if !stopped {
		return report, ErrModelRotation
	}
	info, e := os.Lstat(dir)
	if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0022 != 0 {
		return report, ErrModelRotation
	}
	for _, name := range []string{"state.json", "model-config.json"} {
		info, e = os.Lstat(filepath.Join(dir, name))
		if e != nil || !info.Mode().IsRegular() || info.Size() > maxRotationBytes {
			return report, ErrModelRotation
		}
	}
	lock, e := os.OpenFile(filepath.Join(dir, "store.lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return report, ErrModelRotation
	}
	defer lock.Close()
	if syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		return report, ErrModelRotation
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	lockedInfo, e := lock.Stat()
	if e != nil || !lockedInfo.Mode().IsRegular() {
		return report, ErrModelRotation
	}
	f, e := os.OpenFile(filepath.Join(dir, "model-config.json"), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if e != nil {
		return report, ErrModelRotation
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 || st.Size() > maxRotationBytes {
		return report, ErrModelRotation
	}
	raw, e := io.ReadAll(io.LimitReader(f, maxRotationBytes+1))
	if e != nil || len(raw) > maxRotationBytes {
		return report, ErrModelRotation
	}
	var snapshot modelSnapshot
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&snapshot) != nil {
		return report, ErrModelRotation
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return report, ErrModelRotation
	}
	store := &ModelConfigStore{path: filepath.Join(dir, "model-config.json"), data: snapshot}
	rotated, report, e := rotateModelSnapshot(snapshot, oldKey, newKey, confirmation)
	if e != nil {
		return report, e
	}
	if !apply || report.Credentials == 0 {
		return report, nil
	}
	if e = store.commit(rotated); e != nil {
		return report, ErrModelRotation
	}
	report.Applied = true
	return report, nil
}

// RotatePostgresModelKey updates all credentials, versions and the sanitized
// operator audit in one transaction. The barrier does not stop queue workers;
// operators must stop every instance before invoking this operation.
func RotatePostgresModelKey(ctx context.Context, db *sql.DB, oldKey, newKey, confirmation []byte, stopped, apply bool) (ModelRotationReport, error) {
	report := ModelRotationReport{}
	if !stopped || CheckPostgres(ctx, db) != nil {
		return report, ErrModelRotation
	}
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		return report, ErrModelRotation
	}
	defer tx.Rollback()
	var acquired bool
	if e = tx.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock(728219117)`).Scan(&acquired); e != nil || !acquired {
		return report, ErrModelRotation
	}
	if _, e = tx.ExecContext(ctx, `SET LOCAL synchronous_commit=on`); e != nil {
		return report, ErrModelRotation
	}
	var size int64
	if e = tx.QueryRowContext(ctx, `SELECT coalesce(sum(octet_length(payload::text)),0) FROM studio_model_tenants`).Scan(&size); e != nil || size > maxRotationBytes {
		return report, ErrModelRotation
	}
	template := modelSnapshot{Version: 1, Tenants: map[string]modelTenant{}}
	raw, before, e := readPostgresScopeTx(ctx, tx, "models", template)
	if e != nil {
		return report, ErrModelRotation
	}
	var snapshot modelSnapshot
	if json.Unmarshal(raw, &snapshot) != nil {
		return report, ErrModelRotation
	}
	rotated, report, e := rotateModelSnapshot(snapshot, oldKey, newKey, confirmation)
	if e != nil {
		return report, e
	}
	if !apply || report.Credentials == 0 {
		return report, nil
	}
	scope := &postgresScope{name: "models", ctx: ctx, tx: tx, before: before}
	if e = scope.write(rotated); e != nil {
		return report, ErrModelRotation
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO studio_audit_events(request_id,phase,at,actor_id,tenant_id,role,method,status,duration_ms) VALUES($1,'finished',clock_timestamp(),current_user,'','database-operator','MODEL_KEY_ROTATE',200,0)`, authRandom(16)); e != nil {
		return report, ErrModelRotation
	}
	if e = tx.Commit(); e != nil {
		return report, ErrModelRotation
	}
	report.Applied = true
	return report, nil
}
