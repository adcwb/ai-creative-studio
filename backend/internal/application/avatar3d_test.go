package application

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func avatar3DTestGLB(t *testing.T, uri string) string {
	t.Helper()
	nodes := []map[string]string{}
	for _, name := range []string{"Hips", "Spine", "Neck", "Head", "LeftArm", "RightArm", "LeftHand", "RightHand", "LeftUpLeg", "RightUpLeg"} {
		nodes = append(nodes, map[string]string{"name": name})
	}
	buffer := map[string]any{"byteLength": 4}
	if uri != "" {
		buffer["uri"] = uri
	}
	doc := map[string]any{"asset": map[string]string{"version": "2.0"}, "buffers": []any{buffer}, "nodes": nodes, "meshes": []any{map[string]any{"extras": map[string]any{"targetNames": []string{"jawOpen", "eyeBlinkLeft", "eyeBlinkRight"}}}}}
	jsonBytes, _ := json.Marshal(doc)
	for len(jsonBytes)%4 != 0 {
		jsonBytes = append(jsonBytes, ' ')
	}
	raw := new(bytes.Buffer)
	raw.WriteString("glTF")
	binary.Write(raw, binary.LittleEndian, uint32(2))
	binary.Write(raw, binary.LittleEndian, uint32(28+len(jsonBytes)+4))
	binary.Write(raw, binary.LittleEndian, uint32(len(jsonBytes)))
	raw.WriteString("JSON")
	raw.Write(jsonBytes)
	binary.Write(raw, binary.LittleEndian, uint32(4))
	raw.WriteString("BIN\x00")
	raw.Write([]byte{0, 0, 0, 0})
	path := filepath.Join(t.TempDir(), "model.glb")
	if e := os.WriteFile(path, raw.Bytes(), 0600); e != nil {
		t.Fatal(e)
	}
	return path
}
func TestAvatar3DGLBProfileRejectsExternalAndMalformedInputs(t *testing.T) {
	path := avatar3DTestGLB(t, "")
	profile, e := validateAvatarGLB(context.Background(), path)
	if e != nil || profile.Nodes != 10 || profile.Morphs != 3 {
		t.Fatal(profile, e)
	}
	for _, uri := range []string{"https://evil.invalid/secret", "data:application/octet-stream;base64,AA==", "file:///etc/passwd"} {
		if _, e = validateAvatarGLB(context.Background(), avatar3DTestGLB(t, uri)); e == nil {
			t.Fatal("external URI accepted")
		}
	}
	raw, _ := os.ReadFile(path)
	if e = os.WriteFile(path, append(raw, 0), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = validateAvatarGLB(context.Background(), path); e == nil {
		t.Fatal("bad GLB length accepted")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = validateAvatarGLB(cancelled, path); !errors.Is(e, context.Canceled) {
		t.Fatal("cancelled validation", e)
	}
	decoder := json.NewDecoder(&avatarJSONReader{r: strings.NewReader(`{"name":"` + strings.Repeat("x", 20000) + `"}`)})
	var value any
	if decoder.Decode(&value) == nil {
		t.Fatal("unbounded scalar accepted")
	}
}
func TestAvatar3DLicensedBuiltinIsActualCompatibleGLB(t *testing.T) {
	f, e := os.Open("../../../../frontend/public/avatars/mpfb-63c645a2.glb.gz")
	if os.IsNotExist(e) {
		f, e = os.Open("../../../frontend/public/avatars/mpfb-63c645a2.glb.gz")
	}
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	z, e := gzip.NewReader(f)
	if e != nil {
		t.Fatal(e)
	}
	defer z.Close()
	path := filepath.Join(t.TempDir(), "mpfb.glb")
	out, e := os.Create(path)
	if e != nil {
		t.Fatal(e)
	}
	_, e = io.Copy(out, z)
	if closeErr := out.Close(); e == nil {
		e = closeErr
	}
	if e != nil {
		t.Fatal(e)
	}
	if _, e = validateAvatarGLB(context.Background(), path); e != nil {
		t.Fatal("licensed source profile", e)
	}
	info, e := localBlobInfo(context.Background(), "unused", path)
	if e != nil || info.SHA256 != avatar3DBuiltinSHA {
		t.Fatal("licensed source changed", info, e)
	}
}
func TestAvatar3DModelPersistenceConsentAndScope(t *testing.T) {
	for _, mode := range []string{"file", "postgres"} {
		t.Run(mode, func(t *testing.T) {
			s := testStore(t)
			if mode == "postgres" {
				s = postgresTestStore(t, postgresTestDB(t))
				blobs, _ := NewFileBlobStore(t.TempDir())
				if e := configureBlobStorage(s, blobs); e != nil {
					t.Fatal(e)
				}
			}
			p, in, _ := cloudAvatarFixture(t, s, "three-model-space")
			meta := avatar3DInput{Name: "My character", Body: "F", License: "original-license-1", Consent: "creator-likeness-consent", Confirmed: true}
			ctx := context.Background()
			file := avatar3DTestGLB(t, "")
			model, e := s.saveAvatar3D(ctx, p.SpaceID, p.ID, "three-model-1", "test-actor", meta, file)
			if e != nil {
				t.Fatal(e)
			}
			again, e := s.saveAvatar3D(ctx, p.SpaceID, p.ID, "three-model-1", "test-actor", meta, file)
			if e != nil || again.ID != model.ID {
				t.Fatal("model replay", again, e)
			}
			changed := meta
			changed.Name = "different"
			if _, e = s.saveAvatar3D(ctx, p.SpaceID, p.ID, "three-model-1", "test-actor", changed, file); e == nil {
				t.Fatal("changed key body accepted")
			}
			if _, e = s.avatar3D("foreign", p.ID, model.ID, true); e == nil {
				t.Fatal("foreign model visible")
			}
			if e = s.checkAvatar3DPlayback(ctx, p.SpaceID, p.ID, model.ID, "test-actor", in.AudioAssetID, in.VoiceAuthorizationID); e != nil {
				t.Fatal("authorized playback", e)
			}
			refs, e := avatarCheckpointReferences(s.creative.data)
			if e != nil || len(refs) != 1 || refs[0] != *model.Model {
				t.Fatal("model proof missing in backup", refs, e)
			}
			private := filepath.Join(t.TempDir(), "copy.glb")
			if e = s.materializeAvatarMedia(ctx, *model.Model, private); e != nil {
				t.Fatal(e)
			}
			if mode == "file" {
				dir := filepath.Dir(s.creative.path)
				if e = s.Close(); e != nil {
					t.Fatal(e)
				}
				s, e = OpenStore(dir)
				if e != nil {
					t.Fatal("reopen", e)
				}
			} else {
				s = postgresTestStore(t, s.pg.db)
			}
			restored, e := s.avatar3D(p.SpaceID, p.ID, model.ID, true)
			if e != nil || restored.SHA != model.SHA {
				t.Fatal("persistent model", restored, e)
			}
			mux := http.NewServeMux()
			registerVoiceAuthorizationRoutes(mux, s)
			r := httptest.NewRequest("POST", "/api/v1/projects/"+p.ID+"/voice-authorizations/"+in.VoiceAuthorizationID+"/revoke", strings.NewReader(`{"confirmed":true}`))
			r = r.WithContext(context.WithValue(r.Context(), authScopeKey{}, authScope{SpaceID: p.SpaceID, UserID: "test-actor", Role: "editor"}))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Idempotency-Key", "revoke-three-voice")
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			if w.Code != 200 {
				t.Fatal("revoke", w.Code, w.Body.String())
			}
			if e = s.checkAvatar3DPlayback(ctx, p.SpaceID, p.ID, model.ID, "test-actor", in.AudioAssetID, in.VoiceAuthorizationID); e == nil {
				t.Fatal("revoked voice used")
			}
		})
	}
}

func TestAvatar3DHTTPAuthorizationUploadsAndRevocation(t *testing.T) {
	for _, mode := range []string{"file", "postgres"} {
		t.Run(mode, func(t *testing.T) {
			s := testStore(t)
			if mode == "postgres" {
				s = postgresTestStore(t, postgresTestDB(t))
				b, _ := NewFileBlobStore(t.TempDir())
				if e := configureBlobStorage(s, b); e != nil {
					t.Fatal(e)
				}
			}
			_, _, auth, actor := workflowFixture(t, s)
			_, token, session, e := auth.login("workflow-editor@example.test", "synthetic-password", false)
			if e != nil {
				t.Fatal(e)
			}
			space := ""
			auth.mu.Lock()
			for id, o := range auth.data.Organizations {
				if o.Members[actor] == "editor" {
					space = id
				}
			}
			auth.mu.Unlock()
			p, _, in, _ := avatarFixture(t, s, space)
			grant, e := s.saveVoiceAuthorization(space, p.ID, "three-secure-voice", actor, in.AudioAssetID, "voice-license-evidence", true)
			if e != nil {
				t.Fatal(e)
			}
			h := SecureHandler(s, "http://localhost:5173", auth, AuthOptions{})
			base := "/api/v1/projects/" + p.ID + "/avatar-3d-models"
			query := "?space_id=" + space
			viewer, vt, vs, e := auth.login("three-viewer@example.test", "synthetic-viewer-password", true)
			if e != nil {
				t.Fatal(e)
			}
			auth.mu.Lock()
			snapshot := auth.clone()
			org := snapshot.Organizations[space]
			org.Members[viewer.ID] = "viewer"
			org.Version++
			snapshot.Organizations[space] = org
			e = auth.commit(snapshot)
			auth.mu.Unlock()
			if e != nil {
				t.Fatal(e)
			}
			_, ft, fs, e := auth.login("three-foreign@example.test", "synthetic-foreign-password", true)
			if e != nil {
				t.Fatal(e)
			}
			body := projectJSON(t, avatar3DInput{Name: "CC0 model", Builtin: avatar3DBuiltin, Body: "F", License: "CC0 1.0", Consent: "fictional character consent", Confirmed: true})
			authRequest(t, h, "POST", base+query, body, token, "", 403)
			authRequest(t, h, "POST", base+query, body, vt, vs.CSRF, 403)
			authRequest(t, h, "POST", base+query, body, ft, fs.CSRF, 403)
			response := authRequest(t, h, "POST", base+query, body, token, session.CSRF, 200)
			var builtin Avatar3D
			if e = json.Unmarshal(response.Body.Bytes(), &builtin); e != nil || builtin.CreatedBy != actor || builtin.SHA != avatar3DBuiltinSHA {
				t.Fatal("server-bound model", response.Body.String(), e)
			}
			// Actual multipart boundary, metadata and private file streaming, not a fake
			// upload response. The fixture validates the accepted profile, not rendering.
			raw, _ := os.ReadFile(avatar3DTestGLB(t, ""))
			var upload bytes.Buffer
			writer := multipart.NewWriter(&upload)
			part, _ := writer.CreateFormField("metadata")
			part.Write([]byte(projectJSON(t, avatar3DInput{Name: "Private model", Body: "F", License: "original license", Consent: "likeness permission", Confirmed: true})))
			part, _ = writer.CreateFormFile("model", "private.glb")
			part.Write(raw)
			writer.Close()
			req := httptest.NewRequest("POST", base+query, bytes.NewReader(upload.Bytes()))
			req.Header.Set("Content-Type", writer.FormDataContentType())
			req.Header.Set("Origin", "http://localhost:5173")
			req.Header.Set("X-CSRF-Token", session.CSRF)
			req.Header.Set("Idempotency-Key", "three-private-upload")
			req.AddCookie(&http.Cookie{Name: "studio_session", Value: token})
			out := httptest.NewRecorder()
			h.ServeHTTP(out, req)
			if out.Code != 200 {
				t.Fatal("multipart upload", out.Code, out.Body.String())
			}
			for _, invalid := range []struct {
				metadata string
				data     []byte
			}{
				{`null`, raw},
				{`{"name":"name","unknown":true}`, raw},
				{projectJSON(t, avatar3DInput{Name: "Invalid file", Body: "F", License: "original license", Consent: "likeness permission", Confirmed: true}), raw[:11]},
				{projectJSON(t, avatar3DInput{Name: "Invalid file", Body: "F", License: "original license", Consent: "likeness permission", Confirmed: true}), raw[:len(raw)-1]},
				{projectJSON(t, avatar3DInput{Name: "Invalid file", Body: "F", License: "original license", Consent: "likeness permission", Confirmed: true}), append(append([]byte{}, raw...), 0)},
			} {
				var bad bytes.Buffer
				mw := multipart.NewWriter(&bad)
				mp, _ := mw.CreateFormField("metadata")
				mp.Write([]byte(invalid.metadata))
				mp, _ = mw.CreateFormFile("model", "untrusted.glb")
				mp.Write(invalid.data)
				mw.Close()
				badRequest := httptest.NewRequest("POST", base+query, bytes.NewReader(bad.Bytes()))
				badRequest.Header.Set("Content-Type", mw.FormDataContentType())
				badRequest.Header.Set("Origin", "http://localhost:5173")
				badRequest.Header.Set("X-CSRF-Token", session.CSRF)
				badRequest.Header.Set("Idempotency-Key", "three-invalid-upload")
				badRequest.AddCookie(&http.Cookie{Name: "studio_session", Value: token})
				badResponse := httptest.NewRecorder()
				h.ServeHTTP(badResponse, badRequest)
				if badResponse.Code != 400 {
					t.Fatal("malformed upload must fail safely", badResponse.Code, badResponse.Body.String())
				}
			}
			var model Avatar3D
			if e = json.Unmarshal(out.Body.Bytes(), &model); e != nil || model.Model != nil {
				t.Fatal("private proof exposed", out.Body.String(), e)
			}
			recovered := authRequest(t, h, "GET", base+"/requests/three-private-upload/result"+query, "", token, session.CSRF, 200)
			var receipt Avatar3D
			if e = json.Unmarshal(recovered.Body.Bytes(), &receipt); e != nil || receipt.ID != model.ID || receipt.Model != nil {
				t.Fatal("original upload receipt", recovered.Body.String(), e)
			}
			authRequest(t, h, "GET", base+"/requests/three-private-upload/result"+query, "", ft, fs.CSRF, 403)
			authRequest(t, h, "GET", base+"/requests/three-never-uploaded/result"+query, "", token, session.CSRF, 404)
			filePath := base + "/" + model.ID + "/file" + query
			r := httptest.NewRequest("GET", filePath, nil)
			r.Header.Set("Range", "bytes=0-11")
			r.AddCookie(&http.Cookie{Name: "studio_session", Value: vt})
			out = httptest.NewRecorder()
			h.ServeHTTP(out, r)
			if out.Code != 206 || out.Header().Get("Content-Type") != "model/gltf-binary" || !bytes.Equal(out.Body.Bytes(), raw[:12]) {
				t.Fatal("private range read", out.Code, out.Body.String())
			}
			authRequest(t, h, "GET", filePath, "", ft, fs.CSRF, 403)
			playback := projectJSON(t, map[string]string{"audio_asset_id": in.AudioAssetID, "voice_authorization_id": grant.ID})
			path := base + "/" + model.ID + "/playbacks" + query
			authRequest(t, h, "POST", path, playback, vt, vs.CSRF, 403)
			authRequest(t, h, "POST", path, playback, token, session.CSRF, 200)
			authRequest(t, h, "POST", base+"/"+model.ID+"/revoke"+query, `{"confirmed":true}`, token, session.CSRF, 200)
			authRequest(t, h, "POST", path, playback, token, session.CSRF, 403)
			authRequest(t, h, "GET", filePath, "", token, session.CSRF, 403)
		})
	}
}

// Verify an actual legacy constraint upgrade, rather than assuming CREATE
// TABLE IF NOT EXISTS can change a pre-existing CHECK definition.
func TestAvatar3DPostgresLegacyBlobSchemaMigration(t *testing.T) {
	db := postgresTestDB(t)
	_, err := db.Exec(`ALTER TABLE studio_blobs DROP CONSTRAINT studio_blobs_key_kind_v2;
 ALTER TABLE studio_blobs ADD CONSTRAINT studio_blobs_key_check CHECK(key ~ '^[a-f0-9]{64}/[a-f0-9]{64}/[a-f0-9]{32}(\.(png|mp4|srt|vtt))?$')`)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = InitPostgres(context.Background(), db); err != nil {
			t.Fatal("restartable migration", err)
		}
	}
	key := blobKey("three-space", "three-project", strings.Repeat("a", 32)+".glb")
	if _, err = db.Exec(`INSERT INTO studio_blobs(key,tenant_hash,project_hash,size,sha256) VALUES($1,split_part($1,'/',1),split_part($1,'/',2),4,$2)`, key, strings.Repeat("b", 64)); err != nil {
		t.Fatal("migrated GLB key rejected", err)
	}
	if _, err = db.Exec(`INSERT INTO studio_blobs(key,tenant_hash,project_hash,size,sha256) VALUES($1,split_part($1,'/',1),split_part($1,'/',2),4,$2)`, strings.TrimSuffix(key, ".glb")+".exe", strings.Repeat("b", 64)); err == nil {
		t.Fatal("unsafe blob key accepted")
	}
}

func TestAvatar3DBlobMigrationRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)
	p := createProject(t, s)
	v, e := s.saveAvatar3D(ctx, p.SpaceID, p.ID, "three-migration", "test-actor", avatar3DInput{Name: "Private rig", Body: "F", License: "original GLB", Consent: "fictional likeness", Confirmed: true}, avatar3DTestGLB(t, ""))
	if e != nil {
		t.Fatal(e)
	}
	dir := filepath.Dir(s.path)
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	remote, _ := fakeS3Blobs(t)
	report, e := MigrateBlobStorage(ctx, dir, remote, false)
	if e != nil || report.Objects != 1 {
		t.Fatal("3D migration", report, e)
	}
	original := filepath.Join(dir, "media", filepath.Base(v.Model.Key))
	if e = os.Remove(original); e != nil {
		t.Fatal(e)
	}
	s, e = OpenStore(dir)
	if e != nil {
		t.Fatal(e)
	}
	if e = configureBlobStorage(s, remote); e != nil {
		t.Fatal(e)
	}
	restored, e := s.avatar3D(p.SpaceID, p.ID, v.ID, true)
	if e != nil || restored.SHA != v.SHA {
		t.Fatal(restored, e)
	}
	if e = s.materializeAvatarMedia(ctx, *restored.Model, filepath.Join(t.TempDir(), "restored.glb")); e != nil {
		t.Fatal(e)
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	report, e = MigrateBlobStorage(ctx, dir, remote, true)
	if e != nil || report.Objects != 1 {
		t.Fatal("3D rollback", report, e)
	}
	proof, e := localBlobInfo(ctx, v.Model.Key, original)
	if e != nil || proof != *v.Model {
		t.Fatal("3D rollback integrity", proof, e)
	}
}

