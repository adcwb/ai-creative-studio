import type {TaskInput} from '../../api'
export interface PromptMetadata {name: string; description: string; category: string; tags: string[]; visibility: 'private' | 'organization'}
export interface PromptVariable {name: string; label: string; type: 'text' | 'enum'; required: boolean; default?: string; max_length: number; options?: string[]}
export interface PromptRecipe {syntax: 'plain' | 'variables_v1'; variables?: PromptVariable[]; body: string; defaults: Pick<TaskInput, 'kind' | 'ratio' | 'style'>; notes: string}
export interface PromptTemplate extends PromptMetadata {id: string; space_id: string; owner_id: string; status: 'active' | 'archived'; head_version_id: string; row_version: number; created_at: string; updated_at: string}
export interface PromptVersion extends PromptRecipe {id: string; template_id: string; space_id: string; number: number; sha256: string; created_by: string; created_at: string}
export interface PromptResult {template: PromptTemplate; version: PromptVersion; favorite: boolean}
export interface PromptPage<T> {items: T[]; next_cursor: string}
export interface PendingPrompt {path: string; method: string; body: string; key: string; metadata: PromptMetadata; recipe: PromptRecipe; initial: PromptResult | null}
export function pendingKey(space: string, actor: string) {return `studio.prompt.pending:${encodeURIComponent(actor)}:${encodeURIComponent(space)}`}
export function readPending(space: string, actor: string): PendingPrompt | null {
  try {const raw = sessionStorage.getItem(pendingKey(space, actor)); if (!raw) return null; const value = JSON.parse(raw) as PendingPrompt; return value.path.startsWith('/prompt-templates') && ['POST', 'PATCH'].includes(value.method) && value.body && value.key && value.metadata && value.recipe ? value : null} catch {return null}
}
