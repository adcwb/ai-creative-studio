export type Kind = 'poster' | 'portrait'
export interface Task {
  id: string; space_id: string; prompt: string; kind: Kind
  status: 'queued' | 'running' | 'succeeded' | 'failed'; progress: number
  provider: 'mock-svg'; result_url?: string; error?: string; created_at: string
}
export interface Asset { id: string; task_id: string; space_id: string; prompt: string; kind: Kind; provider: 'mock-svg'; result_url: string }
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