func TestAvatar3DConcurrentConflictingUploadCannotReplaceAcceptedBytes(t *testing.T) {
	for _, mode := range []string{"file", "postgres"} {
		t.Run(mode, func(t *testing.T) {
			s := testStore(t)
			other := s
			if mode == "postgres" {
				db := postgresTestDB(t)
				s = postgresTestStore(t, db)
				other = postgresTestStore(t, db)
				storage, e := NewFileBlobStore(t.TempDir())
				if e != nil {
					t.Fatal(e)
				}
				for _, store := range []*FileStore{s, other} {
					if e = configureBlobStorage(store, storage); e != nil {
						t.Fatal(e)
					}
				}
			}
			p := createProject(t, s)
			first := avatar3DTestGLB(t, "")
			raw, e := os.ReadFile(first)
			if e != nil {
				t.Fatal(e)
			}
			raw[len(raw)-1] = 1
			second := filepath.Join(t.TempDir(), "different.glb")
			if e = os.WriteFile(second, raw, 0600); e != nil {
				t.Fatal(e)
			}
			type result struct {
				model Avatar3D
				err   error
			}
			results := make(chan result, 2)
			start := make(chan struct{})
			for i, file := range []string{first, second} {
				store := s
				if i == 1 {
					store = other
				}
				go func() {
					<-start
					v, e := store.saveAvatar3D(context.Background(), p.SpaceID, p.ID, "three-concurrent", "test-actor", avatar3DInput{Name: "Concurrent rig", Body: "F", License: "original GLB", Consent: "fictional likeness", Confirmed: true}, file)
					results <- result{v, e}
				}()
			}
			close(start)
			accepted := Avatar3D{}
			conflicts := 0
			for i := 0; i < 2; i++ {
				r := <-results
				if r.err == nil {
					if accepted.ID != "" {
						t.Fatal("two conflicting bodies accepted")
					}
					accepted = r.model
				} else if errors.Is(r.err, ErrIdempotencyConflict) {
					conflicts++
				} else {
					t.Fatal(r.err)
				}
			}
			if accepted.ID == "" || conflicts != 1 {
				t.Fatal("missing atomic winner", accepted, conflicts)
			}
			path := filepath.Join(t.TempDir(), "accepted.glb")
			if e = s.materializeAvatarMedia(context.Background(), *accepted.Model, path); e != nil {
				t.Fatal(e)
			}
			proof, e := localBlobInfo(context.Background(), accepted.Model.Key, path)
			if e != nil || proof != *accepted.Model {
				t.Fatal("accepted bytes changed", proof, e)
			}
		})
	}
}
