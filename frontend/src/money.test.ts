import { expect, it } from 'vitest'
import { minorAmountInput, parseMinorAmount } from './money'
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
