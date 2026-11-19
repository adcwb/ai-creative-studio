import React from 'react'
import ReactDOM from 'react-dom/client'
import App from './App'
import './style.css'
import { I18nProvider } from './components/LanguagePicker'
import {StudioTheme} from './ui/StudioTheme'
import {readAppearanceHint} from './preferences'
// The boot shell paints in the last used appearance so the sign-in screen and
// the first frame after a reload do not flash. The account shell takes over as
// soon as the session resolves.
ReactDOM.createRoot(document.getElementById('root')!).render(<React.StrictMode><I18nProvider><StudioTheme appearance={readAppearanceHint()}><App /></StudioTheme></I18nProvider></React.StrictMode>)
