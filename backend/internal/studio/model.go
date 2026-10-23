package studio

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

type Input struct {
	Prompt string `json:"prompt"`
	Kind   string `json:"kind"`
	Ratio  string `json:"ratio"`
	Style  string `json:"style"`
}

func Normalize(in Input) (Input, error) {
	in.Prompt = strings.TrimSpace(in.Prompt)
	in = withDefaults(in)
	if utf8.RuneCountInString(in.Prompt) < 3 || utf8.RuneCountInString(in.Prompt) > 1000 {
		return in, errors.New("描述需为 3–1000 个字符")
	}
	if in.Kind != "poster" && in.Kind != "portrait" && in.Kind != "general" {
		return in, errors.New("类型必须为 poster、portrait 或 general")
	}
	if in.Ratio != "1:1" && in.Ratio != "4:5" && in.Ratio != "16:9" {
		return in, errors.New("比例必须为 1:1、4:5 或 16:9")
	}
	if in.Style != "natural" && in.Style != "studio" && in.Style != "illustration" {
		return in, errors.New("风格必须为 natural、studio 或 illustration")
	}
	return in, nil
}

// withDefaults also upgrades older snapshots without changing stored content.
func withDefaults(in Input) Input {
	if in.Ratio == "" {
		in.Ratio = "4:5"
	}
	if in.Style == "" {
		in.Style = "natural"
	}
	return in
}

type Task struct {
	UpstreamID string `json:"upstream_id,omitempty"`
	ID         string `json:"id"`
	SpaceID    string `json:"space_id"`
	Input
	Status       string    `json:"status"`
	Progress     int       `json:"progress"`
	Provider     string    `json:"provider"`
	Model        string    `json:"model,omitempty"`
	RetryOf      string    `json:"retry_of,omitempty"`
	ResultURL    string    `json:"result_url,omitempty"`
	ResultMIME   string    `json:"result_mime,omitempty"`
	ResultWidth  int       `json:"result_width,omitempty"`
	ResultHeight int       `json:"result_height,omitempty"`
	Error        string    `json:"error,omitempty"`
	ErrorCode    string    `json:"error_code,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	SVG          string    `json:"result_svg,omitempty"`
	Key          string    `json:"idempotency_key,omitempty"`
	ResultPath   string    `json:"result_path,omitempty"`
}

func (t Task) Public() Task { t.SVG = ""; t.Key = ""; t.ResultPath = ""; return t }

type Asset struct {
	ID           string    `json:"id"`
	SpaceID      string    `json:"space_id"`
	TaskID       string    `json:"task_id"`
	Prompt       string    `json:"prompt"`
	Kind         string    `json:"kind"`
	Ratio        string    `json:"ratio"`
	Style        string    `json:"style"`
	Provider     string    `json:"provider"`
	Model        string    `json:"model,omitempty"`
	ResultURL    string    `json:"result_url"`
	ResultMIME   string    `json:"result_mime,omitempty"`
	ResultWidth  int       `json:"result_width,omitempty"`
	ResultHeight int       `json:"result_height,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

var ErrNotFound = errors.New("记录不存在")
var ErrConflict = errors.New("请求与已有任务冲突，或任务尚未完成")
var ErrProviderNotReady = errors.New("生成服务尚未就绪，请检查服务端配置")
var ErrProviderChanged = errors.New("生成模式已发生变化，请刷新后确认再提交")

// Expectation is a submission guard, not part of the persisted creative input
// or the idempotency identity. Older clients may omit it.
type Expectation struct {
	Provider string
	Model    string
}

// GenerationResult is the validated artifact produced by the Eino pipeline.
// Raw bytes and local storage paths are never included in public task JSON.
type GenerationResult struct {
	Data     []byte
	MIMEType string
	Width    int
	Height   int
}

type Capabilities struct {
	Provider   string `json:"provider"`
	Mode       string `json:"mode"`
	Ready      bool   `json:"ready"`
	Model      string `json:"model,omitempty"`
	ReasonCode string `json:"reason_code,omitempty"`
	Message    string `json:"message"`
}
