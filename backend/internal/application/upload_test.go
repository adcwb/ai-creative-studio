package application

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/modules/uploads"
	"github.com/adcwb/ai-creative-studio/backend/internal/persistence/uploadrepo"
)

func wavHeader(n int64, rate int) []byte {
	b := make([]byte, 44)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(n+36))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 2)
	binary.LittleEndian.PutUint32(b[24:], uint32(rate))
	binary.LittleEndian.PutUint32(b[28:], uint32(rate*8))
	binary.LittleEndian.PutUint16(b[32:], 8)
	binary.LittleEndian.PutUint16(b[34:], 32)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], uint32(n))
	return b
}
func uploadCall(h http.Handler, method, path, key string, data []byte, sha string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, bytes.NewReader(data))
	r.Header.Set("Content-Type", "application/json")
	if method == "PUT" {
		r.Header.Set("Content-Type", "application/octet-stream")
		r.Header.Set("X-Chunk-SHA256", sha)
	}
	if key == "" {
		key = "synthetic-upload-key"
	}
	r.Header.Set("Idempotency-Key", key)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func uploadSessionFrom(t *testing.T, w *httptest.ResponseRecorder, status int) uploads.Session {
	t.Helper()
	if w.Code != status {
		t.Fatalf("upload %d expected %d: %s", w.Code, status, w.Body.String())
	}
	var u uploads.Session
	if e := json.Unmarshal(w.Body.Bytes(), &u); e != nil || !uploads.ValidSession(u) {
		t.Fatal("invalid upload response", u, e)
	}
	return u
}
func newUploadForTest(t *testing.T, s *FileStore, project, space, actor string, data []byte) uploads.Session {
	t.Helper()
	now := time.Now().UTC()
	in := uploads.Input{Name: "synthetic.wav", MIME: "audio/wav", Size: int64(len(data)), SHA256: uploads.BytesSHA(data), Role: "shot"}
	u := uploads.Session{Input: in, ID: mediaID(), Space: space, Project: project, Actor: actor, State: "receiving", ChunkSize: uploads.ChunkBytes, RequestSHA: uploads.Digest(in), CreatedAt: now, ExpiresAt: now.Add(7 * 24 * time.Hour)}
	out, e := s.uploads.Create(context.Background(), u)
	if e != nil {
		t.Fatal(e)
	}
	return out
}

