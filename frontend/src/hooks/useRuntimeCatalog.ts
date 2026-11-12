import {useEffect, useState} from 'react'
import {request, spaceQuery} from '../api'
import {runtimeCatalogCache, type RuntimeCatalog} from '../runtimeCatalogCache'

export function useRuntimeCatalog(actor: string, space: string, feature: string, enabled: boolean) {
  const key = JSON.stringify([actor, space, feature])
  const [result, setResult] = useState<{key: string; catalog: RuntimeCatalog | null; error: boolean} | null>(null)
  useEffect(() => {
    if (!enabled || !space) return
    let epoch = 0
    function load() {
      const current = ++epoch
      setResult({key, catalog: null, error: false})
      void runtimeCatalogCache.read(key, signal => request<RuntimeCatalog>(`/runtime-models?${spaceQuery(space, {feature_key: feature})}`, {signal})).then(catalog => {
        if (current === epoch) setResult({key, catalog, error: false})
      }).catch(() => {if (current === epoch) setResult({key, catalog: null, error: true})})
    }
    const unsubscribe = runtimeCatalogCache.subscribe(load)
    load()
    // A picker leaving must not cancel a request shared with other pickers.
    return () => {epoch++; unsubscribe()}
  }, [key, enabled, space, feature])
  return {catalog: result?.key === key ? result.catalog : null, error: result?.key === key && result.error, refresh: () => runtimeCatalogCache.clear()}
}
