import { translate, getLocale } from './i18n'
import {runtimeCatalogCache} from './runtimeCatalogCache'
export type Kind = 'poster' | 'portrait' | 'general'
export type Ratio = '1:1' | '4:5' | '16:9' | '9:16'
export type Style = 'natural' | 'studio' | 'illustration'
export type Provider = 'gateway-images' | 'mock-svg' | 'openai-images' | 'gemini-images' | 'gemini-interactions-images' | 'ark-images' | 'jimeng-images' | 'minimax-images'
export interface Capabilities {
  provider: Provider | 'unavailable'; mode: 'mock' | 'real' | 'unavailable'; ready: boolean
  model?: string; reason_code?: string; message: string
}
export interface ResultSource { provider: Provider; model?: string; result_mime?: string; result_width?: number; result_height?: number }
export interface PromptSource {application_id?: string;variables?: Record<string, string>; template_id: string; version_id: string; edited: boolean; compiled_sha256?: string; effective_sha256?: string}
export interface TaskInput { prompt_source?: PromptSource; prompt: string; kind: Kind; ratio: Ratio; style: Style }
export interface Task extends TaskInput, ResultSource {
  id: string; space_id: string
  status: 'queued' | 'running' | 'succeeded' | 'failed'; progress: number
  result_url?: string; error?: string; error_code?: string; created_at: string; retry_of?: string; upstream_id?: string
}
export interface Asset extends TaskInput, ResultSource { id: string; task_id: string; space_id: string; result_url: string }
export interface Template extends TaskInput { id: string; name: string; description: string }
export interface Space { id: string; name: string; type: string; owner_id: string; role?: 'owner' | 'admin' | 'editor' | 'viewer'; permissions?: string[]; version?: number }
export interface Catalog {
  templates: Template[]
  ratios: {value: Ratio; label: string}[]
  styles: {value: Style; label: string}[]
}
export const kindLabels: Record<Kind, string> = {poster: "创意海报", portrait: "肖像概念", general: "通用图像"}
export const baseURL = (import.meta.env.VITE_API_BASE_URL || 'http://localhost:8080').replace(/\/$/, '')
export class ApiError extends Error {
  constructor(message: string, readonly status: number, readonly code?: string, readonly field?: string) {
    super(message)
    this.name = 'ApiError'
  }
}
let csrfToken = ''
export function setCSRFToken(token: string) {
  if (csrfToken !== token) {csrfToken = token; runtimeCatalogCache.clear()}
}
export function spaceQuery(space: string, values: Record<string, string> = {}) {
  return new URLSearchParams({...values, space_id: space}).toString()
}
export async function request<T>(path: string, init?: RequestInit & {timeoutMs?: number}): Promise<T> {
  const headers = new Headers(init?.headers)
  if (csrfToken && !['GET', 'HEAD', 'OPTIONS'].includes((init?.method ?? 'GET').toUpperCase())) headers.set('X-CSRF-Token', csrfToken)
  const {timeoutMs,...options}=init??{}
  const timeout = AbortSignal.timeout(timeoutMs??(init?.body instanceof Blob ? 300000 : 10000))
	const response = await fetch(`${baseURL}/api/v1${path}`, {...options, headers, credentials: 'include', signal: init?.signal ? AbortSignal.any([init.signal, timeout]) : timeout})
  if (!response.ok) {
    if (response.status === 401 && !path.startsWith('/auth/') && typeof window !== 'undefined') window.dispatchEvent(new Event('studio-session-expired'))
    const body = await response.json().catch(() => null) as {error?: {message?: string; code?: string; field?: string}} | null
    throw new ApiError(localizedError(body?.error?.message, body?.error?.code ?? String(response.status)), response.status, body?.error?.code, body?.error?.field)
  }
  const result = await response.json() as T
  if (!['GET', 'HEAD', 'OPTIONS'].includes((init?.method ?? 'GET').toUpperCase()) && /^\/(ai-gateways|gateway-models|model-features|model-config|model-configs|model-bindings|system-config)([/?]|$)/.test(path)) runtimeCatalogCache.clear()
  return result
}
export function promptError(prompt: string): string | null {
  const size = [...prompt.trim()].length
  return size < 3 || size > 1000 ? translate("请填写 3–1000 个字符的画面描述。") : null
}
export function errorMessage(error: unknown, fallback: string) {
  return error instanceof Error ? localizedError(error.message, error instanceof ApiError ? error.code : undefined) : fallback
}

export function localizedError(message?: string, code?: string) {
  if (getLocale() === 'zh-CN') return message && /[\u3400-\u9fff]/.test(message) ? message : code ? translate('服务拒绝了请求（{p0}）。请检查输入与当前配置。', {p0: code}) : translate('网络或服务暂时不可用，请稍后再试。')
  if (message && !/[\u3400-\u9fff]/.test(message)) return message
  const translated = message ? translate(message) : ''
  if (translated && !/[\u3400-\u9fff]/.test(translated)) return translated
  return code ? translate('服务拒绝了请求（{p0}）。请检查输入与当前配置。', {p0: code}) : translate('网络或服务暂时不可用，请稍后再试。')
}
