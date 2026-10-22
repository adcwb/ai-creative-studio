export type Kind = 'poster' | 'portrait' | 'general'
export type Ratio = '1:1' | '4:5' | '16:9'
export type Style = 'natural' | 'studio' | 'illustration'
export interface TaskInput { prompt: string; kind: Kind; ratio: Ratio; style: Style }
export interface Task extends TaskInput {
  id: string; space_id: string
  status: 'queued' | 'running' | 'succeeded' | 'failed'; progress: number
  provider: 'mock-svg'; result_url?: string; error?: string; created_at: string; retry_of?: string
}
export interface Asset extends TaskInput { id: string; task_id: string; space_id: string; provider: 'mock-svg'; result_url: string }
export interface Template extends TaskInput { id: string; name: string; description: string }
export interface Space { id: string; name: string; type: string; owner_id: string }
export interface Catalog {
  templates: Template[]
  ratios: {value: Ratio; label: string}[]
  styles: {value: Style; label: string}[]
}
export const kindLabels: Record<Kind, string> = {poster: '创意海报', portrait: '肖像概念', general: '通用图像'}
export const statusLabels: Record<Task['status'], string> = {queued: '等待执行', running: '模拟制作中', succeeded: '模拟完成', failed: '执行失败'}
export const baseURL = (import.meta.env.VITE_API_BASE_URL || 'http://localhost:8080').replace(/\/$/, '')
export async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(`${baseURL}/api/v1${path}`, {...init, signal: init?.signal ? AbortSignal.any([init.signal, AbortSignal.timeout(10000)]) : AbortSignal.timeout(10000)})
  if (!response.ok) {
    const body = await response.json().catch(() => null) as {error?: {message?: string}} | null
    throw new Error(body?.error?.message || `请求失败（${response.status}）`)
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
