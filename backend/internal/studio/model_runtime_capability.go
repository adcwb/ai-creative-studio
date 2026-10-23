package studio

import "context"

// CreativeProvider predates capability-specific contracts. Restrict its broad
// method set so a text budget permit cannot be reused for speech or video.
type runtimeCreative struct {
	p          CreativeProvider
	capability string
}

func (p runtimeCreative) Analyze(ctx context.Context, in ProjectInput) (BriefAnalysis, error) {
	if p.capability != "text" {
		return BriefAnalysis{}, errRuntimeDenied
	}
	return p.p.Analyze(ctx, in)
}
func (p runtimeCreative) Script(ctx context.Context, in ProjectInput) (Script, error) {
	if p.capability != "text" {
		return Script{}, errRuntimeDenied
	}
	return p.p.Script(ctx, in)
}
func (p runtimeCreative) Storyboard(ctx context.Context, in Script) ([]StoryboardShot, error) {
	if p.capability != "text" {
		return nil, errRuntimeDenied
	}
	return p.p.Storyboard(ctx, in)
}
func (p runtimeCreative) Speech(ctx context.Context, in string) ([]byte, error) {
	if p.capability != "speech" {
		return nil, errRuntimeDenied
	}
	return p.p.Speech(ctx, in)
}
func (p runtimeCreative) SubmitVideo(ctx context.Context, in string) (VideoJob, error) {
	if p.capability != "video" {
		return VideoJob{}, errRuntimeDenied
	}
	return p.p.SubmitVideo(ctx, in)
}
func (p runtimeCreative) PollVideo(ctx context.Context, in string) (VideoJob, error) {
	if p.capability != "video" || (executionReadOnly(ctx) && (in == "" || in != executionRecoveryUpstream(ctx))) {
		return VideoJob{}, errRuntimeDenied
	}
	return p.p.PollVideo(ctx, in)
}
func (p runtimeCreative) DownloadVideo(ctx context.Context, in string) ([]byte, error) {
	if p.capability != "video" || (executionReadOnly(ctx) && (in == "" || in != executionRecoveryUpstream(ctx))) {
		return nil, errRuntimeDenied
	}
	return p.p.DownloadVideo(ctx, in)
}
func (p runtimeCreative) SubmitVideoWithImage(ctx context.Context, prompt string, data []byte, mime string) (VideoJob, error) {
	if v, ok := p.p.(referenceVideoProvider); ok && p.capability == "video" {
		return v.SubmitVideoWithImage(ctx, prompt, data, mime)
	}
	return VideoJob{}, errRuntimeDenied
}
func (p runtimeCreative) SubmitVideoWithVideo(ctx context.Context, prompt string, data []byte, mime string) (VideoJob, error) {
	if v, ok := p.p.(editVideoProvider); ok && p.capability == "video" {
		return v.SubmitVideoWithVideo(ctx, prompt, data, mime)
	}
	return VideoJob{}, errRuntimeDenied
}
