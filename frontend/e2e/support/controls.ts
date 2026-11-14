import {expect, type Locator} from '@playwright/test'

type Choice = string | {value?: string; label?: string; index?: number}
// Exercise the visible control for both migrated Selects and native controls.
export async function selectOption(control: Locator, choice: Choice | Choice[], options?: {timeout?: number}) {
  if (await control.evaluate(element => element.tagName === 'SELECT')) return control.selectOption(choice, options)
  const selected = Array.isArray(choice) ? choice[0] : choice
  await control.click(options)
  const dropdown = control.page().locator('.ant-select-dropdown:visible')
  if (typeof selected === 'string' || selected.value !== undefined) {
    const value = typeof selected === 'string' ? selected : selected.value!
    await dropdown.locator(`[data-select-value=${JSON.stringify(value)}]`).click(options)
  } else if (selected.label !== undefined) await dropdown.getByRole('option', {name:selected.label,exact:true}).click(options)
  else await dropdown.getByRole('option').nth(selected.index ?? 0).click(options)
}

export async function expectControlValue(control: Locator, value: string | RegExp, options?: {timeout?: number}) {
  await control.waitFor({state:'attached',...options})
  const select = control.locator('xpath=ancestor-or-self::*[contains(concat(" ", normalize-space(@class), " "), " studio-select ")][1]')
  if (await select.count()) await expect(select).toHaveAttribute('data-selected-value',value,options)
  else await expect(control).toHaveValue(value,options)
}

export async function expectSelectOptions(control: Locator, count: number) {
  await control.click()
  await expect(control.page().locator('.ant-select-dropdown:visible [role="option"]')).toHaveCount(count)
  await control.press('Escape')
}
