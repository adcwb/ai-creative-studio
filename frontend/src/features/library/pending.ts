export interface PendingLibraryWrite {path: string; body: string; key: string}
const storageKey = (space: string) => `studio.library.pending:${space}`
export function readPendingLibrary(space: string): PendingLibraryWrite | null {
  try {
    const raw = sessionStorage.getItem(storageKey(space))
    if (!raw) return null
    const value = JSON.parse(raw) as PendingLibraryWrite
    if (typeof value.path !== 'string' || !/^\/library\/assets(?:\/[A-Za-z0-9_-]{1,80}\/(?:versions|archive))?$/.test(value.path) || typeof value.body !== 'string' || value.body.length > 65536 || typeof value.key !== 'string' || !/^[A-Za-z0-9_-]{8,128}$/.test(value.key)) return null
    return value
  } catch {return null}
}
export function savePendingLibrary(space: string, value: PendingLibraryWrite) {sessionStorage.setItem(storageKey(space), JSON.stringify(value))}
export function clearPendingLibrary(space: string) {sessionStorage.removeItem(storageKey(space))}

export interface PendingBinding {body: string; key: string}
const bindingKey = (space: string, project: string) => `studio.library.binding:${space}:${project}`
export function readPendingBinding(space: string, project: string): PendingBinding | null {
  try {
    const raw = sessionStorage.getItem(bindingKey(space, project))
    if (!raw) return null
    const value = JSON.parse(raw) as PendingBinding
    if (typeof value.body !== 'string' || value.body.length > 65536 || typeof value.key !== 'string' || !/^[A-Za-z0-9_-]{8,128}$/.test(value.key)) return null
    return value
  } catch {return null}
}
export function savePendingBinding(space: string, project: string, value: PendingBinding) {sessionStorage.setItem(bindingKey(space, project), JSON.stringify(value))}
export function clearPendingBinding(space: string, project: string) {sessionStorage.removeItem(bindingKey(space, project))}
