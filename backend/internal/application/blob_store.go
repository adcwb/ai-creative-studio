package application

// BlobStore is an internal storage boundary. Callers must establish tenant and
// resource ownership before deriving a key; keys and credentials are never URLs.
import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const MaxBlobBytes int64 = 128 << 20

var ErrBlobNotFound = errors.New("blob not found")
var ErrBlobIntegrity = errors.New("blob integrity check failed")
var blobKeyPattern = regexp.MustCompile(`^[a-f0-9]{64}/[a-f0-9]{64}/[a-f0-9]{32}(\.(png|mp4|srt|vtt))?$`)

type BlobInfo struct {
	Key    string `json:"key"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}
type BlobStore interface {
	Put(context.Context, string, []byte) (BlobInfo, error)
	Stat(context.Context, string) (BlobInfo, error)
	OpenRange(context.Context, string, int64, int64) (io.ReadCloser, error)
	Delete(context.Context, string) error
}

func blobInfo(key string, data []byte) BlobInfo {
	h := sha256.Sum256(data)
	return BlobInfo{key, int64(len(data)), hex.EncodeToString(h[:])}
}
func blobKey(space, project, name string) string {
	return s3Hash(space) + "/" + s3Hash(project) + "/" + name
}
func validBlobInfo(info BlobInfo) bool {
	h, e := hex.DecodeString(info.SHA256)
	return blobKeyPattern.MatchString(info.Key) && info.Size >= 0 && info.Size <= MaxBlobBytes && e == nil && len(h) == 32 && strings.ToLower(info.SHA256) == info.SHA256
}
func checkBlobKey(key string) error {
	if !blobKeyPattern.MatchString(key) {
		return errors.New("invalid blob key")
	}
	return nil
}
func readVerifiedBlob(ctx context.Context, store BlobStore, info BlobInfo) ([]byte, error) {
	if !validBlobInfo(info) {
		return nil, ErrBlobIntegrity
	}
	r, e := store.OpenRange(ctx, info.Key, 0, -1)
	if e != nil {
		return nil, e
	}
	defer r.Close()
	b, e := io.ReadAll(io.LimitReader(r, info.Size+1))
	if e != nil {
		return nil, e
	}
	if blobInfo(info.Key, b) != info {
		return nil, ErrBlobIntegrity
	}
	return b, nil
}

type FileBlobStore struct{ root *os.Root }

func NewFileBlobStore(dir string) (*FileBlobStore, error) {
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	r, e := os.OpenRoot(dir)
	if e != nil {
		return nil, e
	}
	return &FileBlobStore{r}, nil
}
func (s *FileBlobStore) Close() error { return s.root.Close() }
func (s *FileBlobStore) Put(ctx context.Context, key string, b []byte) (BlobInfo, error) {
	if e := ctx.Err(); e != nil {
		return BlobInfo{}, e
	}
	if e := checkBlobKey(key); e != nil {
		return BlobInfo{}, e
	}
	if int64(len(b)) > MaxBlobBytes {
		return BlobInfo{}, errMediaLimit
	}
	info := blobInfo(key, b)
	if e := s.root.MkdirAll(filepath.Dir(key), 0700); e != nil {
		return BlobInfo{}, e
	}
	// Link publishes only the fully fsynced file, without replacing existing data.
	tmp := filepath.Dir(key) + "/.blob-" + mediaID()
	f, e := s.root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return BlobInfo{}, e
	}
	defer s.root.Remove(tmp)
	_, e = f.Write(b)
	if e == nil {
		e = f.Sync()
	}
	e = errors.Join(e, f.Close())
	if e != nil {
		return BlobInfo{}, e
	}
	if e = s.root.Link(tmp, key); e != nil {
		if !errors.Is(e, os.ErrExist) {
			return BlobInfo{}, e
		}
		old, err := s.Stat(ctx, key)
		if err != nil || old != info {
			return BlobInfo{}, ErrBlobIntegrity
		}
	}
	dir, e := s.root.Open(filepath.Dir(key))
	if e != nil {
		return BlobInfo{}, e
	}
	e = dir.Sync()
	dir.Close()
	return info, e
}
func (s *FileBlobStore) open(key string) (*os.File, error) {
	if e := checkBlobKey(key); e != nil {
		return nil, e
	}
	f, e := s.root.OpenFile(key, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if errors.Is(e, os.ErrNotExist) {
		return nil, ErrBlobNotFound
	}
	if e != nil {
		return nil, e
	}
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() || st.Size() > MaxBlobBytes {
		f.Close()
		return nil, ErrBlobIntegrity
	}
	return f, nil
}
func (s *FileBlobStore) Stat(ctx context.Context, key string) (BlobInfo, error) {
	if e := ctx.Err(); e != nil {
		return BlobInfo{}, e
	}
	f, e := s.open(key)
	if e != nil {
		return BlobInfo{}, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, MaxBlobBytes+1))
	if e != nil {
		return BlobInfo{}, e
	}
	return blobInfo(key, b), nil
}

type blobReadCloser struct {
	io.Reader
	io.Closer
}

func (s *FileBlobStore) OpenRange(ctx context.Context, key string, start, length int64) (io.ReadCloser, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	f, e := s.open(key)
	if e != nil {
		return nil, e
	}
	st, e := f.Stat()
	if e != nil || start < 0 || start > st.Size() || length < -1 || length > st.Size()-start {
		f.Close()
		return nil, errors.New("invalid blob range")
	}
	if length < 0 {
		length = st.Size() - start
	}
	return &blobReadCloser{io.NewSectionReader(f, start, length), f}, nil
}
func (s *FileBlobStore) Delete(ctx context.Context, key string) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if e := checkBlobKey(key); e != nil {
		return e
	}
	e := s.root.Remove(key)
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	return e
}

type S3BlobConfig struct {
	Endpoint, Bucket, Region, AccessKey, SecretKey, SessionToken string
	AllowLoopbackHTTP                                            bool
}
type S3BlobStore struct {
	config   S3BlobConfig
	endpoint *url.URL
	client   *http.Client
}

// Endpoint is deployment-only, not tenant-editable. HTTP is restricted to a
// literal loopback address for local integration. Proxy and redirects are off.
func NewS3BlobStore(c S3BlobConfig) (*S3BlobStore, error) {
	u, e := url.Parse(c.Endpoint)
	if e != nil || u.User != nil || u.Hostname() == "" || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || !s3BucketPattern.MatchString(c.Bucket) || strings.Contains(c.Bucket, "..") || !s3RegionPattern.MatchString(c.Region) || !validCredential(c.AccessKey) || !validCredential(c.SecretKey) {
		return nil, errors.New("invalid S3 blob configuration")
	}
	if u.Scheme != "https" {
		ip := net.ParseIP(u.Hostname())
		if u.Scheme != "http" || !c.AllowLoopbackHTTP || ip == nil || !ip.IsLoopback() {
			return nil, errors.New("S3 blobs require HTTPS")
		}
	}
	u.Path = ""
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	tr.DisableCompression = true
	return &S3BlobStore{c, u, &http.Client{Transport: tr, Timeout: 60 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (s *S3BlobStore) request(ctx context.Context, method, key string, b []byte, headers map[string]string) (*http.Response, error) {
	if e := checkBlobKey(key); e != nil {
		return nil, e
	}
	u := *s.endpoint
	u.Path = "/" + s.config.Bucket + "/" + key
	req, e := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(b))
	if e != nil {
		return nil, errors.New("S3 request failed")
	}
	req.GetBody = nil
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	s.sign(req, b, time.Now())
	res, e := s.client.Do(req)
	if e != nil {
		return nil, errors.New("S3 request failed; outcome may be unknown")
	}
	return res, nil
}
func closeBlobResponse(r *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(r.Body, 4096))
	r.Body.Close()
}
func (s *S3BlobStore) Put(ctx context.Context, key string, b []byte) (BlobInfo, error) {
	if int64(len(b)) > MaxBlobBytes {
		return BlobInfo{}, errMediaLimit
	}
	info := blobInfo(key, b)
	r, e := s.request(ctx, "PUT", key, b, map[string]string{"If-None-Match": "*", "x-amz-meta-sha256": info.SHA256})
	if e != nil {
		return BlobInfo{}, e
	}
	defer closeBlobResponse(r)
	if r.StatusCode == 412 {
		old, e := s.Stat(ctx, key)
		if e != nil || old != info {
			return BlobInfo{}, ErrBlobIntegrity
		}
		if _, e = readVerifiedBlob(ctx, s, info); e != nil {
			return BlobInfo{}, e
		}
		return info, nil
	}
	if r.StatusCode != 200 && r.StatusCode != 204 {
		return BlobInfo{}, fmt.Errorf("S3 put failed (%d)", r.StatusCode)
	}
	return info, nil
}
func (s *S3BlobStore) Stat(ctx context.Context, key string) (BlobInfo, error) {
	r, e := s.request(ctx, "HEAD", key, nil, nil)
	if e != nil {
		return BlobInfo{}, e
	}
	defer closeBlobResponse(r)
	if r.StatusCode == 404 {
		return BlobInfo{}, ErrBlobNotFound
	}
	if r.StatusCode != 200 {
		return BlobInfo{}, fmt.Errorf("S3 stat failed (%d)", r.StatusCode)
	}
	n, e := strconv.ParseInt(r.Header.Get("Content-Length"), 10, 64)
	info := BlobInfo{key, n, r.Header.Get("x-amz-meta-sha256")}
	if e != nil || !validBlobInfo(info) {
		return BlobInfo{}, ErrBlobIntegrity
	}
	return info, nil
}
func (s *S3BlobStore) OpenRange(ctx context.Context, key string, start, length int64) (io.ReadCloser, error) {
	if e := checkBlobKey(key); e != nil {
		return nil, e
	}
	if start < 0 || start > MaxBlobBytes || length < -1 || length > MaxBlobBytes-start {
		return nil, errors.New("invalid blob range")
	}
	h := map[string]string{}
	partial := start != 0 || length >= 0
	if length == 0 {
		info, e := s.Stat(ctx, key)
		if e != nil {
			return nil, e
		}
		if start > info.Size {
			return nil, errors.New("invalid blob range")
		}
		return io.NopCloser(bytes.NewReader(nil)), nil
	}
	if partial {
		end := ""
		if length >= 0 {
			end = strconv.FormatInt(start+length-1, 10)
		}
		h["Range"] = fmt.Sprintf("bytes=%d-%s", start, end)
	}
	r, e := s.request(ctx, "GET", key, nil, h)
	if e != nil {
		return nil, e
	}
	if r.StatusCode == 404 {
		closeBlobResponse(r)
		return nil, ErrBlobNotFound
	}
	if (!partial && r.StatusCode != 200) || (partial && r.StatusCode != 206) {
		closeBlobResponse(r)
		return nil, fmt.Errorf("S3 get failed (%d)", r.StatusCode)
	}
	if r.ContentLength > MaxBlobBytes {
		closeBlobResponse(r)
		return nil, ErrBlobIntegrity
	}
	if partial {
		value := r.Header.Get("Content-Range")
		parts := regexp.MustCompile(`^bytes ([0-9]+)-([0-9]+)/([0-9]+)$`).FindStringSubmatch(value)
		if len(parts) != 4 {
			closeBlobResponse(r)
			return nil, ErrBlobIntegrity
		}
		first, e1 := strconv.ParseInt(parts[1], 10, 64)
		last, e2 := strconv.ParseInt(parts[2], 10, 64)
		total, e3 := strconv.ParseInt(parts[3], 10, 64)
		if e1 != nil || e2 != nil || e3 != nil || first != start || last < first || total <= last || total > MaxBlobBytes || r.ContentLength != last-first+1 || (length >= 0 && r.ContentLength != length) {
			closeBlobResponse(r)
			return nil, ErrBlobIntegrity
		}
	}
	return r.Body, nil
}
func (s *S3BlobStore) Delete(ctx context.Context, key string) error {
	r, e := s.request(ctx, "DELETE", key, nil, nil)
	if e != nil {
		return e
	}
	defer closeBlobResponse(r)
	if r.StatusCode != 204 && r.StatusCode != 200 && r.StatusCode != 404 {
		return fmt.Errorf("S3 delete failed (%d)", r.StatusCode)
	}
	return nil
}

// Sign all supplied headers, including conditions, checksums and metadata.
func (s *S3BlobStore) sign(req *http.Request, b []byte, now time.Time) {
	date, stamp := now.UTC().Format("20060102"), now.UTC().Format("20060102T150405Z")
	payload := s3Hash(string(b))
	req.Header.Set("X-Amz-Content-Sha256", payload)
	req.Header.Set("X-Amz-Date", stamp)
	if s.config.SessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", s.config.SessionToken)
	}
	values := map[string]string{"host": req.URL.Host}
	for k, v := range req.Header {
		values[strings.ToLower(k)] = strings.Join(strings.Fields(strings.Join(v, ",")), " ")
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var canonicalHeaders strings.Builder
	for _, k := range keys {
		canonicalHeaders.WriteString(k + ":" + values[k] + "\n")
	}
	signed := strings.Join(keys, ";")
	scope := date + "/" + s.config.Region + "/s3/aws4_request"
	canonical := req.Method + "\n" + req.URL.EscapedPath() + "\n\n" + canonicalHeaders.String() + "\n" + signed + "\n" + payload
	signing := s3HMAC(s3HMAC(s3HMAC(s3HMAC([]byte("AWS4"+s.config.SecretKey), date), s.config.Region), "s3"), "aws4_request")
	signature := hex.EncodeToString(s3HMAC(signing, "AWS4-HMAC-SHA256\n"+stamp+"\n"+scope+"\n"+s3Hash(canonical)))
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+s.config.AccessKey+"/"+scope+", SignedHeaders="+signed+", Signature="+signature)
}
