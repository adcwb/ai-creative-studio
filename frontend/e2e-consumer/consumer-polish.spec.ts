import { expect, test, type APIRequestContext, type Locator } from '@playwright/test';
import { selectOption, expectControlValue } from '../e2e/support/controls';
import {navigateTo, closeNavigation} from '../e2e/support/navigation';
const fixture = 'http://127.0.0.1:5189';
async function ready(request: APIRequestContext) {
    const initial = await (await request.get(fixture + '/api/v1/model-management')).json();
    const features = [{ key: 'image_generation', name: '图像创作', capability: 'image' }, { key: 'prompt_optimization', name: '提示词优化', capability: 'text' }, ...initial.features];
    const model = { id: 'm1', gateway_id: 'g1', name: '创作测试模型', model: 'synthetic-image', enabled: true, capabilities: ['text', 'image'], version: 1, max_output_tokens: 512, json_output: true, video_seconds: 4, video_size: '1280x720', currency: 'USD', maximum_minor: 0, price_source: '' };
    await request.post(fixture + '/__test__/reset', { data: { deploymentAdmin: true, management: { ...initial, features, gateways: [{ id: 'g1', name: '测试网关', base_url: 'https://gateway.example.invalid/v1', enabled: true, version: 1, timeout_seconds: 30, secret_status: 'encrypted' }], models: [model], bindings: features.map(f => ({ feature_key: f.key, model_config_id: model.id, enabled: true, allow_override: true, version: 1 })) } } });
}
test.beforeEach(async ({ request }) => { await request.post(fixture + '/__test__/reset'); await ready(request); });
async function settle(locator: Locator) { await locator.evaluate(async (element) => { await document.fonts.ready; await new Promise<void>(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))); await Promise.all(element.getAnimations({ subtree: true }).filter(animation => animation.effect?.getTiming().iterations !== Infinity).map(animation => animation.finished.catch(() => { }))); }); }
async function contrast(locator: Locator) {
    return locator.evaluate(element => {
        const style = getComputedStyle(element);
        let parent = element;
        while (getComputedStyle(parent).backgroundColor === 'rgba(0, 0, 0, 0)' && parent.parentElement)
            parent = parent.parentElement;
        const luminance = (color: string) => { const a = color.match(/[\d.]+/g)!.slice(0, 3).map(Number).map(v => { v /= 255; return v <= .04045 ? v / 12.92 : ((v + .055) / 1.055) ** 2.4; }); return .2126 * a[0] + .7152 * a[1] + .0722 * a[2]; };
        const [low, high] = [luminance(style.color), luminance(getComputedStyle(parent).backgroundColor)].sort((a, b) => a - b);
        return (high + .05) / (low + .05);
    });
}
test('the image prompt and primary action are visible before templates and preserve a chosen template', async ({ page }, info) => {
    await page.goto('/studio');
    const prompt = page.getByLabel('描述你的画面', { exact: true });
    await prompt.fill('一位年轻旅人在森林工作室画画，晨光柔和。');
    const action = page.getByRole('button', { name: '创建图像', exact: true });
    await expect(action).toBeEnabled();
    await expect(prompt).toBeInViewport();
    await expect(action).toBeInViewport();
    const box = (await action.boundingBox())!;
    expect(box.y + box.height).toBeLessThanOrEqual(page.viewportSize()!.height);
    await expect(page.locator('.compact-runtime-picker')).not.toHaveAttribute('open', '');
    await page.screenshot({ path: info.outputPath('image-first-screen.png') });
    await page.locator('.template-card').first().click();
    await expect(prompt).toHaveValue('春日咖啡海报');
    await expect(prompt).toBeFocused();
    await page.locator('.compact-runtime-picker>summary').click();
    await expect(page.getByRole('combobox', { name: 'AI 网关', exact: true }).first()).toBeVisible();
    await page.locator('.compact-runtime-picker>summary').click();
    await expect(prompt).toHaveValue('春日咖啡海报');
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
});
test('missing model errors stay visible when the compact summary is toggled', async ({ page, request }) => {
    const initial = await (await request.get(fixture + '/api/v1/model-management')).json();
    await request.post(fixture + '/__test__/reset', { data: { management: { ...initial, bindings: [] } } });
    await page.goto('/studio');
    const picker = page.locator('.creation-controls > .compact-runtime-control');
    const error = picker.getByRole('alert');
    await expect(error).toContainText('此功能尚未绑定网关模型');
    await picker.locator('summary').click();
    await expect(error).toBeVisible();
    await expect(picker.locator('details')).toHaveAttribute('open', '');
    await page.getByLabel('描述你的画面', { exact: true }).fill('森林里的旅人');
    await expect(page.getByRole('button', { name: '创建图像', exact: true })).toBeDisabled();
});
test('prompt recipe actions stay visible while the form scrolls and a real fixture save retains the recipe', async ({ page }, info) => {
    await page.route('**/api/v1/prompt-templates?**', async (route) => { if (route.request().method() !== 'POST')
        return route.fulfill({ json: { items: [], next_cursor: '' } }); const body = route.request().postDataJSON(); expect(body.version.body).toBe('森林中的旅人，背景是清晨薄雾。'); expect(route.request().headers()['idempotency-key']).toBeTruthy(); await route.fulfill({ status: 201, json: { template: { ...body, id: 'prompt-1', space_id: 'space-1', owner_id: 'user-1', head_version_id: 'version-1', row_version: 1 }, version: { ...body.version, id: 'version-1', template_id: 'prompt-1', number: 1 } } }); });
    await page.goto('/studio');
    await page.getByLabel('描述你的画面', { exact: true }).fill('森林中的旅人，背景是清晨薄雾。');
    await page.getByRole('button', { name: '保存当前描述', exact: true }).click();
    const dialog = page.getByRole('dialog', { name: '保存自己的提示词', exact: true });
    await expect(dialog).toBeVisible();
    await settle(dialog);
    const save = dialog.getByRole('button', { name: '保存提示词', exact: true });
    await expect(save).toBeInViewport();
    await expect(dialog.getByRole('button', { name: '关闭提示词编辑', exact: true })).toBeInViewport();
    await dialog.getByLabel('提示词名称', { exact: true }).fill('森林人物光线');
    await dialog.locator('.studio-dialog-body').evaluate(node => node.scrollTop = node.scrollHeight);
    await expect(save).toBeInViewport();
    await expect(dialog.getByRole('button', { name: '关闭提示词编辑', exact: true })).toBeInViewport();
    await page.screenshot({ path: info.outputPath('prompt-actions.png') });
    const sent = page.waitForResponse(r => r.url().includes('/prompt-templates?') && r.request().method() === 'POST');
    await save.click();
    expect((await sent).status()).toBe(201);
    await expect(dialog).toHaveCount(0);
    await expect(page.getByLabel('描述你的画面', { exact: true })).toHaveValue('森林中的旅人，背景是清晨薄雾。');
});
for (const theme of ['forest', 'paper', 'screening'])
    test(`${theme} gateway statuses and selected tabs keep contrast after transitions`, async ({ page }, info) => {
        await page.addInitScript(theme => localStorage.setItem('studio.preferences.v1:user-1', theme), theme);
        await page.goto('/settings/models');
        await expect(page.locator('.user-workspace')).toHaveAttribute('data-theme', theme);
        await page.getByRole('tab', { name: 'AI 网关', exact: true }).click();
        await expect(page.locator('.ant-tag').first()).toBeVisible();
        await settle(page.locator('.user-workspace'));
        for (const tag of await page.locator('.ant-tag').all())
            expect(await contrast(tag)).toBeGreaterThanOrEqual(4.5);
        expect(await contrast(page.getByRole('tab', { name: 'AI 网关', exact: true }))).toBeGreaterThanOrEqual(4.5);
        await page.screenshot({ path: info.outputPath(`gateway-${theme}.png`) });
    });
