import {translate} from './i18n'

// The backend already classifies every failure into one of eight categories and
// often supplies a suggestion. The client used to throw both away and print the
// raw English code. This turns what is already on the wire into a Chinese
// explanation plus a concrete next step, without inventing any field.
export type ErrorCategory = 'internal' | 'interrupted' | 'upstream' | 'storage' | 'configuration' | 'access' | 'conflict' | 'input'

export type ErrorAction = 'retry' | 'fix' | 'settings' | 'refresh' | 'verify' | 'contact'

export interface ErrorGuidance {title: string; next: string; action: ErrorAction; mayBill: boolean}

// Codes where the upstream may already have accepted the work, so a blind retry
// could pay twice. Mirrors the existing provider presentation rule.
const uncertainCodes = new Set(['provider_timeout', 'execution_interrupted', 'execution_outcome_unknown', 'provider_error', 'invalid_provider_response'])

const guidance: Record<ErrorCategory, {title: string; next: string; action: ErrorAction}> = {
  input: {title: '参数有问题', next: '按提示修正标红字段后重新提交。', action: 'fix'},
  access: {title: '没有权限', next: '确认当前空间与角色，或请管理员授权。', action: 'contact'},
  conflict: {title: '内容已被更新', next: '刷新取得最新版本后再提交，避免覆盖他人改动。', action: 'refresh'},
  upstream: {title: '模型服务返回错误', next: '稍后重试；若持续失败可换一个模型。', action: 'retry'},
  configuration: {title: '配置未就绪', next: '前往设置补齐网关与模型绑定。', action: 'settings'},
  storage: {title: '存储暂时不可用', next: '稍后重试；内容未丢失。', action: 'retry'},
  interrupted: {title: '本次结果尚未确认', next: '先核对记录再决定是否重试，避免重复提交。', action: 'verify'},
  internal: {title: '服务内部错误', next: '复制诊断编号反馈，便于定位。', action: 'contact'},
}

export function errorGuidance(category?: string, code?: string): ErrorGuidance {
  const known = category && category in guidance ? guidance[category as ErrorCategory] : undefined
  const base = known ?? (code ? guidance.upstream : guidance.internal)
  return {
    title: translate(base.title),
    next: translate(base.next),
    action: base.action,
    mayBill: category === 'interrupted' || (code ? uncertainCodes.has(code) : false),
  }
}