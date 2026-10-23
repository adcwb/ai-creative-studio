package studio

import (
	"context"
	"time"
)

type AsyncImageJob struct {
	ID     string
	Status string
}
type AsyncImageProvider interface {
	SubmitImage(context.Context, Input) (AsyncImageJob, error)
	PollImage(context.Context, string) (AsyncImageJob, error)
	DownloadImage(context.Context, string) (GenerationResult, error)
}

// Query operations may themselves use HTTP POST in a vendor protocol, but they
// never invoke the vendor's billable generation-submission action.
func awaitAsyncImage(ctx context.Context, p AsyncImageProvider, id string, interval time.Duration) (GenerationResult, error) {
	for {
		job, err := p.PollImage(ctx, id)
		if err != nil {
			return GenerationResult{}, err
		}
		if job.ID != id {
			return GenerationResult{}, providerFailure("invalid_provider_response")
		}
		switch job.Status {
		case "succeeded":
			return p.DownloadImage(ctx, id)
		case "failed":
			return GenerationResult{}, providerFailure("provider_rejected")
		case "queued", "running":
		default:
			return GenerationResult{}, providerFailure("invalid_provider_response")
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return GenerationResult{}, ctx.Err()
		case <-timer.C:
		}
	}
}