func TestResumableUploadRestartReplayAndAtomicMediaCompletion(t *testing.T) {
	for _, mode := range []string{"file", "postgres"} {
		t.Run(mode, func(t *testing.T) {
			var s, next *FileStore
			dir := t.TempDir()
			if mode == "file" {
				var e error
				s, e = OpenStore(dir)
				if e != nil {
					t.Fatal(e)
				}
			} else {
				db := postgresTestDB(t)
				s = postgresTestStore(t, db)
				next = postgresTestStore(t, db)
				blobDir := t.TempDir()
				for _, store := range []*FileStore{s, next} {
					objects, e := NewFileBlobStore(blobDir)
					if e != nil {
						t.Fatal(e)
					}
					if e = configureBlobStorage(store, objects); e != nil {
						t.Fatal(e)
					}
				}
			}
			defer func() {
				s.Close()
				if next != nil {
					next.Close()
				}
			}()
			project, _, e := s.CreateProject("personal-demo", "upload-project", projectInput())
			if e != nil {
				t.Fatal(e)
			}
			payload := make([]byte, 30*48000*8+44)
			copy(payload, wavHeader(int64(len(payload)-44), 48000))
			in := uploads.Input{Name: "synthetic.wav", MIME: "audio/wav", Size: int64(len(payload)), SHA256: uploads.BytesSHA(payload), Role: "shot"}
			body, _ := json.Marshal(in)
			base := "/api/v1/projects/" + project.ID + "/media/upload-sessions"
			q := "?space_id=personal-demo"
			h := Handler(s, "")
			u := uploadSessionFrom(t, uploadCall(h, "POST", base+q, "upload-create-key", body, ""), 201)
			if again := uploadSessionFrom(t, uploadCall(h, "POST", base+q, "upload-create-key", body, ""), 201); again.ID != u.ID {
				t.Fatal("creation duplicated")
			}
			in.Name = "changed.wav"
			changed, _ := json.Marshal(in)
			if w := uploadCall(h, "POST", base+q, "upload-create-key", changed, ""); w.Code != 409 {
				t.Fatal(w.Code, w.Body.String())
			}
			prefix := base + "/" + u.ID
			chunk := payload[:uploads.ChunkBytes]
			url := prefix + "/parts/0" + q
			if w := uploadCall(h, "PUT", prefix+"/parts/8388608"+q, "future-part-key", payload[uploads.ChunkBytes:], uploads.BytesSHA(payload[uploads.ChunkBytes:])); w.Code != 409 {
				t.Fatal("accepted sparse upload", w.Code)
			}
			if w := uploadCall(h, "PUT", url, "oversized-part-key", payload, uploads.BytesSHA(payload)); w.Code != 400 {
				t.Fatal("accepted oversized transport chunk", w.Code)
			}
			if w := uploadCall(h, "PUT", url, "part-key", chunk, "bad"); w.Code != 400 {
				t.Fatal(w.Code)
			}
			u = uploadSessionFrom(t, uploadCall(h, "PUT", url, "part-key", chunk, uploads.BytesSHA(chunk)), 200)
			if u.Offset != uploads.ChunkBytes {
				t.Fatal(u.Offset)
			}
			if w := uploadCall(h, "POST", prefix+"/complete"+q, "complete-key", []byte("{}"), ""); w.Code != 409 {
				t.Fatal("accepted partial media", w.Code)
			}
			if e = s.Close(); e != nil {
				t.Fatal(e)
			}
			if mode == "file" {
				s, e = OpenStore(dir)
				if e != nil {
					t.Fatal(e)
				}
			} else {
				s = next
				next = nil
			}
			h = Handler(s, "")
			u = uploadSessionFrom(t, uploadCall(h, "GET", prefix+q, "", nil, ""), 200)
			if u.Offset != uploads.ChunkBytes {
				t.Fatal("lost confirmed progress")
			}
			u = uploadSessionFrom(t, uploadCall(h, "PUT", url, "part-key", chunk, uploads.BytesSHA(chunk)), 200)
			bad := bytes.Clone(chunk)
			bad[0] ^= 1
			if w := uploadCall(h, "PUT", url, "part-key", bad, uploads.BytesSHA(bad)); w.Code != 409 {
				t.Fatal("changed chunk overwritten", w.Code)
			}
			tail := payload[uploads.ChunkBytes:]
			if _, e := u.CheckPart(u.Offset, tail, uploads.BytesSHA(tail)); e != nil {
				t.Fatalf("tail contract: offset=%d size=%d bytes=%d state=%s: %v", u.Offset, u.Size, len(tail), u.State, e)
			}
			u = uploadSessionFrom(t, uploadCall(h, "PUT", fmt.Sprintf("%s/parts/%d%s", prefix, u.Offset, q), "tail-key", tail, uploads.BytesSHA(tail)), 200)
			// Model the exact boundary where media accepted but session completion
			// acknowledgement was lost. Recovery must adopt that existing asset.
			file := filepath.Join(s.media.dir, "upload-before-session-ack")
			if e = os.WriteFile(file, payload, 0600); e != nil {
				t.Fatal(e)
			}
			if _, e = s.media.addAssetFile(context.Background(), project.ID, "personal-demo", "resumable-"+u.ID, u.Name, u.Role, u.Character, u.ShotID, "uploaded", file, u.MIME, nil); e != nil {
				t.Fatal(e)
			}
			u = uploadSessionFrom(t, uploadCall(h, "POST", prefix+"/complete"+q, "complete-key", []byte("{}"), ""), 200)
			if u.State != "completed" || u.AssetID == "" || len(s.media.Get(project.ID, "personal-demo").Assets) != 1 {
				t.Fatal("completion duplicated/lost asset", u)
			}
			again := uploadSessionFrom(t, uploadCall(h, "POST", prefix+"/complete"+q, "different-completion-key", []byte("{}"), ""), 200)
			if again.AssetID != u.AssetID {
				t.Fatal("completion changed")
			}
			state := s.media.Get(project.ID, "personal-demo")
			asset := state.Assets[0]
			p, _, e := s.media.file(project.ID, "personal-demo", asset.ID, false, false)
			if e != nil {
				t.Fatal(e)
			}
			got, e := os.ReadFile(p)
			if e != nil || !bytes.Equal(got, payload) {
				t.Fatal("published bytes changed", e)
			}
			i := uploadrepo.Identity{Space: "personal-demo", Project: project.ID, Actor: "demo", ID: u.ID}
			if e = s.uploads.With(context.Background(), i, func(l *uploadrepo.Locked) error {
				_, _, e := l.Part(0)
				if !errors.Is(e, uploads.ErrNotFound) {
					t.Error("temporary bytes retained", e)
				}
				return nil
			}); e != nil {
				t.Fatal(e)
			}
		})
	}
}

