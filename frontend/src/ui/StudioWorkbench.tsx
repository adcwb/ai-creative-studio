import {useState, type CSSProperties, type ReactNode} from 'react'
import {useI18n} from '../i18n'
import {Icon} from '../components/Icon'
import {usePreferences} from '../components/UserPreferences'
import {StudioDialog} from './StudioDialog'
import {useMobile} from './useMobile'
import './workbench.css'

// The shared three-column reading/production skeleton: outline on the left,
// canvas in the middle, inspector on the right. Pages fill the slots; none of
// them re-implement panel collapsing, sticky bars or the narrow-screen drawers.
// Desktop keeps both side panels; at the shell's 760px breakpoint they move into
// StudioDialog so there is still only one overlay implementation in the product.
export function StudioWorkbench({outline, inspector, toolbar, nextStep, children, outlineTitle, inspectorTitle, measure = false}: {
  children: ReactNode
  outline?: ReactNode
  inspector?: ReactNode
  toolbar?: ReactNode
  nextStep?: ReactNode
  outlineTitle: string
  inspectorTitle: string
  measure?: boolean
}) {
  const {t} = useI18n()
  const mobile = useMobile()
  const {panels, setPanels} = usePreferences()
  const [requested, setRequested] = useState<'outline' | 'inspector' | null>(null)
  // Leaving the narrow viewport closes the drawer instead of leaving stale state.
  const drawer = mobile ? requested : null
  const track = (present: boolean, collapsed: boolean, width: string) => present ? (collapsed ? 'var(--workbench-rail)' : width) : null
  const columns = mobile ? 'minmax(0, 1fr)' : [
    track(Boolean(outline), panels.outline, 'var(--workbench-outline)'),
    'minmax(0, 1fr)',
    track(Boolean(inspector), panels.inspector, 'var(--workbench-inspector)'),
  ].filter(Boolean).join(' ')
  const panel = (side: 'outline' | 'inspector', title: string, content: ReactNode, icon: 'layers' | 'settings') => {
    const collapsed = panels[side]
    const expandLabel = t(side === 'outline' ? '展开大纲' : '展开检查器')
    const collapseLabel = t(side === 'outline' ? '收起大纲' : '收起检查器')
    return <aside className={`studio-workbench-panel studio-workbench-${side}`} data-collapsed={collapsed || undefined} aria-label={title}>
      {collapsed
        ? <button type="button" className="studio-workbench-rail" aria-label={expandLabel} title={expandLabel} aria-expanded={false} onClick={() => setPanels({...panels, [side]: false})}><Icon name={icon} /></button>
        : <><div className="studio-workbench-panel-header"><h2>{title}</h2><button type="button" className="studio-workbench-collapse" aria-label={collapseLabel} title={collapseLabel} aria-expanded onClick={() => setPanels({...panels, [side]: true})}><Icon name="close" /></button></div><div className="studio-workbench-panel-body">{content}</div></>}
    </aside>
  }
  return <div className="studio-workbench" style={{'--workbench-columns': columns} as CSSProperties} data-measure={measure || undefined}>
    {!mobile && outline && panel('outline', outlineTitle, outline, 'layers')}
    <div className="studio-workbench-main">
      {mobile && (outline || inspector) && <div className="studio-workbench-switcher" role="group" aria-label={t('工作区面板')}>
        {outline && <button type="button" onClick={() => setRequested('outline')}><Icon name="layers" /><span>{outlineTitle}</span></button>}
        {inspector && <button type="button" onClick={() => setRequested('inspector')}><Icon name="settings" /><span>{inspectorTitle}</span></button>}
      </div>}
      {toolbar}
      <div className="studio-workbench-canvas">{children}</div>
      {nextStep}
    </div>
    {!mobile && inspector && panel('inspector', inspectorTitle, inspector, 'settings')}
    {drawer && <StudioDialog title={drawer === 'outline' ? outlineTitle : inspectorTitle} onClose={() => setRequested(null)} initialFocus="cancel"><div className="studio-workbench-drawer-body">{drawer === 'outline' ? outline : inspector}</div></StudioDialog>}
  </div>
}