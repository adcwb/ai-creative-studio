import { expect, it } from 'vitest'
import { formatMinorCurrency, minorAmountInput, parseMinorAmount } from './money'
it('parses currency precision without floating point multiplication', () => {
  expect(parseMinorAmount('0.29', 2)).toBe(29)
  expect(parseMinorAmount('123', 0)).toBe(123)
  expect(parseMinorAmount('1.234', 3)).toBe(1234)
  expect(parseMinorAmount('1.001', 2)).toBeNull()
  expect(parseMinorAmount('-1', 2)).toBeNull()
  expect(parseMinorAmount('1e5', 2)).toBeNull()
  expect(parseMinorAmount('9007199254740992', 0)).toBeNull()
  expect(minorAmountInput(29, 2)).toBe('0.29')
  expect(minorAmountInput(123, 0)).toBe('123')
})

it('formats maximum safe minor units without losing cents in any locale', () => {
  expect(formatMinorCurrency(9007199254740991, 'USD', 2, 'en')).toBe('$90,071,992,547,409.91')
  expect(formatMinorCurrency(9007199254740991, 'USD', 2, 'zh-CN')).toBe('US$90,071,992,547,409.91')
  expect(formatMinorCurrency(9007199254740991, 'JPY', 0, 'en')).toBe('¥9,007,199,254,740,991')
  expect(formatMinorCurrency(9007199254740991, 'KWD', 3, 'en')).toBe('KWD\u00a09,007,199,254,740.991')
  expect(formatMinorCurrency(1, 'USD', 2, 'en')).toBe('$0.01')
  expect(formatMinorCurrency(-1, 'USD', 2, 'en')).toBe('-$0.01')
  expect(formatMinorCurrency(0, 'KWD', 3, 'en')).toBe('KWD\u00a00.000')
  expect(() => formatMinorCurrency(9007199254740992, 'USD', 2, 'en')).toThrow(RangeError)
})
