import type { Capabilities } from '../api'
import { Icon } from './Icon'

export function ProviderBanner({capability, error, onRefresh}: {capability: Capabilities | null; error: string; onRefresh: () => void}) {
  if (error) return <div className="error capability-error" role="alert">无法读取图像服务状态：{error} <span>历史记录仍可浏览；创建与重试暂停，草稿会保留至服务状态恢复。</span><button type="button" onClick={onRefresh}>重新检查图像服务</button></div>
  if (!capability) return <div className="banner" role="status"><Icon name="refresh" className="spinning" /><div><strong>正在读取图像服务状态</strong><p>确认服务配置后即可创建任务，历史记录仍可浏览。</p></div></div>
  const ready = capability.ready && capability.mode !== 'unavailable'
  const mock = capability.mode === 'mock'
  return <div className={`banner provider-${ready ? capability.mode : 'unavailable'}`} data-provider={capability.provider}>
    <span className="banner-icon"><Icon name="image" /></span>
    <div><strong>{!ready ? '图像服务暂不可用' : mock ? '模拟演示模式' : '真实图像服务 · 配置就绪'}</strong><p>{capability.message}</p>
      {ready && !mock && <p>配置就绪不代表远端凭据与额度已验证。提交会请求真实图像服务，可能产生费用。</p>}
      {!ready && <p>创建与重试已暂停；历史记录仍可使用，草稿会保留至服务恢复。</p>}
      {capability.model && <p className="provider-model">模型：{capability.model}</p>}
    </div><div className="banner-controls"><span className="banner-tag">{ready ? mock ? '无需模型凭据' : '可能产生费用' : '等待服务配置'}</span><button type="button" className="text-button" onClick={onRefresh}>重新检查图像服务</button></div>
  </div>
}
