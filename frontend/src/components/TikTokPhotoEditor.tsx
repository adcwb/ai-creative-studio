import { useRef, useState } from 'react'
import { ApiError, baseURL, errorMessage, request } from '../api'
import { useI18n } from '../i18n'
import { socialDownloadPath, type SocialMedia, type SocialSource } from '../social'

export function TikTokPhotoEditor({projectId, space, version, reload, sources, ids, cover, onChange, disabled}: {projectId: string; space: string; version: number; reload: () => void; sources: SocialSource[]; ids: string[]; cover: number; onChange: (ids: string[], cover: number) => void; disabled: boolean}) {
  const {t} = useI18n()
  const [normalizeId, setNormalizeId] = useState('')
  const [normalizationConsent, setNormalizationConsent] = useState(false)
  const [normalizing, setNormalizing] = useState(false)
  const [uncertain, setUncertain] = useState(false)
  const [error, setError] = useState('')
  const pending = useRef<{key: string; body: string} | null>(null)
  const photos = sources.filter(item => item.kind === 'asset' && ['image/jpeg', 'image/png'].includes(item.mime_type))
  async function normalize() {
    if (!normalizeId || !normalizationConsent || pending.current || normalizing || uncertain || ids.length >= 35) return
    setNormalizing(true); setError('')
    const attempt = {key: crypto.randomUUID(), body: JSON.stringify({expected_version: version, asset_id: normalizeId, confirmed: true})}
    pending.current = attempt
    try {
      const result = await request<SocialMedia & {normalized_asset_id: string}>(`/projects/${encodeURIComponent(projectId)}/social/photos/normalize?${new URLSearchParams({space_id: space})}`, {method: 'POST', headers: {'Content-Type': 'application/json', 'Idempotency-Key': attempt.key}, body: attempt.body})
      pending.current = null
      const newAsset = result.assets.find(asset => asset.id === result.normalized_asset_id && asset.mime_type === 'image/jpeg')
      if (!newAsset) {setUncertain(true); setError(t('转换结果未能匹配新素材，请刷新素材列表后检查，不要重复转换。'))}
      else {onChange([...ids, newAsset.id], cover); setNormalizeId(''); setNormalizationConsent(false)}
      reload()
    } catch (reason) {setError(errorMessage(reason, t('转换结果未确认，请刷新素材后检查，不要重复转换。'))); if (!(reason instanceof ApiError) || reason.status >= 500) setUncertain(true); else pending.current = null} finally {setNormalizing(false)}
  }
  function remove(index: number) {
    const next = ids.filter((_, i) => i !== index)
    onChange(next, cover === index ? 0 : cover > index ? cover - 1 : cover)
  }
  function move(index: number, offset: number) {
    const next = [...ids], target = index + offset
    if (target < 0 || target >= next.length) return
    ;[next[index], next[target]] = [next[target], next[index]]
    onChange(next, cover === index ? target : cover === target ? index : cover)
  }
  return <fieldset disabled={disabled || normalizing || uncertain} className="tiktok-photos"><legend>{t('TikTok 图片顺序与封面')}</legend>
    <p>{t('选择 1–35 张图片，按下列顺序发布。封面必须来自所选图片；使用前移、后移按钮调整顺序，支持键盘和触屏。')}</p>
    <label>{t('添加待发布图片')}<select aria-label={t('添加待发布图片')} value="" disabled={ids.length >= 35} onChange={event => {if (event.target.value && !ids.includes(event.target.value) && ids.length < 35) onChange([...ids, event.target.value], cover)}}><option value="">{t('选择图片或海报素材')}</option>{photos.filter(item => item.mime_type === 'image/jpeg' && !ids.includes(item.id)).map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label>
    <label>{t('转换图片或海报为发布用 JPEG')}<select aria-label={t('转换图片或海报为发布用 JPEG')} value={normalizeId} onChange={event => {setNormalizeId(event.target.value); setNormalizationConsent(false)}}><option value="">{t('选择需要转换的素材')}</option>{photos.map(item => <option key={item.id} value={item.id}>{item.name} · {item.mime_type}</option>)}</select></label>
    <label className="social-consent"><input type="checkbox" checked={normalizationConsent} onChange={event => setNormalizationConsent(event.target.checked)} />{t('我同意生成新 JPEG：透明区域铺白，最长边缩至 1080 像素，质量 90；保留原素材。')}</label>
    <button type="button" disabled={!normalizeId || !normalizationConsent || ids.length >= 35} onClick={() => void normalize()}>{normalizing ? t('正在转换发布图片…') : t('转换并预览新 JPEG')}</button>
    {error && <p role="alert">{error}</p>}
    <p>{t('已选 {count} / 35 张图片', {count: ids.length})}</p>
    <ol className="tiktok-photo-list">{ids.map((id, index) => {
      const source = photos.find(item => item.id === id), url = socialDownloadPath(source?.file_url)
      return <li key={id}>{url && <img src={`${baseURL}${url}`} alt={t('图片 {index} 预览：{name}', {index: index + 1, name: source?.name ?? id})} />}<strong>{index + 1}. {source?.name ?? t('素材已不可用')}</strong>
        <label className="social-consent"><input type="radio" name="tiktok-photo-cover" checked={cover === index} onChange={() => onChange(ids, index)} />{t('设为图片 {index} 封面', {index: index + 1})}</label>
        <div className="media-actions"><button type="button" disabled={index === 0} aria-label={t('图片 {index} 前移', {index: index + 1})} onClick={() => move(index, -1)}>{t('前移图片')}</button><button type="button" disabled={index === ids.length - 1} aria-label={t('图片 {index} 后移', {index: index + 1})} onClick={() => move(index, 1)}>{t('后移图片')}</button><button type="button" aria-label={t('移除图片 {index}', {index: index + 1})} onClick={() => remove(index)}>{t('移除图片')}</button></div>
      </li>
    })}</ol>
  </fieldset>
}
