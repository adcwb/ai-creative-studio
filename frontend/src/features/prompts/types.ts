import type {TaskInput} from '../../api'
export interface PromptMetadata {name: string; description: string; category: string; tags: string[]; visibility: 'private' | 'organization'}
export interface PromptVariable {name: string; label: string; type: 'text' | 'enum'; required: boolean; default?: string; max_length: number; options?: string[]}
export interface PromptReference {library_asset_id: string; asset_version_id: string; reference_id: string; purpose: string}
export interface PromptRecipe {reference_bindings?: PromptReference[];syntax: 'plain' | 'variables_v1'; variables?: PromptVariable[]; body: string; defaults: Pick<TaskInput, 'kind' | 'ratio' | 'style'>; notes: string}
export interface PromptTemplate extends PromptMetadata {id: string; space_id: string; owner_id: string; status: 'active' | 'archived'; head_version_id: string; row_version: number; created_at: string; updated_at: string}
export interface PromptVersion extends PromptRecipe {id: string; template_id: string; space_id: string; number: number; sha256: string; created_by: string; created_at: string}
export interface PromptResult {template: PromptTemplate; version: PromptVersion; favorite: boolean}
export interface PromptPage<T> {items: T[]; next_cursor: string}
export interface PendingPrompt {path: string; method: string; body: string; key: string; metadata: PromptMetadata; recipe: PromptRecipe; initial: PromptResult | null}
export function pendingKey(space: string, actor: string) {return `studio.prompt.pending:${encodeURIComponent(actor)}:${encodeURIComponent(space)}`}
export function readPending(space: string, actor: string): PendingPrompt | null {
  try {const raw = sessionStorage.getItem(pendingKey(space, actor)); if (!raw) return null; const value = JSON.parse(raw) as PendingPrompt; return value.path.startsWith('/prompt-templates') && ['POST', 'PATCH'].includes(value.method) && value.body && value.key && value.metadata && value.recipe ? value : null} catch {return null}
}

export interface AppliedPromptInput extends TaskInput {reference_asset_ids: string[]; application_id: string}
export interface PromptApplication {id: string; prompt: string; defaults: PromptRecipe['defaults']; prompt_source: NonNullable<TaskInput['prompt_source']>; reference_asset_ids: string[]}
export interface ApplyPending {body: string; key: string; item: PromptResult}
export function applyPendingKey(space: string, actor: string, project: string) {return `studio.prompt.apply:${encodeURIComponent(actor)}:${encodeURIComponent(space)}:${encodeURIComponent(project)}`}
export function readApplyPending(key: string): ApplyPending | null {try {const value = JSON.parse(sessionStorage.getItem(key) ?? 'null') as ApplyPending | null; return value?.body && value.key && value.item?.template?.id ? value : null} catch {return null}}
