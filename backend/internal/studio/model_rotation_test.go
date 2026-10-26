package studio

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func rotationKeys() ([]byte, []byte) {
	return bytes.Repeat([]byte{61}, 32), bytes.Repeat([]byte{72}, 32)
}
func seedRotationModels(t *testing.T, s *ModelConfigStore) map[string]string {
	t.Helper()
	ids := map[string]string{}
	for _, tenant := range []string{"tenant-a", "tenant-b", "tenant-revoked"} {
		report, e := s.update(tenant, 0, syntheticModel(), ModelCredentialInput{Action: "set", Secret: "synthetic-rotation-secret-" + tenant})
		if e != nil {
			t.Fatal(e)
		}
		ids[tenant] = report.Entries[0].ID
		if tenant == "tenant-revoked" {
			in := syntheticModel()
			in.ID = ids[tenant]
			if _, e = s.update(tenant, report.Version, in, ModelCredentialInput{Action: "revoke"}); e != nil {
				t.Fatal(e)
			}
		}
	}
	report, e := s.update("tenant-reference", 0, syntheticModel(), ModelCredentialInput{Action: "reference", Reference: "OPENAI_API_KEY"})
	if e != nil {
		t.Fatal(e)
	}
	ids["tenant-reference"] = report.Entries[0].ID
	return ids
}
func rotationFileFixture(t *testing.T) (string, map[string]string) {
	t.Helper()
	dir := t.TempDir()
	old, _ := rotationKeys()
	raw, e := json.Marshal(emptySnapshot())
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(dir, "state.json"), raw, 0600); e != nil {
		t.Fatal(e)
	}
	s, e := OpenModelConfigStore(dir, old)
	if e != nil {
		t.Fatal(e)
	}
	return dir, seedRotationModels(t, s)
}
func assertRotatedModels(t *testing.T, s *ModelConfigStore, ids map[string]string, version int64) {
	t.Helper()
	for _, tenant := range []string{"tenant-a", "tenant-b"} {
		result, e := s.validate(tenant, ids[tenant])
		if e != nil || result["credential_status"] != "encrypted" {
			t.Fatal("new key cannot decrypt", e)
		}
		report, e := s.report(tenant)
		if e != nil || report.Version != version {
			t.Fatal("changed tenant version was not advanced", e)
		}
	}
	report, e := s.report("tenant-revoked")
	if e != nil || report.Version != 2 || report.Entries[0].SecretStatus != "revoked" || report.Entries[0].Enabled || report.Entries[0].IsDefault {
		t.Fatal("revoked model resurrected", e)
	}
	for _, stored := range s.data.Tenants["tenant-revoked"].Entries {
		if stored.Ciphertext != "" || stored.Entry.SecretReference != "" {
			t.Fatal("revoked secret material resurrected")
		}
	}
	report, e = s.report("tenant-reference")
	if e != nil || report.Version != 1 || report.Entries[0].SecretStatus != "reference_unverified" {
		t.Fatal("external reference mutated", e)
	}
}
func TestModelRotationFilePreviewApplyAndReverse(t *testing.T) {
	dir, ids := rotationFileFixture(t)
	old, next := rotationKeys()
	path := filepath.Join(dir, "model-config.json")
	before, _ := os.ReadFile(path)
	preview, e := RotateFileModelKey(dir, old, next, next, true, false)
	if e != nil || preview.Applied || preview.Credentials != 2 || preview.Tenants != 2 {
		t.Fatal("invalid preview", preview, e)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("preview wrote credentials")
	}
	report, e := RotateFileModelKey(dir, old, next, next, true, true)
	if e != nil || !report.Applied || report.Credentials != 2 || report.BeforeSHA256 == report.AfterSHA256 {
		t.Fatal("rotation failed", report, e)
	}
	s, e := OpenModelConfigStore(dir, next)
	if e != nil {
		t.Fatal(e)
	}
	assertRotatedModels(t, s, ids, 2)
	stale, e := OpenModelConfigStore(dir, old)
	if e != nil {
		t.Fatal(e)
	}
	status, e := stale.validate("tenant-a", ids["tenant-a"])
	if e != nil || status["credential_status"] != "credential_unreadable" {
		t.Fatal("old key still usable", e)
	}
	after, _ = os.ReadFile(path)
	if bytes.Contains(after, []byte("synthetic-rotation-secret")) {
		t.Fatal("plaintext leaked")
	}
	if _, e = RotateFileModelKey(dir, next, old, old, true, true); e != nil {
		t.Fatal(e)
	}
	s, e = OpenModelConfigStore(dir, old)
	if e != nil {
		t.Fatal(e)
	}
	assertRotatedModels(t, s, ids, 3)
}
func TestModelRotationRejectsKeysAndCorruptionWithoutMutation(t *testing.T) {
	dir, _ := rotationFileFixture(t)
	old, next := rotationKeys()
	path := filepath.Join(dir, "model-config.json")
	before, _ := os.ReadFile(path)
	cases := []struct {
		old, next, confirmation []byte
		stopped                 bool
	}{{old, next, next, false}, {old, next, old, true}, {old, old, old, true}, {next, old, old, true}, {old[:12], next, next, true}}
	for _, tc := range cases {
		if _, e := RotateFileModelKey(dir, tc.old, tc.next, tc.confirmation, tc.stopped, true); e == nil {
			t.Fatal("unsafe rotation accepted")
		}
		after, _ := os.ReadFile(path)
		if !bytes.Equal(before, after) {
			t.Fatal("rejected rotation mutated file")
		}
	}
	var d modelSnapshot
	if e := json.Unmarshal(before, &d); e != nil {
		t.Fatal(e)
	}
	var aID, bID string
	for id := range d.Tenants["tenant-a"].Entries {
		aID = id
	}
	for id := range d.Tenants["tenant-b"].Entries {
		bID = id
	}
	b := d.Tenants["tenant-b"].Entries[bID]
	b.Ciphertext = d.Tenants["tenant-a"].Entries[aID].Ciphertext
	d.Tenants["tenant-b"].Entries[bID] = b
	corrupt, _ := json.Marshal(d)
	if e := os.WriteFile(path, corrupt, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := RotateFileModelKey(dir, old, next, next, true, true); e == nil {
		t.Fatal("cross-tenant ciphertext accepted")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(corrupt, after) {
		t.Fatal("partial rotation persisted before validation finished")
	}
}
func TestModelRotationRejectsRevokedCiphertextAndVersionOverflow(t *testing.T) {
	dir, _ := rotationFileFixture(t)
	old, next := rotationKeys()
	s, e := OpenModelConfigStore(dir, old)
	if e != nil {
		t.Fatal(e)
	}
	for id, m := range s.data.Tenants["tenant-revoked"].Entries {
		m.Ciphertext = "synthetic-corruption"
		s.data.Tenants["tenant-revoked"].Entries[id] = m
	}
	if _, _, e = rotateModelSnapshot(s.data, old, next, next); e == nil {
		t.Fatal("revoked ciphertext accepted")
	}
	s, e = OpenModelConfigStore(dir, old)
	if e != nil {
		t.Fatal(e)
	}
	tenant := s.data.Tenants["tenant-a"]
	tenant.Version = 1<<63 - 1
	s.data.Tenants["tenant-a"] = tenant
	if _, _, e = rotateModelSnapshot(s.data, old, next, next); e == nil {
		t.Fatal("version overflow accepted")
	}
}
func TestModelRotationFileRequiresExclusiveRegularPrivateState(t *testing.T) {
	old, next := rotationKeys()
	for _, kind := range []string{"locked", "model-symlink", "lock-symlink", "missing-core", "public-config", "unknown-field"} {
		t.Run(kind, func(t *testing.T) {
			dir, _ := rotationFileFixture(t)
			path := filepath.Join(dir, "model-config.json")
			switch kind {
			case "locked":
				f, e := os.OpenFile(filepath.Join(dir, "store.lock"), os.O_CREATE|os.O_RDWR, 0600)
				if e != nil {
					t.Fatal(e)
				}
				defer f.Close()
				if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
					t.Fatal(e)
				}
				defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
			case "model-symlink":
				if e := os.Rename(path, path+".source"); e != nil {
					t.Fatal(e)
				}
				if e := os.Symlink(path+".source", path); e != nil {
					t.Fatal(e)
				}
			case "lock-symlink":
				if e := os.Symlink(path, filepath.Join(dir, "store.lock")); e != nil {
					t.Fatal(e)
				}
			case "missing-core":
				os.Remove(filepath.Join(dir, "state.json"))
			case "public-config":
				os.Chmod(path, 0644)
			case "unknown-field":
				raw, _ := os.ReadFile(path)
				raw = append([]byte(`{"unexpected":true,`), raw[1:]...)
				os.WriteFile(path, raw, 0600)
			}
			if _, e := RotateFileModelKey(dir, old, next, next, true, true); e == nil {
				t.Fatal("unsafe file state accepted")
			}
		})
	}
}
func TestPostgresModelRotationAtomicAuditAndBarrier(t *testing.T) {
	db := postgresTestDB(t)
	ctx := context.Background()
	old, next := rotationKeys()
	s, e := OpenPostgresModelConfigStore(t.TempDir(), db, old)
	if e != nil {
		t.Fatal(e)
	}
	ids := seedRotationModels(t, s)
	preview, e := RotatePostgresModelKey(ctx, db, old, next, next, true, false)
	if e != nil || preview.Applied || preview.Credentials != 2 {
		t.Fatal(preview, e)
	}
	var audits int
	if e = db.QueryRow(`SELECT count(*) FROM studio_audit_events WHERE method='MODEL_KEY_ROTATE'`).Scan(&audits); e != nil || audits != 0 {
		t.Fatal("preview audit mutation", e)
	}
	if _, e = db.Exec(`CREATE FUNCTION reject_model_rotation_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic failure'; END $$; CREATE TRIGGER reject_model_rotation_audit BEFORE INSERT ON studio_audit_events FOR EACH ROW EXECUTE FUNCTION reject_model_rotation_audit()`); e != nil {
		t.Fatal(e)
	}
	if _, e = RotatePostgresModelKey(ctx, db, old, next, next, true, true); e == nil {
		t.Fatal("audit failure did not abort transaction")
	}
	original, e := OpenPostgresModelConfigStore(t.TempDir(), db, old)
	if e != nil {
		t.Fatal(e)
	}
	assertRotatedModels(t, original, ids, 1)
	if _, e = db.Exec(`DROP TRIGGER reject_model_rotation_audit ON studio_audit_events; DROP FUNCTION reject_model_rotation_audit()`); e != nil {
		t.Fatal(e)
	}
	guard, e := db.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = guard.Exec(`SELECT pg_advisory_xact_lock_shared(728219117)`); e != nil {
		t.Fatal(e)
	}
	if _, e = RotatePostgresModelKey(ctx, db, old, next, next, true, true); e == nil {
		t.Fatal("active writer barrier ignored")
	}
	guard.Rollback()
	report, e := RotatePostgresModelKey(ctx, db, old, next, next, true, true)
	if e != nil || !report.Applied {
		t.Fatal(report, e)
	}
	rotated, e := OpenPostgresModelConfigStore(t.TempDir(), db, next)
	if e != nil {
		t.Fatal(e)
	}
	assertRotatedModels(t, rotated, ids, 2)
	if e = db.QueryRow(`SELECT count(*) FROM studio_audit_events WHERE method='MODEL_KEY_ROTATE'`).Scan(&audits); e != nil || audits != 1 {
		t.Fatal("rotation audit missing", e)
	}
	if _, e = RotatePostgresModelKey(ctx, db, next, old, old, true, true); e != nil {
		t.Fatal(e)
	}
	original, e = OpenPostgresModelConfigStore(t.TempDir(), db, old)
	if e != nil {
		t.Fatal(e)
	}
	assertRotatedModels(t, original, ids, 3)
}