func TestResumableUploadFullChecksumConcurrentPartsAndExpiry(t *testing.T) {
	s := testStore(t)
	project, _, e := s.CreateProject("personal-demo", "checks-project", projectInput())
	if e != nil {
		t.Fatal(e)
	}
	data := []byte("invalid media is still subject to complete-time validation")
	u := newUploadForTest(t, s, project.ID, "personal-demo", "demo", data)
	id := uploadrepo.Identity{Space: u.Space, Project: u.Project, Actor: u.Actor, ID: u.ID}
	var wg sync.WaitGroup
	out := make(chan error, 2)
	for n := 0; n < 2; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			b := bytes.Clone(data)
			if n == 1 {
				b[0] ^= 1
			}
			out <- s.uploads.With(context.Background(), id, func(l *uploadrepo.Locked) error { return l.Put(0, b, uploads.BytesSHA(b)) })
		}(n)
	}
	wg.Wait()
	close(out)
	wins, conflicts := 0, 0
	for e := range out {
		if e == nil {
			wins++
		} else if errors.Is(e, uploads.ErrConflict) {
			conflicts++
		} else {
			t.Fatal(e)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatal(wins, conflicts)
	}
	h := Handler(s, "")
	base := "/api/v1/projects/" + project.ID + "/media/upload-sessions/" + u.ID
	if w := uploadCall(h, "POST", base+"/complete?space_id=personal-demo", "complete-key", []byte("{}"), ""); w.Code != 400 {
		t.Fatal("bad media/hash accepted", w.Code, w.Body.String())
	}
	if len(s.media.Get(project.ID, "personal-demo").Assets) != 0 {
		t.Fatal("invalid upload created asset")
	}
	if n, e := s.uploads.Expire(context.Background(), time.Now().Add(8*24*time.Hour), 100); e != nil || n != 1 {
		t.Fatal(n, e)
	}
	got, e := s.uploads.Get(context.Background(), id)
	if e != nil || got.State != "cancelled" {
		t.Fatal(got, e)
	}
	if e = s.uploads.With(context.Background(), id, func(l *uploadrepo.Locked) error { return l.Put(0, data, uploads.BytesSHA(data)) }); !errors.Is(e, uploads.ErrClosed) {
		t.Fatal(e)
	}
}

