import { translate } from '../i18n'
import { useEffect, useState } from 'react'
import { errorMessage, request, type Capabilities } from '../api'

export function useCapabilities() {
  const [revision, setRevision] = useState(0)
  const [result, setResult] = useState<{revision: number; value: Capabilities} | null>(null)
  const [failure, setFailure] = useState<{revision: number; message: string} | null>(null)
  useEffect(() => {
    const controller = new AbortController()
    void request<Capabilities>('/capabilities', {signal: controller.signal})
      .then(value => {if (!controller.signal.aborted) {setResult({revision, value}); setFailure(null)}})
      .catch(error => {if (!controller.signal.aborted) setFailure({revision, message: errorMessage(error, translate("服务状态读取失败"))})})
    return () => controller.abort()
  }, [revision])
  const capability = result?.revision === revision ? result.value : null
  const error = failure?.revision === revision ? failure.message : ''
  return {capability, error, loading: !capability && !error, refresh: () => setRevision(value => value + 1)}
}
