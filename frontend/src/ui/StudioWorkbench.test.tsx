import {renderToStaticMarkup} from 'react-dom/server'
import type {ReactNode} from 'react'
import {expect, test, vi} from 'vitest'
import {LocaleContext, loadLocale, type Locale} from '../i18n'
import {UserPreferences} from '../components/UserPreferences'
import {StudioWorkbench} from './StudioWorkbench'
import {StickyActionBar} from './StickyActionBar'
import {NextStepDock} from './NextStepDock'

// These cases assert the workbench's own layout contract, not antd's rendering
// (the browser suites cover that). Stubbing the library keeps the unit run from
// transforming the whole antd module graph.
vi.mock('antd', () => ({
  ConfigProvider: ({children}: {children: ReactNode}) => children,
  Modal: () => null,
  Button: () => null,
  theme: {darkAlgorithm: {}, defaultAlgorithm: {}},
}))
vi.mock('antd/locale/zh_CN', () => ({default: {}}))
vi.mock('antd/locale/en_US', () => ({default: {}}))

const slots = {outlineTitle: '场次大纲', inspectorTitle: '本场检查器', outline: <p>outline-body</p>, inspector: <p>inspector-body</p>}
// useI18n reads LocaleContext, whose default locale is captured at module load.
// Drive it explicitly instead of relying on setActiveLocale.
function inLocale(locale: Locale, node: ReactNode) {
  return renderToStaticMarkup(<LocaleContext.Provider value={{locale, setLocale: () => {}}}>{node}</LocaleContext.Provider>)
}

test('工作台按存在的插槽组合列轨，缺失的面板不占列', () => {
  const both = renderToStaticMarkup(<StudioWorkbench {...slots}>canvas</StudioWorkbench>)
  expect(both).toContain('--workbench-columns:var(--workbench-outline) minmax(0, 1fr) var(--workbench-inspector)')
  expect(both).toContain('outline-body')
  expect(both).toContain('inspector-body')
  const outlineOnly = renderToStaticMarkup(<StudioWorkbench outlineTitle={slots.outlineTitle} inspectorTitle={slots.inspectorTitle} outline={slots.outline}>canvas</StudioWorkbench>)
  expect(outlineOnly).toContain('--workbench-columns:var(--workbench-outline) minmax(0, 1fr)')
  expect(outlineOnly).not.toContain('var(--workbench-inspector)')
  const bare = renderToStaticMarkup(<StudioWorkbench outlineTitle={slots.outlineTitle} inspectorTitle={slots.inspectorTitle}>canvas</StudioWorkbench>)
  expect(bare).toContain('--workbench-columns:minmax(0, 1fr)')
  expect(bare).not.toContain('studio-workbench-panel')
})

test('阅读限宽是显式选项，默认不限制卡片网格', () => {
  expect(renderToStaticMarkup(<StudioWorkbench {...slots}>canvas</StudioWorkbench>)).not.toContain('data-measure')
  expect(renderToStaticMarkup(<StudioWorkbench {...slots} measure>canvas</StudioWorkbench>)).toContain('data-measure="true"')
})

test('吸顶栏与推进条都带可访问名称', () => {
  const bar = renderToStaticMarkup(<StickyActionBar label="剧本操作" status={<span>已保存</span>}><button type="button">保存</button></StickyActionBar>)
  expect(bar).toContain('aria-label="剧本操作"')
  expect(bar).toContain('studio-sticky-status')
  expect(bar).toContain('已保存')
  const dock = renderToStaticMarkup(<NextStepDock hint={<span>提示</span>}><a href="/next">下一步</a></NextStepDock>)
  expect(dock).toContain('studio-next-step-actions')
  expect(dock).toContain('aria-label=')
})

test('面板操作名称与推进条名称都有英文文案', async () => {
  await loadLocale('en')
  const workbench = inLocale('en', <StudioWorkbench {...slots}>canvas</StudioWorkbench>)
  expect(workbench).toContain('aria-label="Collapse outline"')
  expect(workbench).toContain('aria-label="Collapse inspector"')
  expect(inLocale('en', <NextStepDock>next</NextStepDock>)).toContain('aria-label="Next step"')
  const zh = inLocale('zh-CN', <StudioWorkbench {...slots}>canvas</StudioWorkbench>)
  expect(zh).toContain('aria-label="收起大纲"')
})

test('面板收起后只留展开轨，不挂载该面板内容', () => {
  const expanded = renderToStaticMarkup(<StudioWorkbench {...slots}>canvas</StudioWorkbench>)
  expect(expanded).toContain('aria-expanded="true"')
  expect(expanded).toContain('studio-workbench-panel-body')
  vi.stubGlobal('localStorage', {
    getItem: (key: string) => key.endsWith(':panels') ? '{"outline":true,"inspector":false}' : null,
    setItem: () => {},
  })
  const collapsed = renderToStaticMarkup(<UserPreferences userID="panel-test"><StudioWorkbench {...slots}>canvas</StudioWorkbench></UserPreferences>)
  expect(collapsed).toContain('--workbench-columns:var(--workbench-rail) minmax(0, 1fr) var(--workbench-inspector)')
  expect(collapsed).toContain('studio-workbench-rail')
  expect(collapsed).toContain('aria-expanded="false"')
  expect(collapsed).not.toContain('outline-body')
  expect(collapsed).toContain('inspector-body')
  vi.unstubAllGlobals()
})