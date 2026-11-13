import {useEffect, useRef, useState} from 'react'
import {errorMessage, promptError, request, type TaskInput} from '../../api'
import {useI18n} from '../../i18n'
import {runtimeFields, type RuntimeChoice} from '../../runtimeSelection'

export function usePromptOptimization(space: string, draft: TaskInput, onChange: (draft: TaskInput) => void) {
  const {t} = useI18n()
  const [choice, setChoice] = useState<RuntimeChoice | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [proposal, setProposal] = useState<{fingerprint: string; prompt: string} | null>(null)
  const [undo, setUndo] = useState<{fingerprint: string; draft: TaskInput} | null>(null)
  const pending = useRef<AbortController | null>(null)
  const fingerprint = JSON.stringify([space, draft])
  useEffect(() => () => {pending.current?.abort()}, [fingerprint])
  async function optimize() {
    if (pending.current || !choice?.model.available || promptError(draft.prompt)) return
    const controller = new AbortController(); pending.current = controller
    setBusy(true); setError(''); setProposal(null)
    try {
      const result = await request<{prompt: string}>(`/prompt-optimizations?${new URLSearchParams({space_id: space})}`, {
        method: 'POST', timeoutMs: 120000, signal: controller.signal,
        headers: {'Content-Type':'application/json', 'Idempotency-Key':crypto.randomUUID()},
        body: JSON.stringify({prompt:draft.prompt,kind:draft.kind,ratio:draft.ratio,style:draft.style,...runtimeFields(choice)}),
      })
      if (!controller.signal.aborted) {
        if (promptError(result.prompt)) throw new Error(t('优化结果无效，请重试。'))
        setProposal({fingerprint, prompt: result.prompt})
      }
    } catch (failure) {if (!controller.signal.aborted) setError(errorMessage(failure, t('提示词优化失败，请重试。')))}
    finally {if (pending.current === controller) {pending.current = null; setBusy(false)}}
  }
  function accept() {
    if (!proposal || proposal.fingerprint !== fingerprint) return
    const next = {...draft, prompt:proposal.prompt, ...(draft.prompt_source ? {prompt_source:{...draft.prompt_source,edited:true}} : {})}
    setUndo({fingerprint:JSON.stringify([space,next]),draft}); setProposal(null); onChange(next)
  }
  function restore() {if (undo?.fingerprint === fingerprint) {onChange(undo.draft); setUndo(null)}}
  return {choice,setChoice,busy,error,optimize,accept,restore,proposal:proposal?.fingerprint === fingerprint ? proposal.prompt : '',canUndo:undo?.fingerprint === fingerprint,dismiss:() => setProposal(null)}
}
