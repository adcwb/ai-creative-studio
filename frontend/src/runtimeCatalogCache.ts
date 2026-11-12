import type {RuntimeModelOption} from './runtimeSelection'

export interface RuntimeCatalog {
  moderation_mode?: 'disabled' | 'required'
  governance_ready: boolean
  models: RuntimeModelOption[]
  bindings?: {feature_key: string; model_config_id: string; allow_override: boolean; enabled: boolean; voice?: string}[]
}

export class ReadCache<T> {
  private entries = new Map<string, {promise: Promise<T>; expires: number; controller: AbortController}>()
  private listeners = new Set<() => void>()
  constructor(private ttl = 15000, private limit = 64) {}

  read(key: string, load: (signal: AbortSignal) => Promise<T>): Promise<T> {
    const cached = this.entries.get(key)
    if (cached && cached.expires > Date.now()) return cached.promise
    const controller = new AbortController()
    const entry = {controller, expires: Infinity, promise: Promise.resolve().then(() => load(controller.signal))}
    entry.promise = entry.promise.then(value => {entry.expires = Date.now() + this.ttl; return value}).catch(error => {
      if (this.entries.get(key) === entry) this.entries.delete(key)
      throw error
    })
    this.entries.set(key, entry)
    if (this.entries.size > this.limit) this.entries.delete(this.entries.keys().next().value!)
    return entry.promise
  }
  subscribe(listener: () => void) {
    this.listeners.add(listener)
    return () => {this.listeners.delete(listener)}
  }
  clear() {
    for (const entry of this.entries.values()) entry.controller.abort()
    this.entries.clear()
    this.listeners.forEach(listener => listener())
  }
}

export const runtimeCatalogCache = new ReadCache<RuntimeCatalog>()
