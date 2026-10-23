import { translate as t, useI18n } from "../i18n"
import { useEffect, useRef, useState } from 'react'
import { errorMessage } from '../api'

export function MaskEditor({url, name, disabled, onSave, onClose}: {url: string; name: string; disabled: boolean; onSave: (blob: Blob, name: string) => void; onClose: () => void}) {
  useI18n()

  const canvas = useRef<HTMLCanvasElement>(null)
  const drawing = useRef(false)
  const [brush, setBrush] = useState(60)
  const [ready, setReady] = useState(false)
  const [error, setError] = useState('')
  const [rectangle, setRectangle] = useState({x: 25, y: 25, width: 50, height: 50})
  useEffect(() => {
    let live = true
    const image = new Image(); image.crossOrigin = 'use-credentials'
    image.onload = () => {
      if (!live || !canvas.current) return
      if (image.naturalWidth * image.naturalHeight > 20000000) {setError(t("图片像素过大。")); return}
      canvas.current.width = image.naturalWidth; canvas.current.height = image.naturalHeight
      const context = canvas.current.getContext('2d')!
      context.fillStyle = '#ffffff'; context.fillRect(0, 0, canvas.current.width, canvas.current.height); setReady(true)
    }
    image.onerror = () => {if (live) setError(t("无法读取原图，请重新登录或刷新媒体。"))}
    image.src = url
    return () => {live = false}
  }, [url])
  function erase(clientX: number, clientY: number) {
    const node = canvas.current
    if (!node || !ready || disabled) return
    const bounds = node.getBoundingClientRect(), context = node.getContext('2d')!
    context.globalCompositeOperation = 'destination-out'; context.beginPath(); context.arc((clientX - bounds.left) * node.width / bounds.width, (clientY - bounds.top) * node.height / bounds.height, brush * node.width / bounds.width / 2, 0, Math.PI * 2); context.fill(); context.globalCompositeOperation = 'source-over'
  }
  async function save() {
    if (!canvas.current) return
    try {
      const pixels = canvas.current.getContext('2d')!.getImageData(0, 0, canvas.current.width, canvas.current.height).data
      let transparent = false, opaque = false
      for (let i = 3; i < pixels.length; i += 4) {if (pixels[i] === 0) transparent = true; if (pixels[i] === 255) opaque = true; if (transparent && opaque) break}
      if (!transparent || !opaque) throw new Error(t("请同时保留要修改的透明区域和不修改的不透明区域。"))
      const blob = await new Promise<Blob>((resolve, reject) => canvas.current!.toBlob(value => value ? resolve(value) : reject(new Error(t("遮罩编码失败"))), 'image/png'))
      if (blob.size > 4 * 1024 * 1024) throw new Error(t("遮罩超过4 MiB，请使用更小的参考图。"))
      onSave(blob, `mask-${name.replace(/\.[^.]+$/, '')}.png`); onClose()
    } catch (reason) {setError(errorMessage(reason, t("遮罩保存失败")))}
  }
  return <section className="mask-editor"><h3>{t("局部编辑遮罩 ·")}{name}</h3><p className="drama-help">{t("在原图上涂抹需要修改的区域；未涂抹区域覆盖白色。仅生成透明PNG遮罩，不调用模型。保存后在创作类型“分镜图片”选择原图与遮罩，支持的真实供应商才可提交。")}</p>{error && <p role="alert" className="error">{error}</p>}<div className="mask-stage" style={{backgroundImage: `url(${JSON.stringify(url)})`}}><canvas ref={canvas} aria-label={t("局部编辑遮罩画布，可用下方矩形输入代替涂抹")} onPointerDown={event => {drawing.current = true; event.currentTarget.setPointerCapture(event.pointerId); erase(event.clientX, event.clientY)}} onPointerMove={event => {if (drawing.current) erase(event.clientX, event.clientY)}} onPointerUp={() => {drawing.current = false}} onPointerCancel={() => {drawing.current = false}} /></div><fieldset disabled={disabled || !ready}><label>{t("画笔直径")}<input type="range" min="10" max="150" value={brush} onChange={event => setBrush(Number(event.target.value))} /></label><div className="storyboard-fields">{(['x', 'y', 'width', 'height'] as const).map(key => <label key={key}>{{x: t("左侧位置%"), y: t("顶部位置%"), width: t("宽度%"), height: t("高度%")}[key]}<input type="number" min="0" max="100" value={rectangle[key]} onChange={event => setRectangle({...rectangle, [key]: Number(event.target.value)})} /></label>)}</div><div className="media-actions"><button onClick={() => {const node = canvas.current!; node.getContext('2d')!.clearRect(node.width * rectangle.x / 100, node.height * rectangle.y / 100, node.width * rectangle.width / 100, node.height * rectangle.height / 100)}}>{t("标记矩形修改区域")}</button><button onClick={() => {const node = canvas.current!, context = node.getContext('2d')!; context.fillStyle = '#fff'; context.fillRect(0, 0, node.width, node.height)}}>{t("重置遮罩")}</button><button onClick={() => void save()}>{t("保存遮罩为新素材")}</button><button onClick={onClose}>{t("关闭遮罩编辑")}</button></div></fieldset></section>
}
