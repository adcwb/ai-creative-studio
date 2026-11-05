package application

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

const s3MultipartThreshold int64 = 16 << 20
const s3MaxPartBytes int64 = 5 << 30

var errS3UploadMissing = errors.New("S3 multipart upload no longer exists")
var errS3Ambiguous = errors.New("S3 multipart initiation outcome ambiguous; do not start another upload")

type s3Part struct {
	Number int    `xml:"PartNumber"`
	Size   int64  `xml:"Size,omitempty"`
	ETag   string `xml:"ETag"`
	SHA    string `xml:"ChecksumSHA256"`
}

func s3CanonicalQuery(v url.Values) string {
	entries := []string{}
	encode := func(s string) string { return strings.ReplaceAll(url.QueryEscape(s), "+", "%20") }
	for key, values := range v {
		for _, value := range values {
			entries = append(entries, encode(key)+"="+encode(value))
		}
	}
	sort.Strings(entries)
	return strings.Join(entries, "&")
}

func (s *S3BlobStore) multipartRequest(ctx context.Context, method, key string, q url.Values, headers map[string]string, body io.Reader, size int64, sha string) (*http.Response, error) {
	if key != "" {
		if e := checkBlobKey(key); e != nil {
			return nil, e
		}
	}
	u := *s.endpoint
	u.Path = "/" + s.config.Bucket
	if key != "" {
		u.Path += "/" + key
	}
	u.RawQuery = s3CanonicalQuery(q)
	r, e := http.NewRequestWithContext(ctx, method, u.String(), body)
	if e != nil {
		return nil, e
	}
	r.ContentLength = size
	r.GetBody = nil
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	s.signPayload(r, sha, time.Now())
	out, e := s.client.Do(r)
	if e != nil {
		return nil, errors.New("S3 multipart request failed; outcome may be unknown")
	}
	return out, nil
}
func s3XML(r *http.Response, out any) error {
	defer closeBlobResponse(r)
	b, e := io.ReadAll(io.LimitReader(r.Body, (8<<20)+1))
	if e != nil || len(b) > 8<<20 {
		return errors.New("S3 multipart response incomplete")
	}
	if xml.Unmarshal(b, out) != nil {
		return errors.New("S3 multipart response invalid")
	}
	return nil
}
func (s *S3BlobStore) listMultipart(ctx context.Context, key string) ([]string, error) {
	q := url.Values{"uploads": {""}, "prefix": {key}, "max-uploads": {"1000"}}
	out := []string{}
	seen := map[string]bool{}
	bytes := 0
	for {
		r, e := s.multipartRequest(ctx, "GET", "", q, nil, nil, 0, s3Hash(""))
		if e != nil {
			return nil, e
		}
		if r.StatusCode != 200 {
			closeBlobResponse(r)
			return nil, errors.New("S3 multipart inventory unavailable")
		}
		var page struct {
			XMLName   xml.Name
			Bucket    string
			Truncated bool                             `xml:"IsTruncated"`
			NextKey   string                           `xml:"NextKeyMarker"`
			NextID    string                           `xml:"NextUploadIdMarker"`
			Uploads   []struct{ Key, UploadId string } `xml:"Upload"`
		}
		if e = s3XML(r, &page); e != nil {
			return nil, e
		}
		if page.XMLName.Local != "ListMultipartUploadsResult" || page.Bucket != s.config.Bucket {
			return nil, ErrBlobIntegrity
		}
		for _, u := range page.Uploads {
			if u.Key == key {
				if u.UploadId == "" || len(u.UploadId) > 4096 || seen[u.UploadId] {
					return nil, ErrBlobIntegrity
				}
				seen[u.UploadId] = true
				out = append(out, u.UploadId)
				bytes += len(u.UploadId) + 4
				if bytes > 1<<20 {
					return nil, errMultipartState // bounded control journal, no new transfer started
				}
			}
		}
		if !page.Truncated {
			break
		}
		if page.NextKey == "" || (page.NextKey == q.Get("key-marker") && page.NextID == q.Get("upload-id-marker")) {
			return nil, ErrBlobIntegrity
		}
		q.Set("key-marker", page.NextKey)
		q.Set("upload-id-marker", page.NextID)
	}
	sort.Strings(out)
	return out, nil
}
func (s *S3BlobStore) startMultipart(ctx context.Context, c *s3Checkpoint) error {
	v := c.state
	if v.Stage == "initiating" {
		current, e := s.listMultipart(ctx, v.Info.Key)
		if e != nil {
			return e
		}
		before := map[string]bool{}
		for _, id := range v.Before {
			before[id] = true
		}
		fresh := []string{}
		for _, id := range current {
			if !before[id] {
				fresh = append(fresh, id)
			}
		}
		if len(fresh) > 1 {
			return errS3Ambiguous
		}
		if len(fresh) == 1 {
			v.UploadID = fresh[0]
			v.Stage = "uploading"
			v.Before = nil
			return c.save(v)
		}
	}
	before, e := s.listMultipart(ctx, v.Info.Key)
	if e != nil {
		return e
	}
	v.Stage = "initiating"
	v.Before = before
	if e = c.save(v); e != nil {
		return e
	}
	r, e := s.multipartRequest(ctx, "POST", v.Info.Key, url.Values{"uploads": {""}}, map[string]string{"x-amz-meta-sha256": v.Info.SHA256, "x-amz-checksum-algorithm": "SHA256"}, nil, 0, s3Hash(""))
	if e != nil {
		return e
	}
	if r.StatusCode != 200 {
		closeBlobResponse(r)
		return errors.New("S3 multipart initiation rejected")
	}
	var reply struct {
		XMLName               xml.Name
		Bucket, Key, UploadId string
	}
	if e = s3XML(r, &reply); e != nil {
		return e
	}
	if reply.XMLName.Local != "InitiateMultipartUploadResult" || reply.Bucket != s.config.Bucket || reply.Key != v.Info.Key || reply.UploadId == "" || len(reply.UploadId) > 4096 {
		return ErrBlobIntegrity
	}
	v.UploadID = reply.UploadId
	v.Stage = "uploading"
	v.Before = nil
	return c.save(v)
}
func (s *S3BlobStore) listParts(ctx context.Context, v s3MultipartState) (map[int]s3Part, error) {
	q := url.Values{"uploadId": {v.UploadID}, "max-parts": {"1000"}}
	out := map[int]s3Part{}
	for {
		r, e := s.multipartRequest(ctx, "GET", v.Info.Key, q, nil, nil, 0, s3Hash(""))
		if e != nil {
			return nil, e
		}
		if r.StatusCode != 200 {
			var fault struct{ Code string }
			e = s3XML(r, &fault)
			if r.StatusCode == 404 && e == nil && fault.Code == "NoSuchUpload" {
				return nil, errS3UploadMissing
			}
			return nil, errors.New("S3 multipart parts unavailable")
		}
		var page struct {
			XMLName       xml.Name
			Key, UploadId string
			Truncated     bool     `xml:"IsTruncated"`
			Next          int      `xml:"NextPartNumberMarker"`
			Parts         []s3Part `xml:"Part"`
		}
		if e = s3XML(r, &page); e != nil {
			return nil, e
		}
		if page.XMLName.Local != "ListPartsResult" || page.Key != v.Info.Key || page.UploadId != v.UploadID {
			return nil, ErrBlobIntegrity
		}
		for _, p := range page.Parts {
			if p.Number < 1 || p.Number > 10000 || p.Size < 1 || p.Size > s3MaxPartBytes || p.ETag == "" || len(p.ETag) > 512 || p.SHA == "" || out[p.Number].Number != 0 {
				return nil, ErrBlobIntegrity
			}
			out[p.Number] = p
		}
		if !page.Truncated {
			break
		}
		old, _ := strconv.Atoi(q.Get("part-number-marker"))
		if page.Next <= old || page.Next > 10000 {
			return nil, ErrBlobIntegrity
		}
		q.Set("part-number-marker", strconv.Itoa(page.Next))
	}
	return out, nil
}
func (s *S3BlobStore) verifyMultipartObject(ctx context.Context, info BlobInfo) (bool, error) {
	actual, e := s.Stat(ctx, info.Key)
	if errors.Is(e, ErrBlobNotFound) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	if actual != info {
		return false, ErrBlobIntegrity
	}
	if e = verifyBlobStream(ctx, s, info); e != nil {
		return false, e
	}
	return true, nil
}
func (s *S3BlobStore) putMultipart(ctx context.Context, info BlobInfo, f *os.File) error {
	if s.multipart == nil {
		return errors.New("S3 multipart requires a persistent checkpoint store")
	}
	needed := (info.Size-1)/10000 + 1
	partBytes := max(s3MultipartThreshold, ((needed-1)/(1<<20)+1)*(1<<20))
	if partBytes > s3MaxPartBytes {
		return errors.New("object exceeds the S3 multipart capability")
	}
	// Reject an existing different object before creating a new journal entry.
	// A rejected mutation must not poison later replay of the accepted object.
	if old, e := s.Stat(ctx, info.Key); e == nil && old != info {
		return ErrBlobIntegrity
	} else if e != nil && !errors.Is(e, ErrBlobNotFound) {
		return e
	}
	scope := s.multipartScope()
	initial := s3MultipartState{Version: 1, Scope: scope, Info: info, PartBytes: partBytes, Stage: "new", Updated: time.Now().UTC()}
	return s.multipart.with(ctx, initial, func(ctx context.Context, c *s3Checkpoint) error {
		// A prior completion can succeed even when its response was lost. Verify
		// the actual immutable bytes before acknowledging that accepted object.
		if found, e := s.verifyMultipartObject(ctx, info); e != nil {
			return e
		} else if found {
			if c.state.UploadID != "" {
				if e = s.abortMultipart(ctx, c.state); e != nil {
					return e
				}
			}
			return nil
		}
		if c.state.Stage == "aborting" {
			return errors.New("S3 multipart cancellation pending; cleanup before retry")
		}
		if c.state.UploadID == "" {
			if e := s.startMultipart(ctx, c); e != nil {
				return e
			}
		}
		parts, e := s.listParts(ctx, c.state)
		if errors.Is(e, errS3UploadMissing) {
			v := c.state
			v.UploadID = ""
			v.Stage = "new"
			v.Before = nil
			if e = c.save(v); e != nil {
				return e
			}
			if e = s.startMultipart(ctx, c); e != nil {
				return e
			}
			parts, e = s.listParts(ctx, c.state)
		}
		if e != nil {
			return e
		}
		expected := int((info.Size + c.state.PartBytes - 1) / c.state.PartBytes)
		for n := range parts {
			if n > expected {
				return ErrBlobIntegrity
			}
		}
		ordered := make([]s3Part, 0, expected)
		for number, offset := 1, int64(0); offset < info.Size; number, offset = number+1, offset+c.state.PartBytes {
			size := min(c.state.PartBytes, info.Size-offset)
			hash := sha256.New()
			if _, e = io.CopyBuffer(hash, contextBlobReader{ctx, io.NewSectionReader(f, offset, size)}, make([]byte, 64<<10)); e != nil {
				return e
			}
			sum := hash.Sum(nil)
			encoded := base64.StdEncoding.EncodeToString(sum)
			if old, ok := parts[number]; ok {
				if old.Size != size || old.SHA != encoded {
					return ErrBlobIntegrity
				}
				ordered = append(ordered, old)
				continue
			}
			if e = c.save(c.state); e != nil {
				return e
			}
			r, e := s.multipartRequest(ctx, "PUT", info.Key, url.Values{"uploadId": {c.state.UploadID}, "partNumber": {strconv.Itoa(number)}}, map[string]string{"x-amz-checksum-sha256": encoded}, io.NewSectionReader(f, offset, size), size, hex.EncodeToString(sum))
			if e != nil {
				return e
			}
			if r.StatusCode != 200 {
				closeBlobResponse(r)
				return errors.New("S3 multipart part rejected; resume the same checkpoint")
			}
			p := s3Part{Number: number, Size: size, ETag: r.Header.Get("ETag"), SHA: r.Header.Get("x-amz-checksum-sha256")}
			closeBlobResponse(r)
			if p.ETag == "" || len(p.ETag) > 512 || p.SHA != encoded {
				return ErrBlobIntegrity
			}
			ordered = append(ordered, p)
		}
		v := c.state
		v.Stage = "completing"
		if e = c.save(v); e != nil {
			return e
		}
		// Size belongs to ListParts, not CompleteMultipartUpload's XML schema.
		for i := range ordered {
			ordered[i].Size = 0
		}
		body, e := xml.Marshal(struct {
			XMLName xml.Name `xml:"CompleteMultipartUpload"`
			Parts   []s3Part `xml:"Part"`
		}{Parts: ordered})
		if e != nil {
			return e
		}
		r, e := s.multipartRequest(ctx, "POST", info.Key, url.Values{"uploadId": {v.UploadID}}, map[string]string{"If-None-Match": "*", "Content-Type": "application/xml"}, strings.NewReader(string(body)), int64(len(body)), s3Hash(string(body)))
		if e != nil {
			if found, check := s.verifyMultipartObject(ctx, info); check == nil && found {
				return nil
			}
			return e
		}
		if r.StatusCode != 200 {
			status := r.StatusCode
			closeBlobResponse(r)
			if status == 412 || status == 409 || status == 404 {
				if found, e := s.verifyMultipartObject(ctx, info); e != nil {
					return e
				} else if found {
					return s.abortMultipart(ctx, v)
				}
			}
			return errors.New("S3 multipart completion unconfirmed; retain checkpoint")
		}
		var reply struct {
			XMLName         xml.Name
			Key, ETag, Code string
		}
		if e = s3XML(r, &reply); e != nil {
			return e
		}
		if reply.XMLName.Local != "CompleteMultipartUploadResult" || reply.Code != "" || reply.Key != info.Key || reply.ETag == "" {
			return errors.New("S3 multipart completion reported an error; retain checkpoint")
		}
		actual, e := s.Stat(ctx, info.Key)
		if e != nil || actual != info {
			return ErrBlobIntegrity
		}
		return nil
	})
}
func (s *S3BlobStore) abortMultipart(ctx context.Context, v s3MultipartState) error {
	if v.UploadID == "" {
		return nil
	}
	r, e := s.multipartRequest(ctx, "DELETE", v.Info.Key, url.Values{"uploadId": {v.UploadID}}, nil, nil, 0, s3Hash(""))
	if e != nil {
		return e
	}
	status := r.StatusCode
	closeBlobResponse(r)
	if status != 204 && status != 200 && status != 404 {
		return errors.New("S3 multipart abort unconfirmed")
	}
	parts, e := s.listParts(ctx, v)
	if errors.Is(e, errS3UploadMissing) {
		return nil
	}
	if e != nil {
		return e
	}
	if len(parts) != 0 {
		return errors.New("S3 multipart parts remain after abort")
	}
	return nil
}