test('binding filters and mobile summaries retain an unsaved row and keep navigation protection', async ({ page }, info) => {
    await page.goto('/settings/models');
    const row = page.locator('.feature-binding-table tbody tr').filter({ hasText: '剧本生成' });
    await expect(row).toBeVisible();
    if (info.project.name === 'mobile') {
        await expect(row.getByRole('button', { name: '展开配置', exact: true })).toBeVisible();
        await row.getByRole('button', { name: '展开配置', exact: true }).click();
    }
    await row.getByRole('checkbox', { name: '允许本次切换', exact: true }).uncheck();
    await page.getByLabel('查找创作功能', { exact: true }).fill('图像');
    await expect(row).toBeHidden();
    await page.getByLabel('查找创作功能', { exact: true }).fill('');
    await expect(row).toBeVisible();
    await expect(row.getByRole('checkbox', { name: '允许本次切换', exact: true })).not.toBeChecked();
    await selectOption(page.getByRole('combobox', { name: '按能力筛选', exact: true }), 'image');
    await expect(row).toBeHidden();
    await selectOption(page.getByRole('combobox', { name: '按能力筛选', exact: true }), '');
    await expect(row).toBeVisible();
    await navigateTo(page, '/assets');
    const guard=page.getByRole('dialog',{name:'离开当前编辑？',exact:true});
    await expect(guard).toBeVisible();
    await guard.getByRole('button',{name:'继续编辑',exact:true}).click();
    await closeNavigation(page);
    await expect(page).toHaveURL(/\/settings\/models$/);
    await expect(row.getByRole('checkbox',{name:'允许本次切换',exact:true})).not.toBeChecked();
    await page.getByRole('checkbox', { name: '仅看待配置', exact: true }).check();
    await expect(page.getByText('没有符合筛选条件的功能', { exact: true })).toBeVisible();
    await page.getByRole('checkbox', { name: '仅看待配置', exact: true }).uncheck();
    await row.getByRole('button', { name: '保存绑定', exact: true }).click();
    await expect(row.getByRole('button', { name: '保存绑定', exact: true })).toBeDisabled();
    await page.screenshot({ path: info.outputPath('bindings-filter.png') });
});
test('storyboard and workflow put creative content within reach and expanded canvas preserves selection', async ({ page }, info) => {
    await page.route('**/projects/*/workflows?*', r => r.fulfill({ json: { items: [], next_cursor: '' } }));
    await page.route('**/projects/*/workflow-runs?*', r => r.fulfill({ json: { items: [], next_cursor: '' } }));
    await page.goto('/projects/story-1/storyboard');
    await expect(page.locator('.storyboard-shot').first()).toBeVisible();
    await expect(page.locator('.storyboard-shot').first()).toBeInViewport();
    await page.screenshot({ path: info.outputPath('storyboard-first-screen.png') });
    await page.goto('/workflows/story-1');
    if (info.project.name === 'mobile') {
        expect((await page.getByRole('combobox',{name:'已保存流程',exact:true}).boundingBox())!.width).toBeGreaterThan(200);
        await expect(page.getByRole('button', { name: '步骤列表', exact: true })).toHaveAttribute('aria-pressed', 'true');
        await expect(page.locator('.workflow-step-list')).toHaveAttribute('open', '');
        await expect(page.locator('.workflow-step-list button').first()).toBeInViewport();
        const stepBox = (await page.locator('.workflow-step-list button').first().boundingBox())!;
        expect(stepBox.y + stepBox.height).toBeLessThanOrEqual(page.viewportSize()!.height);
        await expect(page.locator('.workflow-canvas')).toHaveCount(0);
    }
    else {
        await expect(page.locator('.workflow-canvas')).toBeInViewport();
        expect((await page.locator('.workflow-canvas').boundingBox())!.y).toBeLessThan(650);
    }
    await page.screenshot({path: info.outputPath('workflow-first-screen.png')});
    await page.getByRole('button', { name: '展开画布', exact: true }).click();
    const dialog = page.getByRole('dialog', { name: '创作流程画布', exact: true });
    await expect(dialog.locator('.react-flow__node')).toHaveCount(4);
    await settle(dialog);
    if (info.project.name === 'mobile')
        for (const control of await dialog.locator('.react-flow__controls button').all())
            expect((await control.boundingBox())!.height).toBeGreaterThanOrEqual(44);
    await page.screenshot({ path: info.outputPath('workflow-expanded.png') });
    await page.keyboard.press('Escape');
    await expect(dialog).toHaveCount(0);
    await expectControlValue(page.getByLabel('步骤名称', { exact: true }), '描述与文案');
});
test('empty editing and publication consent show deliberate actions rather than empty canvases', async ({ page }, info) => {
    await page.route('**/projects/*/media/upload-sessions?*', r => r.fulfill({ json: { items: [], next_after: '' } }));
    await page.goto('/projects/story-1/media');
    await expect(page.getByRole('heading', { name: '先准备本集素材', exact: true })).toBeVisible();
    await expect(page.locator('.media-editing')).not.toHaveAttribute('open', '');
    await expect(page.locator('.media-poster')).not.toHaveAttribute('open', '');
    await expect(page.locator('.media-studio canvas:visible')).toHaveCount(0);
    await page.screenshot({ path: info.outputPath('editing-empty.png') });
    await page.goto('/publish/story-1');
    const consent = page.getByLabel('我拥有素材及相关人物、音乐的分享权利。', { exact: true });
    await expect(consent).toBeAttached();
    const width = await consent.evaluate(n => n.getBoundingClientRect().width);
    expect(width).toBeLessThanOrEqual(24);
    await expect(consent.locator('..')).toHaveCSS('display', 'flex');
    await page.screenshot({ path: info.outputPath('publication-consent.png'), fullPage: true });
});
test('settings have distinct shortcuts, one team creation entry and comfortable config actions', async ({ page }, info) => {
    await page.goto('/settings/preferences');
    await expect(page.getByRole('link', { name: '打开后端配置', exact: true })).toBeInViewport();
    await page.getByRole('link', { name: '打开后端配置', exact: true }).click();
    await expect(page.locator('#config-FRONTEND_ORIGIN')).toBeVisible();
    const restore = page.getByRole('button', { name: '恢复环境值', exact: true }).first();
    if (info.project.name === 'mobile')
        expect((await restore.boundingBox())!.height).toBeGreaterThanOrEqual(44);
    await page.goto('/settings/organization');
    await expect(page.getByLabel('新组织名称')).toHaveCount(0);
    await expect(page.getByRole('list', { name: '当前空间成员', exact: true })).toBeVisible();
    await page.getByRole('link', { name: '管理团队空间', exact: true }).click();
    await expect(page.getByLabel('团队名称', { exact: true })).toBeVisible();
    await page.screenshot({ path: info.outputPath('team-settings.png') });
});
