import type {Space} from './api'
// Older servers omit permissions. Explicit empty permissions always deny.
const defaults: Record<string, string[]> = {
  owner: ['workspace.read','workspace.write','workspace.manage','members.read','members.manage','members.manage_admin','models.manage','models.execute','usage.read'],
  admin: ['workspace.read','workspace.write','workspace.manage','members.read','members.manage','models.manage','models.execute','usage.read'],
  editor: ['workspace.read','workspace.write','members.read','models.execute','usage.read'], viewer: ['workspace.read','members.read','usage.read'],
}
export function hasPermission(space: Space | undefined, permission: string) {
  if(space&&space.permissions===undefined&&space.role===undefined&&['personal-demo','team-demo'].includes(space.id))return permission==='workspace.write'||permission==='models.execute'
  return (space?.permissions ?? defaults[space?.role ?? ''] ?? []).includes(permission)
}
