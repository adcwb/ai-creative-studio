import {expect, test} from 'vitest'
import {errorGuidance, type ErrorCategory} from './errorPresentation'

const categories: ErrorCategory[] = ['input', 'access', 'conflict', 'upstream', 'configuration', 'storage', 'interrupted', 'internal']

test('every backend category maps to a Chinese explanation and a next step', () => {
  for (const category of categories) {
    const result = errorGuidance(category)
    expect(result.title, category).toMatch(/[㐀-鿿]/)
    expect(result.next, category).toMatch(/[㐀-鿿]/)
    expect(result.action).toBeTruthy()
  }
  expect(errorGuidance('input').action).toBe('fix')
  expect(errorGuidance('configuration').action).toBe('settings')
  expect(errorGuidance('conflict').action).toBe('refresh')
  expect(errorGuidance('interrupted').action).toBe('verify')
})

test('an unknown category still explains something rather than leaking a code', () => {
  const withCode = errorGuidance('not-a-category', 'provider_error')
  expect(withCode.title).toMatch(/[㐀-鿿]/)
  expect(withCode.title).not.toContain('provider_error')
  expect(errorGuidance(undefined, undefined).action).toBe('contact')
})

test('billing risk is flagged for interrupted work and uncertain upstream codes', () => {
  expect(errorGuidance('interrupted').mayBill).toBe(true)
  for (const code of ['provider_timeout', 'execution_interrupted', 'execution_outcome_unknown', 'provider_error', 'invalid_provider_response']) {
    expect(errorGuidance('upstream', code).mayBill, code).toBe(true)
  }
  expect(errorGuidance('input', 'invalid_input').mayBill).toBe(false)
  expect(errorGuidance('access', 'forbidden').mayBill).toBe(false)
})