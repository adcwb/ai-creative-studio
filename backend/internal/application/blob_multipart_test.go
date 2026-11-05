package application

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type multipartFixturePart struct {
	path string
	part s3Part
}
type multipartFixtureUpload struct {
	key, sha string
	parts    map[int]multipartFixturePart
}
type multipartFixtureObject struct {
	path string
	info BlobInfo
}
type multipartFixture struct {
	mu                                                                    sync.Mutex
	dir                                                                   string
	server                                                                *httptest.Server
	uploads                                                               map[string]*multipartFixtureUpload
	objects                                                               map[string]multipartFixtureObject
	starts, completes, aborts                                             int
	puts                                                                  []int
	failPart, losePart                                                    int
	loseCreate, loseComplete, embeddedError, abortLeavesPart, corruptList bool
	headErrors                                                            int
}

func newMultipartFixture(t *testing.T) *multipartFixture {
	t.Helper()
	f := &multipartFixture{dir: t.TempDir(), uploads: map[string]*multipartFixtureUpload{}, objects: map[string]multipartFixtureObject{}}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}
func (f *multipartFixture) store(t *testing.T, stateDir string) *S3BlobStore {
	t.Helper()
	s, e := NewS3BlobStore(S3BlobConfig{Endpoint: f.server.URL, Bucket: "studio", Region: "us-east-1", AccessKey: "synthetic-access", SecretKey: "synthetic-secret", AllowLoopbackHTTP: true, MultipartStateDir: stateDir})
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func fixtureXML(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/xml")
	b, _ := xml.Marshal(v)
	w.Write(b)
}
func fixtureLost(w http.ResponseWriter) {
	conn, _, e := w.(http.Hijacker).Hijack()
	if e == nil {
		conn.Close()
	}
}
func fixtureError(w http.ResponseWriter, status int, code string) {
	w.WriteHeader(status)
	fixtureXML(w, struct {
		XMLName xml.Name `xml:"Error"`
		Code    string
	}{Code: code})
}

// Independent verifier, anchored by the published AWS vectors below. It uses
// the wire query and declared signed headers, not the production canonicalizer.
func fixtureSignature(r *http.Request) bool {
	auth := r.Header.Get("Authorization")
	fields := strings.Split(strings.TrimPrefix(auth, "AWS4-HMAC-SHA256 "), ", ")
	if len(fields) != 3 {
		return false
	}
	credential := strings.TrimPrefix(fields[0], "Credential=")
	signed := strings.TrimPrefix(fields[1], "SignedHeaders=")
	signature := strings.TrimPrefix(fields[2], "Signature=")
	parts := strings.Split(credential, "/")
	if len(parts) != 5 || parts[0] != "synthetic-access" || parts[2] != "us-east-1" || parts[3] != "s3" || parts[4] != "aws4_request" {
		return false
	}
	var headers strings.Builder
	for _, name := range strings.Split(signed, ";") {
		value := r.Header.Get(name)
		if name == "host" {
			value = r.Host
		}
		headers.WriteString(name + ":" + strings.Join(strings.Fields(value), " ") + "\n")
	}
	query := strings.Split(r.URL.RawQuery, "&")
	sort.Strings(query)
	canonical := strings.Join([]string{r.Method, r.URL.EscapedPath(), strings.Join(query, "&"), headers.String(), signed, r.Header.Get("X-Amz-Content-Sha256")}, "\n")
	hash := func(s string) string { v := sha256.Sum256([]byte(s)); return hex.EncodeToString(v[:]) }
	mac := func(key []byte, value string) []byte {
		h := hmac.New(sha256.New, key)
		h.Write([]byte(value))
		return h.Sum(nil)
	}
	key := mac(mac(mac(mac([]byte("AWS4synthetic-secret"), parts[1]), parts[2]), "s3"), "aws4_request")
	expected := hex.EncodeToString(mac(key, "AWS4-HMAC-SHA256\n"+r.Header.Get("X-Amz-Date")+"\n"+strings.Join(parts[1:], "/")+"\n"+hash(canonical)))
	return signature == expected
}
func (f *multipartFixture) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !fixtureSignature(r) {
		fixtureError(w, 403, "SignatureDoesNotMatch")
		return
	}
	key := strings.TrimPrefix(r.URL.Path, "/studio/")
	q := r.URL.Query()
	id := q.Get("uploadId")
	if r.URL.Path == "/studio" && r.Method == "GET" && q.Has("uploads") {
		type entry struct{ Key, UploadId string }
		list := []entry{}
		for id, u := range f.uploads {
			if strings.HasPrefix(u.key, q.Get("prefix")) {
				list = append(list, entry{u.key, id})
			}
		}
		sort.Slice(list, func(i, j int) bool {
			if list[i].Key == list[j].Key {
				return list[i].UploadId < list[j].UploadId
			}
			return list[i].Key < list[j].Key
		})
		page := []entry{}
		for _, u := range list {
			if u.Key > q.Get("key-marker") || u.Key == q.Get("key-marker") && u.UploadId > q.Get("upload-id-marker") {
				page = append(page, u)
			}
		}
		more := len(page) > 2
		if more {
			page = page[:2]
		}
		nextKey, nextID := "", ""
		if more {
			nextKey = page[len(page)-1].Key
			nextID = page[len(page)-1].UploadId
		}
		fixtureXML(w, struct {
			XMLName   xml.Name `xml:"ListMultipartUploadsResult"`
			Bucket    string
			Truncated bool    `xml:"IsTruncated"`
			NextKey   string  `xml:"NextKeyMarker"`
			NextID    string  `xml:"NextUploadIdMarker"`
			Uploads   []entry `xml:"Upload"`
		}{Bucket: "studio", Truncated: more, NextKey: nextKey, NextID: nextID, Uploads: page})
		return
	}
	if r.Method == "POST" && q.Has("uploads") {
		if r.Header.Get("X-Amz-Checksum-Algorithm") != "SHA256" || r.Header.Get("X-Amz-Meta-Sha256") == "" {
			fixtureError(w, 400, "MissingChecksum")
			return
		}
		f.starts++
		id = fmt.Sprintf("fixture +/= %04d", f.starts)
		f.uploads[id] = &multipartFixtureUpload{key: key, sha: r.Header.Get("X-Amz-Meta-Sha256"), parts: map[int]multipartFixturePart{}}
		if f.loseCreate {
			f.loseCreate = false
			fixtureLost(w)
			return
		}
		fixtureXML(w, struct {
			XMLName               xml.Name `xml:"InitiateMultipartUploadResult"`
			Bucket, Key, UploadId string
		}{Bucket: "studio", Key: key, UploadId: id})
		return
	}
	if id != "" {
		u, ok := f.uploads[id]
		if !ok || u.key != key {
			fixtureError(w, 404, "NoSuchUpload")
			return
		}
		switch r.Method {
		case "GET":
			marker, _ := strconv.Atoi(q.Get("part-number-marker"))
			parts := []s3Part{}
			for n, p := range u.parts {
				if n > marker {
					parts = append(parts, p.part)
				}
			}
			sort.Slice(parts, func(i, j int) bool { return parts[i].Number < parts[j].Number })
			more := len(parts) > 2
			if more {
				parts = parts[:2]
			}
			next := 0
			if more {
				next = parts[len(parts)-1].Number
			}
			if f.corruptList && len(parts) > 0 {
				parts[0].SHA = "wrong-checksum"
			}
			fixtureXML(w, struct {
				XMLName       xml.Name `xml:"ListPartsResult"`
				Key, UploadId string
				Truncated     bool     `xml:"IsTruncated"`
				Next          int      `xml:"NextPartNumberMarker"`
				Parts         []s3Part `xml:"Part"`
			}{Key: key, UploadId: id, Truncated: more, Next: next, Parts: parts})
		case "PUT":
			n, _ := strconv.Atoi(q.Get("partNumber"))
			f.puts = append(f.puts, n)
			if n == f.failPart {
				f.failPart = 0
				fixtureError(w, 503, "SlowDown")
				return
			}
			path := filepath.Join(f.dir, fmt.Sprintf("part-%d-%d", f.starts, n))
			file, e := os.Create(path)
			if e != nil {
				fixtureError(w, 500, "DiskError")
				return
			}
			h := sha256.New()
			size, e := io.Copy(io.MultiWriter(file, h), r.Body)
			file.Close()
			sum := h.Sum(nil)
			if e != nil || size != r.ContentLength || hex.EncodeToString(sum) != r.Header.Get("X-Amz-Content-Sha256") || base64.StdEncoding.EncodeToString(sum) != r.Header.Get("X-Amz-Checksum-Sha256") {
				fixtureError(w, 400, "BadDigest")
				return
			}
			p := s3Part{Number: n, Size: size, ETag: fmt.Sprintf(`"opaque-etag-%d"`, n), SHA: base64.StdEncoding.EncodeToString(sum)}
			u.parts[n] = multipartFixturePart{path, p}
			if n == f.losePart {
				f.losePart = 0
				fixtureLost(w)
				return
			}
			w.Header().Set("ETag", p.ETag)
			w.Header().Set("X-Amz-Checksum-Sha256", p.SHA)
		case "POST":
			f.completes++
			if r.Header.Get("If-None-Match") != "*" {
				fixtureError(w, 400, "MissingConditionalWrite")
				return
			}
			if _, ok := f.objects[key]; ok {
				fixtureError(w, 412, "PreconditionFailed")
				return
			}
			if f.embeddedError {
				f.embeddedError = false
				fixtureError(w, 200, "InternalError")
				return
			}
			b, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			if s3Hash(string(b)) != r.Header.Get("X-Amz-Content-Sha256") {
				fixtureError(w, 400, "BadDigest")
				return
			}
			var in struct {
				Parts []s3Part `xml:"Part"`
			}
			if xml.Unmarshal(b, &in) != nil || len(in.Parts) != len(u.parts) {
				fixtureError(w, 400, "InvalidPart")
				return
			}
			out, e := os.CreateTemp(f.dir, "object-")
			if e != nil {
				fixtureError(w, 500, "DiskError")
				return
			}
			h := sha256.New()
			var size int64
			for i, p := range in.Parts {
				stored, ok := u.parts[p.Number]
				if !ok || p.Number != i+1 || p.ETag != stored.part.ETag || p.SHA != stored.part.SHA || (i < len(in.Parts)-1 && stored.part.Size < 5<<20) {
					out.Close()
					fixtureError(w, 400, "InvalidPart")
					return
				}
				part, e := os.Open(stored.path)
				if e != nil {
					out.Close()
					fixtureError(w, 500, "DiskError")
					return
				}
				n, e := io.Copy(io.MultiWriter(out, h), part)
				part.Close()
				size += n
				if e != nil {
					out.Close()
					fixtureError(w, 500, "DiskError")
					return
				}
			}
			out.Close()
			sha := hex.EncodeToString(h.Sum(nil))
			if sha != u.sha {
				fixtureError(w, 400, "BadDigest")
				return
			}
			f.objects[key] = multipartFixtureObject{out.Name(), BlobInfo{key, size, sha}}
			delete(f.uploads, id)
			if f.loseComplete {
				f.loseComplete = false
				f.headErrors = 1
				fixtureLost(w)
				return
			}
			fixtureXML(w, struct {
				XMLName   xml.Name `xml:"CompleteMultipartUploadResult"`
				Key, ETag string
			}{Key: key, ETag: `"final-opaque-etag"`})
		case "DELETE":
			f.aborts++
			if f.abortLeavesPart {
				f.abortLeavesPart = false
				w.WriteHeader(204)
				return
			}
			delete(f.uploads, id)
			w.WriteHeader(204)
		default:
			fixtureError(w, 405, "MethodNotAllowed")
		}
		return
	}
	obj, ok := f.objects[key]
	if r.Method == "HEAD" && f.headErrors > 0 {
		f.headErrors--
		w.WriteHeader(503)
		return
	}
	switch r.Method {
	case "GET", "HEAD":
		if !ok {
			w.WriteHeader(404)
			return
		}
		file, e := os.Open(obj.path)
		if e != nil {
			w.WriteHeader(500)
			return
		}
		defer file.Close()
		w.Header().Set("X-Amz-Meta-Sha256", obj.info.SHA256)
		http.ServeContent(w, r, "object", time.Unix(1, 0), file)
	case "PUT":
		if ok {
			w.WriteHeader(412)
			return
		}
		file, e := os.CreateTemp(f.dir, "small-")
		if e != nil {
			w.WriteHeader(500)
			return
		}
		info, e := hashBlobStream(r.Context(), key, io.TeeReader(r.Body, file))
		file.Close()
		if e != nil || info.SHA256 != r.Header.Get("X-Amz-Content-Sha256") {
			w.WriteHeader(400)
			return
		}
		f.objects[key] = multipartFixtureObject{file.Name(), info}
	case "DELETE":
		delete(f.objects, key)
		w.WriteHeader(204)
	default:
		w.WriteHeader(405)
	}
}

