import Popup from 'antd-mobile/es/components/popup'
import {useEffect, type ReactNode} from 'react'

export default function MobileSheet({children, container, zIndex, onShow}: {children: ReactNode; container: HTMLElement; zIndex: number; onShow: () => void}) {
  useEffect(() => {const frame = requestAnimationFrame(onShow); return () => cancelAnimationFrame(frame)}, [onShow])
  return <Popup visible getContainer={container} position="bottom" bodyClassName="studio-mobile-sheet" maskClassName="studio-sheet-mask" style={{'--z-index':String(zIndex)}}>{children}</Popup>
}
