import {ConfigProvider, theme as antTheme, type ThemeConfig} from 'antd'
import zhCN from 'antd/locale/zh_CN'
import enUS from 'antd/locale/en_US'
import {useLayoutEffect, type ReactNode} from 'react'
import {useI18n} from '../i18n'
import {defaultTheme, rememberAppearance, type Theme} from '../preferences'
import {applyTheme, colorSchemes, palettes} from './palettes'
import {useMobile} from './useMobile'
import './studio-ui.css'

export function StudioTheme({children, appearance = defaultTheme}: {children: ReactNode; appearance?: Theme}) {
  const {locale} = useI18n()
  const mobile = useMobile()
  const color = palettes[appearance]
  // The same palette drives antd tokens and the CSS custom properties, so the
  // two can never disagree. Cleanup restores the previous appearance, which is
  // what lets the account shell hand control back to the boot shell on logout.
  useLayoutEffect(() => {
    const restore = applyTheme(appearance, document.documentElement)
    rememberAppearance(appearance)
    return restore
  }, [appearance])
  const config: ThemeConfig = {
    algorithm: colorSchemes[appearance] === 'dark' ? antTheme.darkAlgorithm : antTheme.defaultAlgorithm,
    token: {
      colorPrimary:color.primary, colorPrimaryHover:color.primaryHover, colorPrimaryActive:color.primaryActive,
      colorInfo:color.primary, colorSuccess:color.successText, colorWarning:color.warningText, colorError:color.errorText,
      colorTextBase:color.ink, colorText:color.ink, colorTextSecondary:color.inkMuted,
      colorTextPlaceholder:color.inkPlaceholder, colorTextLabel:color.ink, colorTextLightSolid:color.onPrimary,
      colorBgBase:color.canvas, colorBgLayout:color.canvas, colorBgContainer:color.surface,
      colorBgElevated:color.surfaceSoft, colorBgSpotlight:color.forest,
      colorBorder:color.controlBorder, colorBorderSecondary:color.border, colorFillAlter:color.surfaceSoft,
      colorLink:color.link,
      fontFamily:'"PingFang SC", "Microsoft YaHei", system-ui, sans-serif', fontSize:14,
      borderRadius:8, controlHeight:mobile ? 44 : 40, controlOutlineWidth:2, wireframe:false,
    },
    components: {
      // Hover darkens instead of lightening: lightening the dark-theme primary
      // drops white label contrast below the 4.5:1 gate.
      Button: {primaryColor:color.onPrimary,defaultBg:color.surface,defaultColor:color.ink,defaultBorderColor:color.controlBorder,controlHeightSM:30,contentFontSizeSM:12},
      Input: {colorBgContainer:color.surfaceInput},
      Select: {colorBgContainer:color.surfaceInput,optionSelectedBg:color.surfaceSoft,optionSelectedColor:color.ink},
      Tabs: {itemSelectedColor:color.link,itemHoverColor:color.link,inkBarColor:color.rail,horizontalMargin:'0 0 20px 0',horizontalItemGutter:mobile ? 22 : 32},
      Checkbox: {controlInteractiveSize:14},
      Tag: {colorSuccess:color.successText,colorSuccessBg:color.successBg,colorSuccessBorder:color.successText,defaultBg:color.surfaceSoft,defaultColor:color.ink},
      // On narrow screens the gateway table becomes a stacked grid, so cell
      // padding and rules are removed here rather than by overriding .ant-*.
      Table: {headerBg:color.surfaceSoft,headerColor:color.inkMuted,rowHoverBg:color.surfaceSoft,borderColor:mobile ? 'transparent' : color.border,colorBgContainer:color.surface,cellPaddingBlock:mobile ? 0 : 16,cellPaddingInline:mobile ? 0 : 16},
      Modal: {headerBg:color.surface,contentBg:color.surface,titleColor:color.ink},
      Card: {colorBgContainer:color.surface},
      Collapse: {headerBg:color.surfaceSoft,contentBg:color.surface},
      Alert: {colorSuccessBg:color.successBg,colorSuccessBorder:color.successText,colorWarningBg:color.warningBg,colorWarningBorder:color.warningBorder,colorErrorBg:color.errorBg,colorErrorBorder:color.errorBorder,colorInfoBg:color.surfaceSoft,colorInfoBorder:color.controlBorder},
    },
  }
  return <ConfigProvider locale={locale === 'en' ? enUS : zhCN} theme={config} button={{autoInsertSpace:false}} getPopupContainer={trigger => trigger?.closest<HTMLElement>('.studio-dialog-content, dialog, .user-workspace') ?? document.body}>{children}</ConfigProvider>
}