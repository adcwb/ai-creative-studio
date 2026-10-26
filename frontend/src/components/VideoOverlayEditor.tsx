import { translate as t } from '../i18n'
import type { VideoOverlay, VisualAsset } from '../mediaTimeline'

export function VideoOverlayEditor({layers, assets, duration, onChange}: {layers: VideoOverlay[]; assets: (VisualAsset & {name: string})[]; duration: number; onChange: (layers: VideoOverlay[]) => void}) {
  const visual = assets.filter(asset => asset.kind !== 'audio')
  function update(index: number, patch: Partial<VideoOverlay>) {onChange(layers.map((layer, i) => i === index ? {...layer, ...patch} : layer))}
  function move(index: number, delta: number) {const next = [...layers]; [next[index], next[index + delta]] = [next[index + delta], next[index]]; onChange(next)}
  const fields = [
    {name: 'start', label: t('成片起点（秒）'), min: 0, max: duration, step: .1},
    {name: 'trim_start', label: t('视频起点（秒）'), min: 0, max: 180, step: .1},
    {name: 'duration', label: t('使用时长（秒）'), min: .1, max: duration, step: .1},
    {name: 'x', label: t('横向位置（0–1）'), min: 0, max: .9, step: .01},
    {name: 'y', label: t('纵向位置（0–1）'), min: 0, max: .9, step: .01},
    {name: 'width', label: t('画幅宽度（0–1）'), min: .1, max: 1, step: .01},
    {name: 'height', label: t('画幅高度（0–1）'), min: .1, max: 1, step: .01},
    {name: 'opacity', label: t('不透明度'), min: 0, max: 1, step: .05},
  ] as const
  return <section aria-label={t('画面叠层')}>
    <h3>{t('画面叠层')}</h3>
    <p className="drama-help">{t('最多4条图片或视频叠层，后列叠层显示在上方。位置和大小相对成片画幅；时间相对交叉溶解后的成片。保存并导出后查看实际合成，不提供实时叠层预览。')}</p>
    {!layers.length && <p className="drama-help">{t('尚未添加画面叠层。原镜头时间轴仍正常播放。')}</p>}
    {layers.map((layer, index) => <article key={layer.id}>
      <div className="media-actions"><strong>{t('叠层 {index}', {index: index + 1})}</strong>
        <button disabled={index === 0} onClick={() => move(index, -1)}>{t('下移叠层 {index}', {index: index + 1})}</button>
        <button disabled={index === layers.length - 1} onClick={() => move(index, 1)}>{t('上移叠层 {index}', {index: index + 1})}</button>
        <button onClick={() => onChange(layers.filter((_, i) => i !== index))}>{t('移除叠层 {index}', {index: index + 1})}</button>
      </div>
      <div className="storyboard-fields">
        <label>{t('画面素材')}<select aria-label={t('叠层 {index} 素材', {index: index + 1})} value={layer.asset_id} onChange={event => update(index, {asset_id: event.target.value, preserve_source_audio: false, source_audio_volume: 0, trim_start: 0})}>{visual.map(asset => <option key={asset.id} value={asset.id}>{asset.name}</option>)}</select></label>
        {fields.map(field => <label key={field.name}>{field.label}<input type="number" aria-label={t('叠层 {index} {field}', {index: index + 1, field: field.label})} min={field.min} max={field.max} step={field.step} disabled={field.name === 'trim_start' && visual.find(asset => asset.id === layer.asset_id)?.kind !== 'video'} value={layer[field.name]} onChange={event => update(index, {[field.name]: Number(event.target.value)})} /></label>)}
        <label><input type="checkbox" checked={layer.preserve_source_audio} disabled={visual.find(asset => asset.id === layer.asset_id)?.kind !== 'video'} onChange={event => update(index, {preserve_source_audio: event.target.checked, source_audio_volume: event.target.checked ? 1 : 0})} />{t('保留叠层 {index} 视频原音', {index: index + 1})}</label>
        <label>{t('原音音量')}<input aria-label={t('叠层 {index} 原音音量', {index: index + 1})} type="number" min="0" max="1" step="0.1" disabled={!layer.preserve_source_audio} value={layer.source_audio_volume} onChange={event => update(index, {source_audio_volume: Number(event.target.value)})} /></label>
      </div>
    </article>)}
    <button disabled={!visual.length || layers.length >= 4 || duration < .1} onClick={() => onChange([...layers, {id: crypto.randomUUID(), asset_id: visual[0].id, start: 0, trim_start: 0, duration: Math.min(3, duration, visual[0].kind === 'video' ? visual[0].duration : 3), x: .65, y: .05, width: .3, height: .3, opacity: 1, preserve_source_audio: false, source_audio_volume: 0}])}>{t('添加画面叠层')}</button>
  </section>
}