func TestResumableUploadSecureOwnerCSRFIsolationAndRevocation(t *testing.T) {
	s := testStore(t)
	auth, e := OpenAuthStore(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	owner, token, session, e := auth.login("upload-owner@example.invalid", "Synthetic-upload-password!", true)
	if e != nil {
		t.Fatal(e)
	}
	_, et, es, e := auth.login("upload-editor@example.invalid", "Synthetic-upload-password!", true)
	if e != nil {
		t.Fatal(e)
	}
	space := auth.spaces(owner.ID)[0]["id"]
	project, _, e := s.CreateProject(space, "secure-upload-project", projectInput())
	if e != nil {
		t.Fatal(e)
	}
	h := SecureHandler(s, "http://localhost:5173", auth, AuthOptions{AllowRegistration: true})
	base := "/api/v1/projects/" + project.ID + "/media/upload-sessions"
	q := "?space_id=" + space
	in := uploads.Input{Name: "private-original.wav", MIME: "audio/wav", Size: 4, SHA256: uploads.BytesSHA([]byte("test")), Role: "shot"}
	b, _ := json.Marshal(in)
	authRequest(t, h, "POST", base+q, string(b), token, "", 403)
	w := authRequest(t, h, "POST", base+q, string(b), token, session.CSRF, 201)
	var u uploads.Session
	json.Unmarshal(w.Body.Bytes(), &u)
	authRequest(t, h, "PUT", "/api/v1/organizations/"+space+"/members", `{"email":"upload-editor@example.invalid","role":"editor"}`, token, session.CSRF, 200)
	authRequest(t, h, "GET", base+"/"+u.ID+q, "", et, "", 404)
	w = authRequest(t, h, "GET", base+q, "", et, "", 200)
	if bytes.Contains(w.Body.Bytes(), []byte(in.Name)) {
		t.Fatal("private transfer leaked")
	}
	authRequest(t, h, "POST", base+"/"+u.ID+"/cancel"+q, "{}", et, es.CSRF, 404)
	authRequest(t, h, "POST", base+"/"+u.ID+"/complete"+q, "{}", token, session.CSRF, 409)
	authRequest(t, h, "PUT", "/api/v1/organizations/"+space+"/members", `{"email":"upload-editor@example.invalid","role":"viewer"}`, token, session.CSRF, 200)
	authRequest(t, h, "POST", base+q, string(b), et, es.CSRF, 403)
	authRequest(t, h, "PUT", "/api/v1/organizations/"+space+"/members", `{"email":"upload-editor@example.invalid","role":"editor"}`, token, session.CSRF, 200)
	w = authRequest(t, h, "POST", base+q, string(b), et, es.CSRF, 201)
	var editorUpload uploads.Session
	if e = json.Unmarshal(w.Body.Bytes(), &editorUpload); e != nil {
		t.Fatal(e)
	}
	part := func(want int) {
		r := httptest.NewRequest("PUT", base+"/"+editorUpload.ID+"/parts/0"+q, bytes.NewReader([]byte("test")))
		r.Header.Set("Content-Type", "application/octet-stream")
		r.Header.Set("Origin", "http://localhost:5173")
		r.Header.Set("X-CSRF-Token", es.CSRF)
		r.Header.Set("X-Chunk-SHA256", uploads.BytesSHA([]byte("test")))
		r.Header.Set("Idempotency-Key", "secure-part-key")
		r.AddCookie(&http.Cookie{Name: "studio_session", Value: et})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	part(200)
	authRequest(t, h, "PUT", "/api/v1/organizations/"+space+"/members", `{"email":"upload-editor@example.invalid","role":"remove"}`, token, session.CSRF, 200)
	part(403)
	authRequest(t, h, "POST", base+"/"+editorUpload.ID+"/complete"+q, "{}", et, es.CSRF, 403)
	authRequest(t, h, "GET", base+"/"+editorUpload.ID+q, "", et, "", 403)
	if len(s.media.Get(project.ID, space).Assets) != 0 {
		t.Fatal("revoked upload created asset")
	}
}

func TestResumableUploadOfflineMigrationPreservesPendingBytesAndSource(t *testing.T) {
	db := postgresTestDB(t)
	dir := t.TempDir()
	s, e := OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	project, _, e := s.CreateProject("personal-demo", "pending-migrate-project", projectInput())
	if e != nil {
		t.Fatal(e)
	}
	data := bytes.Repeat([]byte("synthetic transfer"), int(uploads.ChunkBytes/18)+100)
	// Stage exactly one full chunk and leave additional bytes pending.
	if int64(len(data)) <= uploads.ChunkBytes {
		data = append(data, make([]byte, uploads.ChunkBytes-int64(len(data))+1)...)
	}
	u := newUploadForTest(t, s, project.ID, "personal-demo", "demo", data)
	i := uploadrepo.Identity{Space: u.Space, Project: u.Project, Actor: u.Actor, ID: u.ID}
	chunk := data[:uploads.ChunkBytes]
	if e = s.uploads.With(context.Background(), i, func(l *uploadrepo.Locked) error { return l.Put(0, chunk, uploads.BytesSHA(chunk)) }); e != nil {
		t.Fatal(e)
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	before, e := migrationMediaInventory(dir)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = ImportPostgresData(context.Background(), dir, db); e != nil {
		t.Fatal(e)
	}
	after, e := migrationMediaInventory(dir)
	if e != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("offline import changed original files", e)
	}
	pg := postgresTestStore(t, db)
	got, e := pg.uploads.Get(context.Background(), i)
	if e != nil || got.Offset != uploads.ChunkBytes {
		t.Fatal(got, e)
	}
	if e = pg.uploads.With(context.Background(), i, func(l *uploadrepo.Locked) error {
		_, b, e := l.Part(0)
		if e == nil && !bytes.Equal(b, chunk) {
			t.Error("imported bytes differ")
		}
		return e
	}); e != nil {
		t.Fatal(e)
	}
	target := filepath.Join(t.TempDir(), "restored")
	if _, e = ExportPostgresData(context.Background(), target, db); e != nil {
		t.Fatal(e)
	}
	restored, e := OpenStore(target)
	if e != nil {
		t.Fatal(e)
	}
	defer restored.Close()
	got, e = restored.uploads.Get(context.Background(), i)
	if e != nil || got.Offset != uploads.ChunkBytes {
		t.Fatal(got, e)
	}
	if e = restored.uploads.With(context.Background(), i, func(l *uploadrepo.Locked) error {
		_, b, e := l.Part(0)
		if e == nil && !bytes.Equal(b, chunk) {
			t.Error("exported bytes differ")
		}
		return e
	}); e != nil {
		t.Fatal(e)
	}
}

func TestResumableUploadReadinessRequiresOfflineSchemaUpgrade(t *testing.T) {
	db := postgresTestDB(t)
	if _, e := db.Exec("DROP TABLE studio_upload_parts"); e != nil {
		t.Fatal(e)
	}
	if CheckPostgres(context.Background(), db) == nil {
		t.Fatal("missing upload schema accepted")
	}
	var present bool
	if e := db.QueryRow(`SELECT to_regclass('studio_upload_parts') IS NOT NULL`).Scan(&present); e != nil || present {
		t.Fatal("readiness performed DDL", e)
	}
	if e := InitPostgres(context.Background(), db); e != nil {
		t.Fatal(e)
	}
	if e := CheckPostgres(context.Background(), db); e != nil {
		t.Fatal(e)
	}
}

func TestResumableUploadPostgresRefusesLocalTransferDataWithoutMutatingCache(t *testing.T) {
	db := postgresTestDB(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "uploads")
	if e := os.Mkdir(path, 0700); e != nil {
		t.Fatal(e)
	}
	file := filepath.Join(path, "existing-transfer")
	if e := os.WriteFile(file, []byte("preserve local transfer"), 0600); e != nil {
		t.Fatal(e)
	}
	if s, e := OpenPostgresStore(dir, db); e == nil {
		s.Close()
		t.Fatal("ignored existing upload data")
	}
	entries, e := os.ReadDir(dir)
	if e != nil || len(entries) != 1 || entries[0].Name() != "uploads" {
		t.Fatal("cache changed before refusal", entries, e)
	}
	b, e := os.ReadFile(file)
	if e != nil || string(b) != "preserve local transfer" {
		t.Fatal("local bytes changed", e)
	}
}

func TestResumableUploadFinalizationDoesNotHoldMigrationTransaction(t *testing.T) {
	db := postgresTestDB(t)
	s := postgresTestStore(t, db)
	project, _, e := s.CreateProject("personal-demo", "upload-barrier-project", projectInput())
	if e != nil {
		t.Fatal(e)
	}
	u := newUploadForTest(t, s, project.ID, "personal-demo", "demo", []byte("test"))
	id := uploadrepo.Identity{Space: u.Space, Project: u.Project, Actor: u.Actor, ID: u.ID}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	e = s.uploads.With(ctx, id, func(l *uploadrepo.Locked) error {
		conn, e := db.Conn(ctx)
		if e != nil {
			return e
		}
		defer conn.Close()
		// A backup can acquire its barrier while finalization owns the upload
		// session. Nested media acceptance cannot deadlock behind that barrier.
		if _, e = conn.ExecContext(ctx, `SELECT pg_advisory_lock(728219117)`); e != nil {
			return e
		}
		if _, e = conn.ExecContext(ctx, `SELECT pg_advisory_unlock(728219117)`); e != nil {
			return e
		}
		return l.Put(0, []byte("test"), uploads.BytesSHA([]byte("test")))
	})
	if e != nil {
		t.Fatal("session lock held a long migration transaction", e)
	}
}

func TestResumableUploadCorruptAndSymlinkedChunksFailClosed(t *testing.T) {
	for _, mode := range []string{"corrupt-bytes", "symlink"} {
		t.Run(mode, func(t *testing.T) {
			s := testStore(t)
			project, _, e := s.CreateProject("personal-demo", "corrupt-upload-project", projectInput())
			if e != nil {
				t.Fatal(e)
			}
			data := []byte("test")
			u := newUploadForTest(t, s, project.ID, "personal-demo", "demo", data)
			id := uploadrepo.Identity{Space: u.Space, Project: u.Project, Actor: u.Actor, ID: u.ID}
			if e = s.uploads.With(context.Background(), id, func(l *uploadrepo.Locked) error { return l.Put(0, data, u.SHA256) }); e != nil {
				t.Fatal(e)
			}
			path := filepath.Join(filepath.Dir(s.path), "uploads", uploads.Digest(u.Space), u.ID, "00000000000000000000.part")
			if mode == "corrupt-bytes" {
				f, e := os.OpenFile(path, os.O_RDWR, 0)
				if e != nil {
					t.Fatal(e)
				}
				st, _ := f.Stat()
				_, e = f.WriteAt([]byte("x"), st.Size()-1)
				f.Close()
				if e != nil {
					t.Fatal(e)
				}
			} else {
				saved := filepath.Join(t.TempDir(), "outside-record")
				if e = os.Rename(path, saved); e != nil {
					t.Fatal(e)
				}
				if e = os.Symlink(saved, path); e != nil {
					t.Fatal(e)
				}
			}
			w := uploadCall(Handler(s, ""), "POST", "/api/v1/projects/"+project.ID+"/media/upload-sessions/"+u.ID+"/complete?space_id=personal-demo", "corrupt-complete-key", []byte("{}"), "")
			if w.Code != 503 {
				t.Fatal("corrupt transfer did not fail closed", w.Code, w.Body.String())
			}
			if len(s.media.Get(project.ID, u.Space).Assets) != 0 {
				t.Fatal("corrupt data became a project asset")
			}
		})
	}
}

func TestResumableUploadLargeValidAudio(t *testing.T) {
	if os.Getenv("STUDIO_UPLOAD_LARGE_TEST") != "1" {
		t.Skip("STUDIO_UPLOAD_LARGE_TEST=1 enables the full 184MB media transfer and decode")
	}
	s := testStore(t)
	project, _, e := s.CreateProject("personal-demo", "large-upload-project", projectInput())
	if e != nil {
		t.Fatal(e)
	}
	f, e := os.CreateTemp(t.TempDir(), "audio-")
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	size := int64(120*192000*8 + 44)
	if _, e = f.Write(wavHeader(size-44, 192000)); e != nil {
		t.Fatal(e)
	}
	if e = f.Truncate(size); e != nil {
		t.Fatal(e)
	}
	if _, e = f.Seek(0, 0); e != nil {
		t.Fatal(e)
	}
	info, e := localBlobInfo(context.Background(), "unused", f.Name())
	if e != nil {
		t.Fatal(e)
	}
	in := uploads.Input{Name: "120-second-pcm.wav", MIME: "audio/wav", Size: size, SHA256: info.SHA256, Role: "shot"}
	b, _ := json.Marshal(in)
	h := Handler(s, "")
	base := "/api/v1/projects/" + project.ID + "/media/upload-sessions"
	q := "?space_id=personal-demo"
	u := uploadSessionFrom(t, uploadCall(h, "POST", base+q, "large-upload-key", b, ""), 201)
	for offset := int64(0); offset < size; {
		part := make([]byte, min(uploads.ChunkBytes, size-offset))
		if _, e = io.ReadFull(f, part); e != nil {
			t.Fatal(e)
		}
		u = uploadSessionFrom(t, uploadCall(h, "PUT", fmt.Sprintf("%s/%s/parts/%d%s", base, u.ID, offset, q), fmt.Sprintf("chunk-%020d", offset), part, uploads.BytesSHA(part)), 200)
		offset = u.Offset
	}
	u = uploadSessionFrom(t, uploadCall(h, "POST", base+"/"+u.ID+"/complete"+q, "complete-large-key", []byte("{}"), ""), 200)
	path, _, e := s.media.file(project.ID, "personal-demo", u.AssetID, false, false)
	if e != nil {
		t.Fatal(e)
	}
	actual, e := localBlobInfo(context.Background(), "unused", path)
	if e != nil || actual.Size != size || actual.SHA256 != info.SHA256 {
		t.Fatal(actual, e)
	}
	if out, e := exec.Command("ffmpeg", "-v", "error", "-i", path, "-f", "null", "-").CombinedOutput(); e != nil {
		t.Fatal(e, string(out))
	}
	t.Logf("verified %d bytes, 120-second PCM, all chunks and full decode", size)
}

func TestResumableUploadCompletesSameS3TransferAfterAPIRestart(t *testing.T) {
	for _, mode := range []string{"file", "postgres"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			var s, next *FileStore
			var e error
			if mode == "file" {
				s, e = OpenStore(dir)
				if e != nil {
					t.Fatal(e)
				}
			} else {
				db := postgresTestDB(t)
				s = postgresTestStore(t, db)
				next = postgresTestStore(t, db)
			}
			defer func() {
				s.Close()
				if next != nil {
					next.Close()
				}
			}()
			fixture := newMultipartFixture(t)
			if e := configureBlobStorage(s, fixture.store(t, t.TempDir())); e != nil {
				t.Fatal(e)
			}
			project, _, e := s.CreateProject("personal-demo", "multipart-project", projectInput())
			if e != nil {
				t.Fatal(e)
			}
			data := make([]byte, s3MultipartThreshold+44+256)
			copy(data, wavHeader(int64(len(data))-44, 192000))
			u := newUploadForTest(t, s, project.ID, "personal-demo", "demo", data)
			i := uploadrepo.Identity{Space: u.Space, Project: u.Project, Actor: u.Actor, ID: u.ID}
			if e := s.uploads.With(context.Background(), i, func(l *uploadrepo.Locked) error {
				for offset := int64(0); offset < int64(len(data)); {
					end := min(offset+uploads.ChunkBytes, int64(len(data)))
					part := data[offset:end]
					if e := l.Put(offset, part, uploads.BytesSHA(part)); e != nil {
						return e
					}
					offset = end
				}
				return nil
			}); e != nil {
				t.Fatal(e)
			}
			fixture.failPart = 2
			url := "/api/v1/projects/" + project.ID + "/media/upload-sessions/" + u.ID + "/complete?space_id=personal-demo"
			if w := uploadCall(Handler(s, ""), "POST", url, "complete-first", []byte("{}"), ""); w.Code != 500 || !strings.Contains(w.Body.String(), "media_storage_error") {
				t.Fatal("S3 failure not surfaced", w.Code, w.Body.String())
			}
			if len(s.media.Get(project.ID, u.Space).Assets) != 0 {
				t.Fatal("partial S3 object visible as asset")
			}
			if e := s.Close(); e != nil {
				t.Fatal(e)
			}
			if mode == "file" {
				next, e = OpenStore(dir)
				if e != nil {
					t.Fatal(e)
				}
			}
			if e := configureBlobStorage(next, fixture.store(t, t.TempDir())); e != nil {
				t.Fatal(e)
			}
			u = uploadSessionFrom(t, uploadCall(Handler(next, ""), "POST", url, "complete-retry", []byte("{}"), ""), 200)
			again := uploadSessionFrom(t, uploadCall(Handler(next, ""), "POST", url, "complete-replay", []byte("{}"), ""), 200)
			if u.AssetID == "" || again.AssetID != u.AssetID || len(next.media.Get(project.ID, u.Space).Assets) != 1 {
				t.Fatal("completion duplicated asset", u, again)
			}
			key := blobKey(u.Space, project.ID, u.AssetID)
			if e := verifyBlobStream(context.Background(), next.blobs.store, blobInfo(key, data)); e != nil {
				t.Fatal("accepted media mismatch", e)
			}
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			ones := 0
			for _, n := range fixture.puts {
				if n == 1 {
					ones++
				}
			}
			if fixture.starts != 1 || fixture.completes != 1 || ones != 1 {
				t.Fatal("API restart did not resume same transfer", fixture.starts, fixture.completes, fixture.puts)
			}
		})
	}
}
