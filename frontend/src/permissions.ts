import type {Space} from './api'
// Older servers omit permissions. Explicit empty permissions always deny.
const defaults: Record<string, string[]> = {
  owner: ['workspace.write','members.manage','members.manage_admin','models.manage','models.execute'],
  admin: ['workspace.write','members.manage','models.manage','models.execute'],
  editor: ['workspace.write','models.execute'], viewer: [],
}
export function hasPermission(space: Space | undefined, permission: string) {
  return (space?.permissions ?? defaults[space?.role ?? ''] ?? []).includes(permission)
}
