import {ConfigProvider, theme as antTheme, type ThemeConfig} from 'antd'
import zhCN from 'antd/locale/zh_CN'
import enUS from 'antd/locale/en_US'
import type {ReactNode} from 'react'
import {useI18n} from '../i18n'
import type {Theme} from '../preferences'
import './studio-ui.css'

const palettes = {
  forest: {primary:'#244b3b',hover:'#35604a',text:'#263e32',muted:'#63705e',surface:'#ffffff',input:'#fbfcf8',border:'#7f8d78',line:'#e0e5d9',soft:'#f0f3eb'},
  paper: {primary:'#80543c',hover:'#68412c',text:'#3a312c',muted:'#716254',surface:'#fffdf8',input:'#faf6ee',border:'#93816e',line:'#d9cfc1',soft:'#ece5d9'},
  screening: {primary:'#4e6b7f',hover:'#49677b',text:'#f0eeea',muted:'#b2b8bf',surface:'#23272c',input:'#1c2025',border:'#788593',line:'#424850',soft:'#2e333a'},
} satisfies Record<Theme, object>

export function StudioTheme({children, appearance = 'forest'}: {children: ReactNode; appearance?: Theme}) {
  const {locale} = useI18n()
  const color = palettes[appearance]
  const config: ThemeConfig = {
    algorithm: appearance === 'screening' ? antTheme.darkAlgorithm : antTheme.defaultAlgorithm,
    token: {
      colorPrimary:color.primary, colorPrimaryHover:color.hover, colorPrimaryActive:color.hover,
      colorInfo:color.primary, colorText:color.text, colorTextSecondary:color.muted,
      colorTextPlaceholder:color.muted, colorTextLabel:color.text,
      colorBgContainer:color.surface, colorBgElevated:color.surface,
      colorBorder:color.border, colorBorderSecondary:color.line, colorFillAlter:color.soft,
      fontFamily:'"PingFang SC", "Microsoft YaHei", system-ui, sans-serif', fontSize:14,
      borderRadius:8, controlHeight:40, controlOutlineWidth:2,
    },
    components: {
      Button: {primaryColor:'#ffffff',defaultBg:color.surface},
      Input: {colorBgContainer:color.input},
      Select: {colorBgContainer:color.input,optionSelectedBg:color.soft,optionSelectedColor:color.text},
      Table: {headerBg:color.soft,headerColor:color.muted,rowHoverBg:color.soft},
    },
  }
  return <ConfigProvider locale={locale === 'en' ? enUS : zhCN} theme={config} button={{autoInsertSpace:false}} getPopupContainer={trigger => trigger?.closest<HTMLElement>('.studio-dialog-content, dialog, .user-workspace') ?? document.body}>{children}</ConfigProvider>
}
