import type {Capabilities} from './api'
import {runtimeReady, type RuntimeChoice, type RuntimePreview} from './runtimeSelection'

// Only the explicitly named development fixture profile retains synthetic UI
// workflows. Normal dev and production builds always require a gateway; the
// fixture profile cannot be built for deployment (see vite.config.ts).
export const gatewayCreationRequired = !(import.meta.env.DEV && import.meta.env.MODE === 'legacy-fixtures')
export function creationReady(choice: RuntimeChoice | null, preview: RuntimePreview) {
  return (!gatewayCreationRequired || !!choice?.model.gateway_id) && runtimeReady(choice, preview)
}
export function creationCapability(choice: RuntimeChoice | null, preview: RuntimePreview, legacy?: Capabilities | null): Capabilities {
  if (!gatewayCreationRequired && !choice) return legacy ?? {provider: 'unavailable', mode: 'unavailable', ready: false, message: ''}
  return {provider: choice?.model.provider as Capabilities['provider'] ?? 'unavailable', model: choice?.model.model, mode: 'real', ready: creationReady(choice, preview), message: ''}
}
export function creationPath(path: string) {
  if (!gatewayCreationRequired) return path
  return path.replace(/^\/tasks(?=\/|\?|$)/, '/gateway-tasks')
    .replace(/^\/runtime-quote(?=\?|$)/, '/gateway-runtime-quote')
    .replace(/\/(generations|storyboard-generations|creative-jobs|analysis)(?=\?|$)/, '/gateway-$1')
}
