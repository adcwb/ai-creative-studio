package generation

import (
	"errors"
)

var ErrNotFound = errors.New("记录不存在")

var ErrConflict = errors.New("请求与已有任务冲突，或任务尚未完成")

var ErrProviderNotReady = errors.New("生成服务尚未就绪，请检查服务端配置")

var ErrProviderChanged = errors.New("生成模式已发生变化，请刷新后确认再提交")
