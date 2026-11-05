package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// StreamingBlobStore handles complete media independently of inline request
// buffers. Info comes from hashing the source, never from an untrusted header.
type StreamingBlobStore interface {
	BlobStore
	PutStream(context.Context, BlobInfo, io.Reader) (BlobInfo, error)
}

type contextBlobReader struct {
	ctx context.Context
	io.Reader
}

func (r contextBlobReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(b)
}

func hashBlobStream(ctx context.Context, key string, r io.Reader) (BlobInfo, error) {
	h := sha256.New()
	n, err := io.CopyBuffer(h, contextBlobReader{ctx, r}, make([]byte, 64<<10))
	return BlobInfo{Key: key, Size: n, SHA256: hex.EncodeToString(h.Sum(nil))}, err
}

func copyBlobBytes(ctx context.Context, w io.Writer, r io.Reader, info BlobInfo) error {
	if !validBlobInfo(info) {
		return ErrBlobIntegrity
	}
	h := sha256.New()
	n, err := io.CopyBuffer(io.MultiWriter(w, h), contextBlobReader{ctx, io.LimitReader(r, info.Size+1)}, make([]byte, 64<<10))
	if err != nil {
		return err
	}
	if n != info.Size || hex.EncodeToString(h.Sum(nil)) != info.SHA256 {
		return ErrBlobIntegrity
	}
	return nil
}

func verifyBlobStream(ctx context.Context, store BlobStore, info BlobInfo) error {
	r, err := store.OpenRange(ctx, info.Key, 0, -1)
	if err != nil {
		return err
	}
	defer r.Close()
	return copyBlobBytes(ctx, io.Discard, r, info)
}

func copyBlobStream(ctx context.Context, from, to BlobStore, info BlobInfo) error {
	if target, ok := to.(StreamingBlobStore); ok {
		r, err := from.OpenRange(ctx, info.Key, 0, -1)
		if err != nil {
			return err
		}
		defer r.Close()
		actual, err := target.PutStream(ctx, info, r)
		if err == nil && actual != info {
			return ErrBlobIntegrity
		}
		return err
	}
	b, err := readVerifiedBlob(ctx, from, info)
	if err != nil {
		return err
	}
	actual, err := to.Put(ctx, info.Key, b)
	if err == nil && actual != info {
		return ErrBlobIntegrity
	}
	return err
}

func (s *FileBlobStore) PutStream(ctx context.Context, info BlobInfo, r io.Reader) (BlobInfo, error) {
	if !validBlobInfo(info) {
		return BlobInfo{}, ErrBlobIntegrity
	}
	if err := ctx.Err(); err != nil {
		return BlobInfo{}, err
	}
	if err := s.root.MkdirAll(filepath.Dir(info.Key), 0700); err != nil {
		return BlobInfo{}, err
	}
	tmp := filepath.Dir(info.Key) + "/.blob-" + mediaID()
	f, err := s.root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return BlobInfo{}, err
	}
	defer s.root.Remove(tmp)
	err = copyBlobBytes(ctx, f, r, info)
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return BlobInfo{}, err
	}
	if err = s.root.Link(tmp, info.Key); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return BlobInfo{}, err
		}
		old, e := s.Stat(ctx, info.Key)
		if e != nil || old != info {
			return BlobInfo{}, ErrBlobIntegrity
		}
	}
	dir, err := s.root.Open(filepath.Dir(info.Key))
	if err != nil {
		return BlobInfo{}, err
	}
	defer dir.Close()
	return info, dir.Sync()
}

func (s *S3BlobStore) PutStream(ctx context.Context, info BlobInfo, r io.Reader) (BlobInfo, error) {
	if !validBlobInfo(info) {
		return BlobInfo{}, ErrBlobIntegrity
	}
	// Spool and verify with bounded memory before signing. A private immutable
	// source also permits request-body hashing without buffering a whole video.
	f, err := os.CreateTemp("", "studio-blob-upload-")
	if err != nil {
		return BlobInfo{}, err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err = copyBlobBytes(ctx, f, r, info); err != nil {
		return BlobInfo{}, err
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return BlobInfo{}, err
	}
	u := *s.endpoint
	u.Path = "/" + s.config.Bucket + "/" + info.Key
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u.String(), f)
	if err != nil {
		return BlobInfo{}, err
	}
	req.ContentLength = info.Size
	req.GetBody = nil
	req.Header.Set("If-None-Match", "*")
	req.Header.Set("x-amz-meta-sha256", info.SHA256)
	s.signPayload(req, info.SHA256, time.Now())
	response, err := s.client.Do(req)
	if err != nil {
		return BlobInfo{}, errors.New("S3 upload failed; outcome may be unknown")
	}
	defer closeBlobResponse(response)
	if response.StatusCode == http.StatusPreconditionFailed {
		old, e := s.Stat(ctx, info.Key)
		if e != nil || old != info {
			return BlobInfo{}, ErrBlobIntegrity
		}
		return info, verifyBlobStream(ctx, s, info)
	}
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusNoContent {
		return BlobInfo{}, errors.New("S3 upload rejected")
	}
	return info, nil
}

func openLocalBlob(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		f.Close()
		return nil, ErrBlobIntegrity
	}
	return f, nil
}

func localBlobInfo(ctx context.Context, key, path string) (BlobInfo, error) {
	f, err := openLocalBlob(path)
	if err != nil {
		return BlobInfo{}, err
	}
	defer f.Close()
	return hashBlobStream(ctx, key, f)
}

func (m *managedBlobs) putFile(ctx context.Context, key, path string) error {
	f, err := openLocalBlob(path)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := hashBlobStream(ctx, key, f)
	if err != nil {
		return err
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if target, ok := m.store.(StreamingBlobStore); ok {
		actual, e := target.PutStream(ctx, info, f)
		if e != nil {
			return e
		}
		if actual != info {
			return ErrBlobIntegrity
		}
	} else {
		if info.Size > MaxBlobBytes {
			return errors.New("blob backend does not support streaming media")
		}
		data, e := io.ReadAll(f)
		if e != nil {
			return e
		}
		actual, e := m.store.Put(ctx, key, data)
		if e != nil {
			return e
		}
		if actual != info {
			return ErrBlobIntegrity
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if old, ok := m.entries[key]; ok && old != info {
		return ErrBlobIntegrity
	}
	if m.db != nil {
		return insertBlobInfo(ctx, m.db, info)
	}
	m.entries[key] = info
	return m.commit()
}

// byte-backed callers keep their existing bounded inline contract.
var _ StreamingBlobStore = (*FileBlobStore)(nil)
var _ StreamingBlobStore = (*S3BlobStore)(nil)
