package projects

import (
	"errors"
)

var (
	ErrInvalidProjectInput = errors.New("项目输入无效")
	ErrIdempotencyConflict = errors.New("幂等键已用于不同的项目操作或内容")
	ErrVersionConflict     = errors.New("项目版本已更新，请保留草稿并读取最新版本")
	ErrGenerationActive    = errors.New("项目正在生成，请等待本次生成完成")
	ErrScriptMissing       = errors.New("请先成功生成一版模拟剧本")
)

type inputError struct{ message string }

func (e *inputError) Error() string   { return ErrInvalidProjectInput.Error() + "：" + e.message }
func (*inputError) Unwrap() error     { return ErrInvalidProjectInput }
func InputError(message string) error { return &inputError{message: message} }
func InputMessage(err error) string {
	var e *inputError
	if errors.As(err, &e) {
		return e.Error()
	}
	return ErrInvalidProjectInput.Error()
}

var ErrStoryboardMissing = errors.New("请先成功生成一份模拟分镜计划")
