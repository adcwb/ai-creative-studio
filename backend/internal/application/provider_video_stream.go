package application

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"time"
)

func guardRuntimeDownloadTo(lifetime context.Context, download func(context.Context, string, io.Writer) error, guard func(context.Context) error, onDispatch func()) func(context.Context, string, io.Writer) error {
	if download == nil {
		return nil
	}
	return func(ctx context.Context, url string, out io.Writer) error {
		if e := lifetime.Err(); e != nil {
			return e
		}
		if e := ctx.Err(); e != nil {
			return e
		}
		if e := guard(ctx); e != nil {
			return e
		}
		ctx, cancel := context.WithCancel(ctx)
		stop := context.AfterFunc(lifetime, cancel)
		defer stop()
		defer cancel()
		if onDispatch != nil {
			onDispatch()
		}
		return download(ctx, url, out)
	}
}
func safeVideoDownloadTo(ctx context.Context, raw string, out io.Writer) error {
	return safeMediaDownloadStream(ctx, raw, out, "video/mp4")
}
func safeImageDownloadTo(ctx context.Context, raw string, out io.Writer) error {
	return safeMediaDownloadStream(ctx, raw, out, "")
}
func safeMediaDownloadStream(ctx context.Context, raw string, out io.Writer, mime string) error {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	u, ip, e := downloadDestination(ctx, raw, net.DefaultResolver.LookupNetIP)
	if e != nil {
		return e
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	transport := pinnedDownloadTransport(ip, dialer.DialContext)
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 90 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, e := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if e != nil {
		return providerFailure("unsafe_media_url")
	}
	response, e := libraryGuardedRequest(client, req)
	if e != nil {
		return providerFailure("media_download_failed")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return providerFailure("invalid_provider_response")
	}
	if mime != "" {
		return copyProviderMedia(ctx, response.Body, out, mime)
	}
	_, e = io.CopyBuffer(out, contextBlobReader{ctx, response.Body}, make([]byte, 64<<10))
	return e
}
func downloadVideoResult(ctx context.Context, url string, stream func(context.Context, string, io.Writer) error, legacy func(context.Context, string) ([]byte, error), out io.Writer) error {
	if stream != nil {
		return stream(ctx, url, out)
	}
	if legacy == nil {
		return providerFailure("provider_unavailable")
	}
	// Only injected legacy adapters use this compatibility route; all public
	// constructors and runtime clients install the guarded streaming downloader.
	b, e := legacy(ctx, url)
	if e != nil {
		return e
	}
	return copyProviderMedia(ctx, bytes.NewReader(b), out, "video/mp4")
}
func (p *ArkVideoProvider) DownloadVideoTo(ctx context.Context, id string, out io.Writer) error {
	v, e := p.get(ctx, id)
	if e != nil {
		return e
	}
	if v.Status != "succeeded" || v.Content.VideoURL == "" {
		return providerFailure("provider_rejected")
	}
	return downloadVideoResult(ctx, v.Content.VideoURL, p.downloadTo, p.download, out)
}
func (p *MiniMaxVideoProvider) DownloadVideoTo(ctx context.Context, id string, out io.Writer) error {
	v, e := p.get(ctx, id)
	if e != nil {
		return e
	}
	if v.Status != "succeeded" || v.Content == nil || v.Content.URL == "" {
		return providerFailure("provider_rejected")
	}
	return downloadVideoResult(ctx, v.Content.URL, p.downloadTo, p.download, out)
}
func (p *GoogleMediaProvider) DownloadVideoTo(ctx context.Context, id string, out io.Writer) error {
	job, e := p.PollVideo(ctx, id)
	if e != nil {
		return e
	}
	if job.Status != "completed" {
		return providerFailure("provider_rejected")
	}
	res, e := p.request(ctx, "GET", "/v1beta/files/"+id+":download?alt=media", nil)
	if e != nil {
		return e
	}
	defer res.Body.Close()
	switch res.StatusCode {
	case 301, 302, 303, 307, 308:
		res.Body.Close()
		return downloadVideoResult(ctx, res.Header.Get("Location"), p.downloadTo, p.download, out)
	}
	if res.StatusCode != 200 {
		return providerFailure("invalid_provider_response")
	}
	return copyProviderMedia(ctx, res.Body, out, "video/mp4")
}
