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
}

func Normalize(in Input) (Input, error) {
	in.Prompt = strings.TrimSpace(in.Prompt)
	if utf8.RuneCountInString(in.Prompt) < 3 || utf8.RuneCountInString(in.Prompt) > 1000 {
		return in, errors.New("描述需为 3–1000 个字符")
	}
	if in.Kind != "poster" && in.Kind != "portrait" {
		return in, errors.New("类型必须为 poster 或 portrait")
	}
	return in, nil
}

type Task struct {
	ID      string `json:"id"`
	SpaceID string `json:"space_id"`
	Input
	Status    string    `json:"status"`
	Progress  int       `json:"progress"`
	Provider  string    `json:"provider"`
	ResultURL string    `json:"result_url,omitempty"`
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	SVG       string    `json:"result_svg,omitempty"`
	Key       string    `json:"idempotency_key,omitempty"`
}

func (t Task) Public() Task { t.SVG = ""; t.Key = ""; return t }

type Asset struct {
	ID        string    `json:"id"`
	SpaceID   string    `json:"space_id"`
	TaskID    string    `json:"task_id"`
	Prompt    string    `json:"prompt"`
	Kind      string    `json:"kind"`
	Provider  string    `json:"provider"`
	ResultURL string    `json:"result_url"`
	CreatedAt time.Time `json:"created_at"`
}

var ErrNotFound = errors.New("记录不存在")
var ErrConflict = errors.New("请求与已有任务冲突，或任务尚未完成")
