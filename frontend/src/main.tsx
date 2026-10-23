import React from 'react'
import ReactDOM from 'react-dom/client'
import App from './App'
import './style.css'
import { I18nProvider } from './components/LanguagePicker'
ReactDOM.createRoot(document.getElementById('root')!).render(<React.StrictMode><I18nProvider><App /></I18nProvider></React.StrictMode>)