func multipartSource(t *testing.T, size int64) (string, BlobInfo) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "source")
	f, e := os.Create(path)
	if e != nil {
		t.Fatal(e)
	}
	if e = f.Truncate(size); e != nil {
		t.Fatal(e)
	}
	if _, e = f.WriteAt([]byte("actual-tail"), size-11); e != nil {
		t.Fatal(e)
	}
	f.Close()
	info, e := localBlobInfo(context.Background(), blobKey("multipart-tenant", "project", mediaID()), path)
	if e != nil {
		t.Fatal(e)
	}
	return path, info
}
func multipartPut(t *testing.T, s *S3BlobStore, path string, info BlobInfo) error {
	t.Helper()
	f, e := os.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	_, e = s.PutStream(context.Background(), info, f)
	return e
}

func TestS3PublishedSignatureVectorsAndCanonicalQueries(t *testing.T) {
	// Public synthetic AWS documentation examples, never used with the network.
	s := &S3BlobStore{config: S3BlobConfig{AccessKey: "AKIAIOSFODNN7EXAMPLE", SecretKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", Region: "us-east-1"}}
	for _, tc := range []struct{ path, rangeHeader, signature string }{{"/test.txt", "bytes=0-9", "f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41"}, {"/?lifecycle", "", "fea454ca298b7da1c68078a5d1bdbfbbe0d65c699e0f91ac7a200a0136783543"}, {"/?prefix=J&max-keys=2", "", "34b48302e7b5fa45bde8084f4b7868a86f0a534bc59db6670ed5711ef69dc6f7"}} {
		r, _ := http.NewRequest("GET", "https://examplebucket.s3.amazonaws.com"+tc.path, nil)
		if tc.rangeHeader != "" {
			r.Header.Set("Range", tc.rangeHeader)
		}
		s.signPayload(r, s3Hash(""), time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC))
		if !strings.HasSuffix(r.Header.Get("Authorization"), "Signature="+tc.signature) {
			t.Fatal("published SigV4 vector differs", r.Header.Get("Authorization"))
		}
	}
	if got := s3CanonicalQuery(url.Values{"uploadId": {"a +/="}, "uploads": {""}, "z": {"a b", "a+b"}, "é": {"x"}}); got != "%C3%A9=x&uploadId=a%20%2B%2F%3D&uploads=&z=a%20b&z=a%2Bb" {
		t.Fatal(got)
	}
}

func TestS3MultipartResumesAcknowledgedPartsAcrossStoreRestart(t *testing.T) {
	for _, fault := range []string{"part-rejection", "lost-part-ack", "lost-create-ack", "embedded-complete-error", "lost-complete-ack"} {
		t.Run(fault, func(t *testing.T) {
			fixture := newMultipartFixture(t)
			dir := t.TempDir()
			s := fixture.store(t, dir)
			path, info := multipartSource(t, 2*s3MultipartThreshold+123)
			switch fault {
			case "part-rejection":
				fixture.failPart = 2
			case "lost-part-ack":
				fixture.losePart = 1
			case "lost-create-ack":
				fixture.loseCreate = true
			case "embedded-complete-error":
				fixture.embeddedError = true
			case "lost-complete-ack":
				fixture.loseComplete = true
			}
			if e := multipartPut(t, s, path, info); e == nil {
				t.Fatal("injected failure acknowledged as success")
			}
			fixture.mu.Lock()
			before := append([]int(nil), fixture.puts...)
			fixture.mu.Unlock()
			s = fixture.store(t, dir)
			if e := multipartPut(t, s, path, info); e != nil {
				t.Fatal("checkpoint recovery", e)
			}
			if e := verifyBlobStream(context.Background(), s, info); e != nil {
				t.Fatal(e)
			}
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			if fixture.starts != 1 {
				t.Fatal("duplicate multipart initiation", fixture.starts)
			}
			if len(before) > 0 && (fault == "part-rejection" || fault == "lost-part-ack") {
				for _, n := range fixture.puts[len(before):] {
					if n == 1 {
						t.Fatal("resent acknowledged first part", fixture.puts)
					}
				}
			}
			if fault == "lost-complete-ack" && fixture.completes != 1 {
				t.Fatal("repeated accepted completion", fixture.completes)
			}
			for _, entry := range func() []os.DirEntry { e, _ := os.ReadDir(dir); return e }() {
				if strings.HasSuffix(entry.Name(), ".json") {
					t.Fatal("completed checkpoint retained")
				}
			}
		})
	}
}

func TestS3MultipartWrongChecksumAndExistingObjectFailClosed(t *testing.T) {
	f := newMultipartFixture(t)
	s := f.store(t, t.TempDir())
	path, info := multipartSource(t, s3MultipartThreshold+200)
	f.failPart = 2
	if e := multipartPut(t, s, path, info); e == nil {
		t.Fatal("failure injection missed")
	}
	f.corruptList = true
	if e := multipartPut(t, s, path, info); !errors.Is(e, ErrBlobIntegrity) {
		t.Fatal("trusted unverified remote part", e)
	}
	f.corruptList = false
	if e := multipartPut(t, s, path, info); e != nil {
		t.Fatal(e)
	}
	wrong := info
	wrong.SHA256 = strings.Repeat("a", 64)
	if e := s.putMultipart(context.Background(), wrong, func() *os.File { file, _ := os.Open(path); t.Cleanup(func() { file.Close() }); return file }()); !errors.Is(e, ErrBlobIntegrity) {
		t.Fatal("existing object can be replaced", e)
	}
	if e := multipartPut(t, s, path, info); e != nil {
		t.Fatal("rejected mutation poisoned accepted replay", e)
	}
}

func TestS3MultipartJournalPostgresCrossAPIResume(t *testing.T) {
	db := postgresTestDB(t)
	f := newMultipartFixture(t)
	s := f.store(t, t.TempDir())
	if e := s.configureMultipart("", db); e != nil {
		t.Fatal(e)
	}
	path, info := multipartSource(t, s3MultipartThreshold+10)
	f.failPart = 2
	if e := multipartPut(t, s, path, info); e == nil {
		t.Fatal("failure injection missed")
	}
	other := f.store(t, t.TempDir())
	if e := other.configureMultipart("", db); e != nil {
		t.Fatal(e)
	}
	if e := multipartPut(t, other, path, info); e != nil {
		t.Fatal("another API cannot resume", e)
	}
	f.mu.Lock()
	if f.starts != 1 {
		t.Error("another API started duplicate transfer")
	}
	f.mu.Unlock()
}

func TestS3MultipartConcurrentIdenticalSubmissionsPublishOneObject(t *testing.T) {
	for _, mode := range []string{"file", "postgres"} {
		t.Run(mode, func(t *testing.T) {
			f := newMultipartFixture(t)
			dir := t.TempDir()
			a, b := f.store(t, dir), f.store(t, dir)
			if mode == "postgres" {
				db := postgresTestDB(t)
				if e := a.configureMultipart("", db); e != nil {
					t.Fatal(e)
				}
				if e := b.configureMultipart("", db); e != nil {
					t.Fatal(e)
				}
			}
			path, info := multipartSource(t, s3MultipartThreshold+10)
			done := make(chan error, 2)
			for _, s := range []*S3BlobStore{a, b} {
				go func(s *S3BlobStore) { done <- multipartPut(t, s, path, info) }(s)
			}
			for range 2 {
				if e := <-done; e != nil {
					t.Fatal(e)
				}
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.starts != 1 || f.completes != 1 {
				t.Fatal("concurrent duplicate transfer", f.starts, f.completes)
			}
		})
	}
}

func TestS3MultipartCleanupRetriesWithoutDeletingAcceptedObjects(t *testing.T) {
	for _, mode := range []string{"file", "postgres"} {
		t.Run(mode, func(t *testing.T) {
			f := newMultipartFixture(t)
			s := f.store(t, t.TempDir())
			if mode == "postgres" {
				if e := s.configureMultipart("", postgresTestDB(t)); e != nil {
					t.Fatal(e)
				}
			}
			path, info := multipartSource(t, s3MultipartThreshold+10)
			f.failPart = 2
			if e := multipartPut(t, s, path, info); e == nil {
				t.Fatal("missed injected failure")
			}
			// A successfully accepted object must survive abort of a separate,
			// abandoned transfer with the same key.
			f.mu.Lock()
			f.objects[info.Key] = multipartFixtureObject{path, info}
			f.abortLeavesPart = true
			f.mu.Unlock()
			if n, e := s.CleanupMultipart(context.Background(), time.Now().Add(time.Hour), 100); n != 0 || e == nil {
				t.Fatal("unverified abort treated as clean", n, e)
			}
			// Failed cleanup updates the timestamp. Its aborting marker must still
			// be retried, without waiting another seven days.
			if n, e := s.CleanupMultipart(context.Background(), time.Now().Add(-7*24*time.Hour), 100); n != 1 || e != nil {
				t.Fatal("cleanup retry", n, e)
			}
			if e := verifyBlobStream(context.Background(), s, info); e != nil {
				t.Fatal("cleanup damaged accepted object", e)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.aborts != 2 || len(f.uploads) != 0 {
				t.Fatal("parts leaked", f.aborts, len(f.uploads))
			}
		})
	}
}

func TestS3MultipartCleanupUnknownInitiationPreservesForeignUploads(t *testing.T) {
	for _, ambiguous := range []bool{false, true} {
		t.Run(fmt.Sprint(ambiguous), func(t *testing.T) {
			f := newMultipartFixture(t)
			s := f.store(t, t.TempDir())
			path, info := multipartSource(t, s3MultipartThreshold+5)
			f.uploads["preexisting foreign"] = &multipartFixtureUpload{key: info.Key, parts: map[int]multipartFixturePart{}}
			f.loseCreate = true
			if e := multipartPut(t, s, path, info); e == nil {
				t.Fatal("lost initiation was acknowledged")
			}
			if ambiguous {
				f.mu.Lock()
				f.uploads["unknown second writer"] = &multipartFixtureUpload{key: info.Key, parts: map[int]multipartFixturePart{}}
				f.mu.Unlock()
			}
			n, e := s.CleanupMultipart(context.Background(), time.Now().Add(time.Hour), 100)
			f.mu.Lock()
			defer f.mu.Unlock()
			if ambiguous {
				if n != 0 || !errors.Is(e, errS3Ambiguous) || f.aborts != 0 {
					t.Fatal("ambiguous uploads aborted", n, e, f.aborts)
				}
			} else if n != 1 || e != nil || f.aborts != 1 {
				t.Fatal("unknown initiation cleanup", n, e, f.aborts)
			}
			if _, ok := f.uploads["preexisting foreign"]; !ok {
				t.Fatal("foreign upload deleted")
			}
		})
	}
}

func TestS3MultipartCleanupSkipsActiveWriterAndOtherScopes(t *testing.T) {
	for _, mode := range []string{"file", "postgres"} {
		t.Run(mode, func(t *testing.T) {
			f := newMultipartFixture(t)
			s := f.store(t, t.TempDir())
			if mode == "postgres" {
				if e := s.configureMultipart("", postgresTestDB(t)); e != nil {
					t.Fatal(e)
				}
			}
			_, info := multipartSource(t, s3MultipartThreshold+2)
			v := s3MultipartState{Version: 1, Scope: s.multipartScope(), Info: info, PartBytes: s3MultipartThreshold, Stage: "new", Updated: time.Now()}
			ready, release := make(chan struct{}), make(chan struct{})
			done := make(chan error, 1)
			go func() {
				done <- s.multipart.with(context.Background(), v, func(_ context.Context, c *s3Checkpoint) error {
					if e := c.save(v); e != nil {
						close(ready)
						return e
					}
					close(ready)
					<-release
					return errMultipartKeep
				})
			}()
			<-ready
			n, e := s.CleanupMultipart(context.Background(), time.Now().Add(time.Hour), 100)
			close(release)
			ownedErr := <-done
			if n != 0 || e != nil || !errors.Is(ownedErr, errMultipartKeep) {
				t.Fatal("active writer collected", n, e, ownedErr)
			}
			config := s.config
			config.Bucket = "other"
			other, err := NewS3BlobStore(config)
			if err != nil {
				t.Fatal(err)
			}
			other.multipart = s.multipart
			if n, e := other.CleanupMultipart(context.Background(), time.Now().Add(time.Hour), 100); n != 0 || e != nil {
				t.Fatal("foreign scope collected", n, e)
			}
			if n, e := s.CleanupMultipart(context.Background(), time.Now().Add(time.Hour), 100); n != 1 || e != nil {
				t.Fatal("released checkpoint not collected", n, e)
			}
		})
	}
}

func TestS3MultipartSQLLeaseFencesOldWriterAndSchemaUpgrade(t *testing.T) {
	db := postgresTestDB(t)
	ctx := context.Background()
	f := newMultipartFixture(t)
	s := f.store(t, t.TempDir())
	if e := s.configureMultipart("", db); e != nil {
		t.Fatal(e)
	}
	_, info := multipartSource(t, s3MultipartThreshold)
	v := s3MultipartState{Version: 1, Scope: s.multipartScope(), Info: info, PartBytes: s3MultipartThreshold, Stage: "new", Updated: time.Now()}
	e := s.multipart.with(ctx, v, func(_ context.Context, c *s3Checkpoint) error {
		if _, e := db.Exec(`UPDATE studio_blob_multipart SET lease_until=now()-interval '1 second'`); e != nil {
			return e
		}
		// A second process claims an expired row through the actual repository.
		// The old writer may neither checkpoint nor delete its successor's row.
		other := &s3MultipartJournal{db: db}
		e := other.with(ctx, v, func(_ context.Context, current *s3Checkpoint) error {
			if e := c.save(c.state); !errors.Is(e, errMultipartFence) {
				t.Errorf("old checkpoint accepted after takeover: %v", e)
			}
			next := current.state
			next.Stage = "aborting"
			if e := current.save(next); e != nil {
				return e
			}
			return errMultipartKeep
		})
		if !errors.Is(e, errMultipartKeep) {
			return e
		}
		return nil
	})
	if !errors.Is(e, errMultipartFence) {
		t.Fatal("stale writer accepted", e)
	}
	var owner string
	var stage string
	if e := db.QueryRow(`SELECT lease_owner,payload->>'stage' FROM studio_blob_multipart`).Scan(&owner, &stage); e != nil || owner != "" || stage != "aborting" {
		t.Fatal("old release changed new lease", owner, e)
	}
	if _, e := db.Exec(`DROP TABLE studio_blob_multipart`); e != nil {
		t.Fatal(e)
	}
	if e := CheckPostgres(ctx, db); e == nil {
		t.Fatal("old schema reported ready")
	}
	if e := InitPostgres(ctx, db); e != nil {
		t.Fatal(e)
	}
	if e := CheckPostgres(ctx, db); e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	if e := os.MkdirAll(filepath.Join(dir, "s3-multipart"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := writeS3State(filepath.Join(dir, "s3-multipart"), s3StateName(v.Scope, v.Info.Key), v); e != nil {
		t.Fatal(e)
	}
	if cached, e := OpenPostgresStore(dir, db); e == nil {
		cached.Close()
		t.Fatal("local S3 progress silently ignored by PG startup")
	}
}

func TestS3MultipartFilePostgresMigrationResumesRemoteParts(t *testing.T) {
	db := postgresTestDB(t)
	ctx := context.Background()
	f := newMultipartFixture(t)
	dir := t.TempDir()
	s := f.store(t, dir)
	path, info := multipartSource(t, s3MultipartThreshold+10)
	f.failPart = 2
	if e := multipartPut(t, s, path, info); e == nil {
		t.Fatal("missed failure")
	}
	name := s3StateName(s.multipartScope(), info.Key) + ".json"
	before, e := os.ReadFile(filepath.Join(dir, name))
	if e != nil {
		t.Fatal(e)
	}
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	if n, e := importS3Multipart(ctx, tx, dir); e != nil || n != 1 {
		t.Fatal("import", n, e)
	}
	if e := tx.Commit(); e != nil {
		t.Fatal(e)
	}
	after, e := os.ReadFile(filepath.Join(dir, name))
	if e != nil || string(before) != string(after) {
		t.Fatal("import rewrote source", e)
	}
	out := t.TempDir()
	tx, e = db.BeginTx(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	if n, e := exportS3Multipart(ctx, tx, out); e != nil || n != 1 {
		t.Fatal("export", n, e)
	}
	if e := tx.Commit(); e != nil {
		t.Fatal(e)
	}
	after, e = os.ReadFile(filepath.Join(out, name))
	if e != nil || string(before) != string(after) {
		t.Fatal("roundtrip changed checkpoint", e)
	}
	if e := multipartPut(t, f.store(t, out), path, info); e != nil {
		t.Fatal("resume migrated checkpoint", e)
	}
	if f.starts != 1 {
		t.Fatal("migration started duplicate transfer", f.starts)
	}
}
