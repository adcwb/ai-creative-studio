import { ApiError, request } from '../../api'
import type { Asset, Detail } from './types'

export function isWorkflowVersionConflict(error: unknown) {
  return (
    error instanceof ApiError && error.status === 409 && error.code === 'workflow_version_conflict'
  )
}

export function loadWorkflow(project: string, space: string, id: string, signal?: AbortSignal) {
  return request<Detail>(`/projects/${project}/workflows/${id}?space_id=${space}`, { signal })
}

export async function loadWorkflowAssets(project: string, space: string, signal: AbortSignal) {
  const { assets } = await request<{ assets: Asset[] }>(
    `/projects/${project}/media?space_id=${space}`,
    { signal },
  )
  return assets
}
