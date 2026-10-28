package application

// The S3 bridge stores only a random-key copy of a validated reference image.
// It is not the application's general media backend. Credentials stay on the
// server, and URLs must never be logged or stored in public job responses.
import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

type ReferenceObjectBridge interface {
	ReferenceURL(context.Context, []byte, string) (string, error)
}
type S3ReferenceConfig struct {
	Endpoint, Bucket, Region, AccessKey, SecretKey, SessionToken string
	Authorized                                                   bool
}
type S3ReferenceBridge struct {
	config   S3ReferenceConfig
	endpoint *url.URL
	now      func() time.Time
	client   *http.Client
}

var s3BucketPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)
var s3RegionPattern = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)

func NewS3ReferenceBridge(config S3ReferenceConfig) (*S3ReferenceBridge, error) {
	endpoint, err := url.Parse(config.Endpoint)
	if !config.Authorized || err != nil || endpoint.Scheme != "https" || endpoint.User != nil || endpoint.Hostname() == "" || (endpoint.Port() != "" && endpoint.Port() != "443") || endpoint.Path != "" && endpoint.Path != "/" || endpoint.RawQuery != "" || endpoint.Fragment != "" || !s3BucketPattern.MatchString(config.Bucket) || strings.Contains(config.Bucket, "..") || !s3RegionPattern.MatchString(config.Region) || !validCredential(config.AccessKey) || !validCredential(config.SecretKey) {
		return nil, errors.New("reference bridge requires explicit authorization and valid HTTPS S3 configuration")
	}
	endpoint.Path = ""
	return &S3ReferenceBridge{config: config, endpoint: endpoint, now: time.Now}, nil
}

// ConfigureReferenceBridgeFromEnv performs no network call or credential probe.
// Enabled means sending copies to the configured object service is authorized.
func ConfigureReferenceBridgeFromEnv() (ReferenceObjectBridge, error) {
	if os.Getenv("REFERENCE_BRIDGE_ENABLED") != "true" {
		return nil, nil
	}
	return NewS3ReferenceBridge(S3ReferenceConfig{Endpoint: os.Getenv("REFERENCE_S3_ENDPOINT"), Bucket: os.Getenv("REFERENCE_S3_BUCKET"), Region: os.Getenv("REFERENCE_S3_REGION"), AccessKey: os.Getenv("REFERENCE_S3_ACCESS_KEY"), SecretKey: os.Getenv("REFERENCE_S3_SECRET_KEY"), SessionToken: os.Getenv("REFERENCE_S3_SESSION_TOKEN"), Authorized: true})
}
func s3HMAC(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	_, _ = h.Write([]byte(data))
	return h.Sum(nil)
}
func s3Hash(data string) string {
	sum := sha256.Sum256([]byte(data))
	return hex.EncodeToString(sum[:])
}
func s3Query(query url.Values) string { return strings.ReplaceAll(query.Encode(), "+", "%20") }
func s3Presign(config S3ReferenceConfig, target *url.URL, method string, now time.Time, ttl int) string {
	date, stamp := now.UTC().Format("20060102"), now.UTC().Format("20060102T150405Z")
	scope := date + "/" + config.Region + "/s3/aws4_request"
	query := url.Values{"X-Amz-Algorithm": {"AWS4-HMAC-SHA256"}, "X-Amz-Credential": {config.AccessKey + "/" + scope}, "X-Amz-Date": {stamp}, "X-Amz-Expires": {fmt.Sprint(ttl)}, "X-Amz-SignedHeaders": {"host"}}
	if config.SessionToken != "" {
		query.Set("X-Amz-Security-Token", config.SessionToken)
	}
	canonical := method + "\n" + target.EscapedPath() + "\n" + s3Query(query) + "\nhost:" + target.Host + "\n\nhost\nUNSIGNED-PAYLOAD"
	signing := s3HMAC(s3HMAC(s3HMAC(s3HMAC([]byte("AWS4"+config.SecretKey), date), config.Region), "s3"), "aws4_request")
	signature := hex.EncodeToString(s3HMAC(signing, "AWS4-HMAC-SHA256\n"+stamp+"\n"+scope+"\n"+s3Hash(canonical)))
	query.Set("X-Amz-Signature", signature)
	copy := *target
	copy.RawQuery = s3Query(query)
	return copy.String()
}
func (b *S3ReferenceBridge) ReferenceURL(ctx context.Context, data []byte, mime string) (string, error) {
	normalized, err := nativeImage(data, mime)
	if err != nil {
		return "", providerFailure("invalid_reference_image")
	}
	var key [24]byte
	if _, err = rand.Read(key[:]); err != nil {
		return "", providerFailure("reference_bridge_failed")
	}
	target := *b.endpoint
	target.Path = "/" + b.config.Bucket + "/references/" + hex.EncodeToString(key[:]) + ".png"
	client := b.client
	if client == nil {
		_, ip, err := downloadDestination(ctx, target.String(), net.DefaultResolver.LookupNetIP)
		if err != nil {
			return "", providerFailure("unsafe_reference_bridge")
		}
		dialer := &net.Dialer{Timeout: 10 * time.Second}
		transport := pinnedDownloadTransport(ip, dialer.DialContext)
		defer transport.CloseIdleConnections()
		client = &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	now := b.now()
	signed := s3Presign(b.config, &target, http.MethodPut, now, 60)
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, signed, bytes.NewReader(normalized.Data))
	if err != nil {
		return "", providerFailure("reference_bridge_failed")
	}
	request.GetBody = nil
	request.Header.Set("Content-Type", "image/png")
	response, err := client.Do(request)
	if err != nil {
		return "", providerFailure("reference_bridge_failed")
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusNoContent {
		return "", providerFailure("reference_bridge_failed")
	}
	return s3Presign(b.config, &target, http.MethodGet, b.now(), 300), nil
}
