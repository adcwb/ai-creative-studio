import { translate as t } from '../i18n'

export function AuthBrand() {
  return <aside className="auth-brand" aria-label={t('映创，让灵感成片。')}>
    <img className="auth-brand-logo" src="/brand/lockup-dark.svg" width="158" height="48" alt={t('映创 Creative Studio')} />
    <div className="auth-brand-story">
      <h2>{t('让灵感成片。')}</h2>
      <p>{t('从第一束灵感，到每一帧表达。')}</p>
      <div className="auth-artwork" aria-hidden="true">
        <div className="auth-artwork-outline" />
        <div className="auth-artwork-main">
          <svg viewBox="0 0 560 360" fill="none">
            <defs>
              <linearGradient id="auth-sky" x1="280" y1="0" x2="280" y2="360" gradientUnits="userSpaceOnUse"><stop stopColor="#c5d4b3" /><stop offset="1" stopColor="#e9e9cf" /></linearGradient>
              <linearGradient id="auth-hill" x1="160" y1="170" x2="370" y2="360" gradientUnits="userSpaceOnUse"><stop stopColor="#6e8c73" /><stop offset="1" stopColor="#264e42" /></linearGradient>
              <linearGradient id="auth-light" x1="290" y1="200" x2="370" y2="360" gradientUnits="userSpaceOnUse"><stop stopColor="#ecedbe" stopOpacity=".8" /><stop offset="1" stopColor="#ecedbe" stopOpacity="0" /></linearGradient>
            </defs>
            <path fill="url(#auth-sky)" d="M0 0h560v360H0z" />
            <circle cx="405" cy="102" r="46" fill="#f4f1d9" />
            <path d="M0 234c92-104 160-101 263-29S438 278 560 155v205H0Z" fill="#9bad8e" />
            <path d="M0 295c136 28 178-144 307-110s161 89 253 50v125H0Z" fill="url(#auth-hill)" />
            <path d="M284 165h47v88h-47z" fill="#22473a" />
            <path d="M290 171h35v82h-35z" fill="#e5e8b9" />
            <path d="m290 253 35-5 124 112H250Z" fill="url(#auth-light)" />
            <path d="M22 47V22h25m466 0h25v25M22 313v25h25m466 0h25v-25" stroke="#f7f7e8" strokeWidth="1.5" opacity=".8" />
          </svg>
          <div className="auth-artwork-caption"><span>{t('每个想法，都有画面。')}</span><span className="auth-frame-symbol">▰ ▰ ▰</span></div>
        </div>
        <div className="auth-artwork-detail">
          <svg viewBox="0 0 160 200" fill="none"><path fill="#dce4c5" d="M0 0h160v200H0z" /><circle cx="105" cy="57" r="27" fill="#f5f2dd" /><path d="M0 125c46-52 72-50 160 20v55H0Z" fill="#9aad8c" /><path d="M0 175c76-87 93-39 160-67v92H0Z" fill="#416953" /><path d="M13 32V13h19m96 0h19v19M13 168v19h19m96 0h19v-19" stroke="#f5f2dd" /></svg>
        </div>
      </div>
    </div>
    <div className="auth-brand-bottom"><p>{t('一个想法，无限种表达。')}</p><span>{t('图像 · 分镜 · 短片')}</span></div>
  </aside>
}
