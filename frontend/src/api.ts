export type Kind = 'poster' | 'portrait' | 'general'
export type Ratio = '1:1' | '4:5' | '16:9'
export type Style = 'natural' | 'studio' | 'illustration'
export type Provider = 'mock-svg' | 'openai-images' | 'gemini-images' | 'ark-images' | 'jimeng-images' | 'minimax-images'
export interface Capabilities {
  provider: Provider | 'unavailable'; mode: 'mock' | 'real' | 'unavailable'; ready: boolean
  model?: string; reason_code?: string; message: string
}
export interface ResultSource { provider: Provider; model?: string; result_mime?: string; result_width?: number; result_height?: number }
export interface TaskInput { prompt: string; kind: Kind; ratio: Ratio; style: Style }
export interface Task extends TaskInput, ResultSource {
  id: string; space_id: string
  status: 'queued' | 'running' | 'succeeded' | 'failed'; progress: number
  result_url?: string; error?: string; error_code?: string; created_at: string; retry_of?: string; upstream_id?: string
}
export interface Asset extends TaskInput, ResultSource { id: string; task_id: string; space_id: string; result_url: string }
export interface Template extends TaskInput { id: string; name: string; description: string }
export interface Space { id: string; name: string; type: string; owner_id: string }
export interface Catalog {
  templates: Template[]
  ratios: {value: Ratio; label: string}[]
  styles: {value: Style; label: string}[]
}
export const kindLabels: Record<Kind, string> = {poster: '创意海报', portrait: '肖像概念', general: '通用图像'}
export const baseURL = (import.meta.env.VITE_API_BASE_URL || 'http://localhost:8080').replace(/\/$/, '')
export class ApiError extends Error {
  constructor(message: string, readonly status: number, readonly code?: string) {
    super(message)
    this.name = 'ApiError'
  }
}
export async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(`${baseURL}/api/v1${path}`, {...init, signal: init?.signal ? AbortSignal.any([init.signal, AbortSignal.timeout(10000)]) : AbortSignal.timeout(10000)})
  if (!response.ok) {
    const body = await response.json().catch(() => null) as {error?: {message?: string; code?: string}} | null
    throw new ApiError(body?.error?.message || `请求失败（${response.status}）`, response.status, body?.error?.code)
  }
  return response.json() as Promise<T>
}
export function promptError(prompt: string): string | null {
  const size = [...prompt.trim()].length
  return size < 3 || size > 1000 ? '请填写 3–1000 个字符的画面描述。' : null
}
export function errorMessage(error: unknown, fallback: string) {
  return error instanceof Error ? error.message : fallback
}
