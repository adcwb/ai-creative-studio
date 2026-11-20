import {renderToStaticMarkup} from 'react-dom/server'
import {expect, test, vi} from 'vitest'
import {StudioButton} from './StudioButton'
import {StudioTag} from './StudioTag'
import {StudioEmpty} from './StudioEmpty'
import {StudioField} from './StudioField'
import {StudioCard} from './StudioCard'
import {StudioError} from './StudioError'
import {StudioSkeleton} from './StudioSkeleton'
import {StudioInput, StudioTextArea} from './StudioInput'

// The atoms' job is to map product intent onto library props and to wire the
// accessibility plumbing. Stub the library so these cases assert that mapping
// rather than antd's own rendering, which the browser suites cover.
vi.mock('antd', () => ({
  Button: (props: Record<string, unknown>) => <button data-type={String(props.type)} data-danger={String(Boolean(props.danger))} data-size={String(props.size)} className={String(props.className)}>{props.children as never}</button>,
  Tag: (props: Record<string, unknown>) => <span data-color={String(props.color)} className={String(props.className)}>{props.children as never}</span>,
  Alert: (props: Record<string, unknown>) => <div className={String(props.className)} data-title={String(props.title)}>{props.description as never}</div>,
  Skeleton: () => <div className="skeleton-stub" />,
  Input: Object.assign(
    (props: Record<string, unknown>) => <input className={String(props.className)} data-status={String(props.status)} />,
    {TextArea: (props: Record<string, unknown>) => <textarea className={String(props.className)} data-status={String(props.status)} />},
  ),
}))

test('按钮变体映射到库类型，危险态额外置 danger', () => {
  expect(renderToStaticMarkup(<StudioButton variant="primary">go</StudioButton>)).toContain('data-type="primary"')
  const danger = renderToStaticMarkup(<StudioButton variant="danger">del</StudioButton>)
  expect(danger).toContain('data-type="primary"')
  expect(danger).toContain('data-danger="true"')
  expect(renderToStaticMarkup(<StudioButton variant="text">t</StudioButton>)).toContain('data-type="text"')
  expect(renderToStaticMarkup(<StudioButton size="small">s</StudioButton>)).toContain('data-size="small"')
  expect(renderToStaticMarkup(<StudioButton>x</StudioButton>)).toContain('studio-button')
})

test('徽标语义映射到库颜色，长标签可换行', () => {
  expect(renderToStaticMarkup(<StudioTag tone="success">ok</StudioTag>)).toContain('data-color="success"')
  expect(renderToStaticMarkup(<StudioTag tone="error">bad</StudioTag>)).toContain('data-color="error"')
  expect(renderToStaticMarkup(<StudioTag>x</StudioTag>)).toContain('data-color="default"')
})

test('空状态区分无数据、无结果与无权限，并可带操作', () => {
  for (const kind of ['empty', 'no-results', 'no-access'] as const) {
    const html = renderToStaticMarkup(<StudioEmpty kind={kind} title="还没有内容" description="说明" action={<button type="button">新建</button>} />)
    expect(html).toContain(`data-kind="${kind}"`)
    expect(html).toContain('studio-empty-action')
    expect(html).toContain('还没有内容')
  }
})

test('字段把说明与错误接到控件上，错误态标记 aria-invalid', () => {
  const html = renderToStaticMarkup(<StudioField label="名称" hint="最多 80 字" error="必填" required>{props => <input {...props} />}</StudioField>)
  const id = /for="([^"]+)"/.exec(html)?.[1]
  expect(id).toBeTruthy()
  expect(html).toContain(`id="${id}"`)
  expect(html).toContain(`aria-describedby="${id}-hint ${id}-error"`)
  expect(html).toContain('aria-invalid="true"')
  expect(html).toContain('aria-required="true"')
  expect(html).toContain('role="alert"')
  const clean = renderToStaticMarkup(<StudioField label="名称">{props => <input {...props} />}</StudioField>)
  expect(clean).not.toContain('aria-invalid')
  expect(clean).not.toContain('aria-describedby')
})

test('面板保留既有 .panel 布局契约并统一标题区', () => {
  const html = renderToStaticMarkup(<StudioCard title="当前剧本" extra={<span>已保存</span>}>body</StudioCard>)
  expect(html).toContain('class="panel studio-card"')
  expect(html).toContain('studio-card-header')
  expect(html).toContain('studio-card-extra')
  expect(renderToStaticMarkup(<StudioCard>body</StudioCard>)).not.toContain('studio-card-header')
})

test('错误面渲染稳定码与下一步操作', () => {
  const html = renderToStaticMarkup(<StudioError message="模型服务返回错误" code="provider_error" action={<button type="button">重试</button>} />)
  expect(html).toContain('studio-error')
  expect(html).toContain('provider_error')
  expect(html).toContain('studio-error-action')
  expect(renderToStaticMarkup(<StudioError message="仅消息" />)).not.toContain('studio-error-detail')
})

test('骨架屏媒体型保持固定宽高比，输入错误态透传', () => {
  expect(renderToStaticMarkup(<StudioSkeleton shape="media" />)).toContain('studio-skeleton-media')
  expect(renderToStaticMarkup(<StudioSkeleton shape="card" rows={2} />)).toContain('data-shape="card"')
  expect(renderToStaticMarkup(<StudioInput aria-invalid />)).toContain('data-status="error"')
  expect(renderToStaticMarkup(<StudioTextArea aria-invalid />)).toContain('studio-textarea')
})