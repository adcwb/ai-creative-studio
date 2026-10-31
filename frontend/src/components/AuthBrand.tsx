import { useEffect, useState } from 'react'
import { useI18n } from '../i18n'

const authScenes = [
  {id: 'vision', title: '让想象，有迹可循。', description: '从图像构思到参考编辑，把灵感留在画面里。', label: '图像创作与编辑', planned: false},
  {id: 'sequence', title: '让每一帧，讲述故事。', description: '从剧本与分镜，到镜头编排和短片输出。', label: '短剧与镜头制作', planned: false},
  {id: 'presence', title: '让表达，拥有新的在场。', description: '探索能对话的数字角色。交互引擎尚未上线。', label: '交互型数字人', planned: true},
]
export function AuthBrand() {
  const {t} = useI18n()
  const [index, setIndex] = useState(0)
  const [paused, setPaused] = useState(false)
  const [reduced, setReduced] = useState(() => matchMedia('(prefers-reduced-motion: reduce)').matches)
  const [ready, setReady] = useState<string[]>([])
  useEffect(() => {
    const query = matchMedia('(prefers-reduced-motion: reduce)')
    const update = () => setReduced(query.matches)
    query.addEventListener('change', update)
    return () => query.removeEventListener('change', update)
  }, [])
  useEffect(() => {
    if (paused || reduced) return
    const timer = window.setInterval(() => {
      if (!document.hidden) setIndex(current => {
        const next = (current + 1) % authScenes.length
        return ready.includes(authScenes[next].id) ? next : current
      })
    }, 8000)
    return () => window.clearInterval(timer)
  }, [paused, reduced, ready])
  const scene = authScenes[index]
  return <aside className="auth-brand" aria-label={t('创作灵感画廊')}>
    <img className="auth-brand-logo" src="/brand/lockup-dark.svg" width="158" height="48" alt={t('映创 Creative Studio')} />
    <div className="auth-gallery" role="region" aria-roledescription={t('轮播')} aria-label={t('平台创作能力')} data-scene={scene.id}>
      <div className="auth-scene-images" aria-hidden="true">
        {authScenes.map((item, i) => <img key={item.id} src={`/brand/scenes/${item.id}.webp`} width="1536" height="1024" alt="" loading="eager" decoding="async" fetchPriority={i === 0 ? 'high' : 'low'} className={i === index ? 'is-active' : ''} onLoad={() => setReady(current => current.includes(item.id) ? current : [...current, item.id])} />)}
      </div>
      <div className="auth-scene-copy" aria-live={paused || reduced ? 'polite' : 'off'} aria-atomic="true">
        <div className="auth-scene-kicker"><span>{t(scene.label)}</span>{scene.planned && <span className="planned-badge">{t('规划中')}</span>}</div>
        <h2>{t(scene.title)}</h2><p>{t(scene.description)}</p>
      </div>
      <div className="auth-gallery-controls">

        <button type="button" className="auth-playback" disabled={reduced} aria-label={t(reduced ? '已按系统偏好停止动画' : paused ? '播放轮播' : '暂停轮播')} onClick={() => setPaused(value => !value)}>{t(reduced ? '静态展示' : paused ? '播放' : '暂停')}</button>
      </div>
    </div>
    <div className="auth-brand-bottom"><p>{t('一个想法，无限种表达。')}</p><span>{t('图像 · 分镜 · 短片')}</span></div>
  </aside>
}
