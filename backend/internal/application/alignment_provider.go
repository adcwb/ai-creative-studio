package application

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"strings"
	"time"
)

type AlignmentResult struct {
	Transcript  string
	Cues        []SubtitleCue
	TimingBasis string
}
type AlignmentProvider interface {
	Transcribe(context.Context, []byte, string, float64) (AlignmentResult, error)
}
type OpenAIAlignmentProvider struct {
	gateway              bool
	plainTranscript      bool
	key, model, endpoint string
	client               *http.Client
}

func NewOpenAIAlignmentProvider(key, model string) *OpenAIAlignmentProvider {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil
	return newOpenAIAlignmentProvider(key, model, "https://api.openai.com/v1/audio/transcriptions", &http.Client{Transport: t, Timeout: 120 * time.Second})
}
func newOpenAIAlignmentProvider(key, model, endpoint string, client *http.Client) *OpenAIAlignmentProvider {
	c := *client
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &OpenAIAlignmentProvider{key: key, model: model, endpoint: endpoint, client: &c}
}
func ConfigureAlignmentFromEnv() (AlignmentProvider, Capabilities) {
	name, model := os.Getenv("ASR_PROVIDER"), os.Getenv("ASR_MODEL")
	caps := Capabilities{Provider: name, Mode: "real", Model: model, Message: "真实转写未验证效果和费用；只支持已确认的verbose_json时间戳协议。"}
	if name == "" {
		caps.Provider = "unavailable"
		caps.Mode = "unavailable"
		caps.Message = "未配置ASR；可按文本估算并人工校准，不会识别音频。"
		return nil, caps
	}
	if name != "openai-asr" {
		caps.Provider = "unavailable"
		caps.Mode = "unavailable"
		caps.ReasonCode = "unsupported_provider"
		return nil, caps
	}
	// Official docs: whisper-1 supports verbose_json + segment/word timestamps.
	// gpt-4o-transcribe/mini JSON and diarized protocols are not interchangeable.
	if model != "whisper-1" {
		caps.ReasonCode = "unsupported_timestamp_model"
		caps.Message = "请显式配置ASR_MODEL=whisper-1；本适配器不假定其他模型支持verbose_json时间戳。"
		return nil, caps
	}
	key := os.Getenv("OPENAI_API_KEY")
	if !validCredential(key) {
		caps.ReasonCode = "missing_api_key"
		return nil, caps
	}
	caps.Ready = true
	return NewOpenAIAlignmentProvider(key, model), caps
}
func (p *OpenAIAlignmentProvider) Transcribe(ctx context.Context, audio []byte, mime string, duration float64) (AlignmentResult, error) {
	return p.transcribeReference(ctx, referenceMedia{Data: audio, MIME: mime}, duration)
}
func (p *OpenAIAlignmentProvider) TranscribeFile(ctx context.Context, path, mime string, duration float64) (AlignmentResult, error) {
	return p.transcribeReference(ctx, referenceMedia{Path: path, MIME: mime}, duration)
}
func (p *OpenAIAlignmentProvider) transcribeReference(ctx context.Context, reference referenceMedia, duration float64) (AlignmentResult, error) {
	mime := reference.MIME

	fail := func() (AlignmentResult, error) {
		return AlignmentResult{}, providerFailure("invalid_provider_response")
	}
	f, size, e := referenceOpen(ctx, reference)
	if e != nil {
		return AlignmentResult{}, e
	}
	f.Close()
	if (!p.gateway && p.model != "whisper-1" && p.model != "gpt-transcribe") || size <= 0 || duration <= 0 || math.IsNaN(duration) || math.IsInf(duration, 0) || (mime != "audio/wav" && mime != "audio/mpeg") {
		return AlignmentResult{}, providerFailure("provider_rejected")
	}
	// This applies only to the direct OpenAI file API, never arbitrary gateways.
	if !p.gateway && size > 25_000_000 {
		return AlignmentResult{}, providerFailure("provider_inline_capacity")
	}
	fields := [][2]string{{"model", p.model}, {"response_format", "verbose_json"}, {"timestamp_granularities[]", "segment"}, {"timestamp_granularities[]", "word"}}
	if (!p.gateway && p.model == "gpt-transcribe") || p.plainTranscript {

		fields = [][2]string{{"model", p.model}, {"response_format", "json"}}
	}
	ext := "wav"
	if mime == "audio/mpeg" {
		ext = "mp3"
	}
	response, err := alignmentMultipartResponse(ctx, p, fields, reference, "audio."+ext)
	if err != nil {
		var failure *ProviderError
		if errors.As(err, &failure) && (failure.Code == "library_use_denied" || failure.Code == "alignment_source_unpinned" || failure.Code == "alignment_source_changed") {
			return AlignmentResult{}, err
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return AlignmentResult{}, providerFailure("provider_timeout")
		}
		return AlignmentResult{}, providerFailure("provider_error")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		code := "provider_error"
		switch response.StatusCode {
		case 401, 403:
			code = "provider_auth_failed"
		case 429:
			code = "provider_rate_limited"
		case 400, 404, 422:
			code = "provider_rejected"
		}
		return AlignmentResult{}, providerFailure(code)
	}
	b, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(b) > 1<<20 {
		return fail()
	}
	if (!p.gateway && p.model == "gpt-transcribe") || p.plainTranscript {
		var out struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(b, &out) != nil || !validTranscript(out.Text) {
			return fail()
		}
		return AlignmentResult{Transcript: strings.TrimSpace(out.Text), Cues: []SubtitleCue{}, TimingBasis: "transcript-only"}, nil
	}
	var out struct {
		Segments []struct {
			Start *float64 `json:"start"`
			End   *float64 `json:"end"`
			Text  *string  `json:"text"`
		} `json:"segments"`
		Words []struct {
			Word  string   `json:"word"`
			Start *float64 `json:"start"`
			End   *float64 `json:"end"`
		} `json:"words"`
	}
	if json.Unmarshal(b, &out) != nil {
		return fail()
	}
	cues := []SubtitleCue{}
	for _, segment := range out.Segments {
		if segment.Start == nil || segment.End == nil || segment.Text == nil {
			return fail()
		}
		cues = append(cues, SubtitleCue{Start: *segment.Start, End: *segment.End, Text: *segment.Text})
	}
	basis := "asr-segments"
	if len(cues) == 0 && len(out.Words) > 0 {
		basis = "asr-words"
		var current SubtitleCue
		for _, rawWord := range out.Words {
			if rawWord.Start == nil || rawWord.End == nil {
				return fail()
			}
			word := struct {
				Word       string
				Start, End float64
			}{rawWord.Word, *rawWord.Start, *rawWord.End}
			if word.End <= word.Start || word.Start < 0 || math.IsNaN(word.Start) || math.IsNaN(word.End) || strings.TrimSpace(word.Word) == "" {
				return fail()
			}
			if current.Text != "" && (word.Start-current.End > 0.7 || len([]rune(current.Text+word.Word)) > 200) {
				cues = append(cues, current)
				current = SubtitleCue{}
			}
			if current.Text == "" {
				current.Start = word.Start
			} else if word.Start < current.End {
				return fail()
			}
			current.End = word.End
			if current.Text != "" && !strings.HasPrefix(word.Word, " ") {
				current.Text += " "
			}
			current.Text += word.Word
			if strings.ContainsAny(word.Word, "。！？.!?；;") {
				cues = append(cues, current)
				current = SubtitleCue{}
			}
		}
		if current.Text != "" {
			cues = append(cues, current)
		}
	}
	if len(cues) == 0 {
		return fail()
	}
	// Use a small tolerance for upstream decimal rounding; never invent timing.
	for i := range cues {
		cues[i].Text = strings.TrimSpace(cues[i].Text)
		if cues[i].End > duration && cues[i].End <= duration+0.05 {
			cues[i].End = duration
		}
	}
	if len(cues) > 1000 {
		return fail()
	}
	previous := 0.0
	for _, cue := range cues {
		if cue.Start < previous || validateSubtitleCues([]SubtitleCue{cue}, duration) != nil {
			return fail()
		}
		previous = cue.End
	}
	return AlignmentResult{Cues: cues, TimingBasis: basis}, nil
}

