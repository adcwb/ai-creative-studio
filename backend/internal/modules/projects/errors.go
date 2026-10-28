package projects

import (
	"errors"
	"fmt"
)

var (
	ErrInvalidProjectInput = errors.New("项目输入无效")
	ErrIdempotencyConflict = errors.New("幂等键已用于不同的项目操作或内容")
	ErrVersionConflict     = errors.New("项目版本已更新，请保留草稿并读取最新版本")
	ErrGenerationActive    = errors.New("项目正在生成，请等待本次生成完成")
	ErrScriptMissing       = errors.New("请先成功生成一版模拟剧本")
)

func InputError(message string) error {
	return fmt.Errorf("%w：%s", ErrInvalidProjectInput, message)
}

var ErrStoryboardMissing = errors.New("请先成功生成一份模拟分镜计划")
