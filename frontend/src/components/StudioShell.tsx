import type { ReactNode } from 'react'
import type { Capabilities, Space } from '../api'
import { capabilityLabel } from '../providerPresentation'
import { Icon } from './Icon'

export function StudioShell({spaces, space, capability, capabilityError, onSpaceChange, children}: {spaces: Space[]; space: string; capability: Capabilities | null; capabilityError: string; onSpaceChange: (space: string) => void; children: ReactNode}) {
  return <div className="shell">
    <a className="skip-link" href="#workspace">跳到创作工作区</a>
    <aside className="sidebar">
      <div className="sidebar-inner">
      <a className="brand" href="/" aria-label="映创工作室首页"><img src="/brand/lockup-dark.svg" alt="映创 Creative Studio" width="158" height="48" /></a>
      <div className="space-picker"><label htmlFor="space">工作空间</label><select id="space" aria-label="选择演示空间" value={space} disabled={!spaces.length} onChange={event => onSpaceChange(event.target.value)}>{spaces.length ? spaces.map(item => <option key={item.id} value={item.id}>{item.name}</option>) : <option value="">等待空间数据</option>}</select></div>
      <div className="sidebar-current"><Icon name="image" /><span>视觉工作室</span><span className="current-dot" /></div>
      <div className="sidebar-note">让创意，<br />从一张图开始。</div>
      <div className="roadmap"><span className="eyebrow">COMING LATER</span><strong>短剧 / 漫剧工作台</strong><p>规划中 · 暂未提供生成能力</p></div>
      <div className="demo-id"><span className="avatar">映</span><div>演示身份 · demo-user<small>无生产认证 · 空间不构成权限隔离</small></div></div>
      </div>
    </aside>
    <main id="workspace"><header className="topbar"><span>工作室 <span aria-hidden="true">/</span> 视觉创作</span><span className={`provider-badge ${capability?.ready ? '' : 'provider-unavailable'}`}><span />{capabilityLabel(capability, capabilityError)}</span></header>{children}<footer><span>映创 · 为每一次灵感留白</span><span>创作记录 / 持久保存</span></footer></main>
  </div>
}