func validTranscript(text string) bool {
	return strings.TrimSpace(text) != "" && len([]rune(text)) <= 2000
}
func validAlignmentModeModel(mode, model string) bool {
	return (mode == "transcription" && model == "gpt-transcribe") || (mode == "asr" && model == "whisper-1")
}
func validRuntimeAlignmentMode(mode, model string, selection *RuntimeSelection) bool {
	if selection != nil && selection.GatewayID != "" {
		return mode == "transcription" && selection.FeatureKey == "speech_transcription" || mode == "asr" && selection.FeatureKey == "subtitle_alignment"
	}
	return validAlignmentModeModel(mode, model)
}

type fileAlignmentProvider interface {
	TranscribeFile(context.Context, string, string, float64) (AlignmentResult, error)
}

func alignmentMultipartResponse(ctx context.Context, p *OpenAIAlignmentProvider, fields [][2]string, reference referenceMedia, filename string) (*http.Response, error) {
	reader, writer := io.Pipe()
	form := multipart.NewWriter(writer)
	done := make(chan error, 1)
	go func() {
		e := func() error {
			for _, field := range fields {
				if e := form.WriteField(field[0], field[1]); e != nil {
					return e
				}
			}
			header := textproto.MIMEHeader{}
			header.Set("Content-Disposition", `form-data; name="file"; filename="`+filename+`"`)
			header.Set("Content-Type", reference.MIME)
			part, e := form.CreatePart(header)
			if e != nil {
				return e
			}
			f, _, e := referenceOpen(ctx, reference)
			if e != nil {
				return e
			}
			_, e = io.CopyBuffer(part, contextBlobReader{ctx, f}, make([]byte, 64<<10))
			e = errors.Join(e, f.Close())
			if e != nil {
				return e
			}
			return form.Close()
		}()
		writer.CloseWithError(e)
		done <- e
	}()
	req, e := http.NewRequestWithContext(ctx, "POST", p.endpoint, reader)
	if e != nil {
		reader.Close()
		<-done
		return nil, e
	}
	req.GetBody = nil
	req.Header.Set("Authorization", "Bearer "+p.key)
	req.Header.Set("Content-Type", form.FormDataContentType())
	res, e := libraryGuardedRequest(p.client, req)
	reader.Close()
	writeErr := <-done
	if e != nil || (writeErr != nil && res.StatusCode >= 200 && res.StatusCode < 300) {
		if res != nil {
			res.Body.Close()
		}
		if e != nil {
			return nil, e
		}
		return nil, providerFailure("provider_error")
	}
	return res, nil
}
func runAlignmentFile(ctx context.Context, p AlignmentProvider, task AlignmentTask, path, mime string) (AlignmentResult, error) {
	if task.Mode != "forced" {
		if file, ok := p.(fileAlignmentProvider); ok {
			return file.TranscribeFile(ctx, path, mime, task.AudioDuration)
		}
	}
	// Legacy operator-installed byte protocols retain their documented adapter
	// support boundary; gateway and direct built-in ASR never enter this branch.
	f, e := openLocalBlob(path)
	if e != nil {
		return AlignmentResult{}, e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return AlignmentResult{}, e
	}
	if info.Size() <= 0 || info.Size() > 24<<20 {
		return AlignmentResult{}, providerFailure("unsupported_capability")
	}
	data, e := io.ReadAll(f)
	if e != nil {
		return AlignmentResult{}, e
	}
	if task.Mode == "forced" {
		if forced, ok := p.(ForcedAlignmentProvider); ok {
			return forced.Align(ctx, data, mime, task.AudioDuration, task.Text)
		}
		return AlignmentResult{}, providerFailure("unsupported_capability")
	}
	return p.Transcribe(ctx, data, mime, task.AudioDuration)
}
