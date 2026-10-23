export function parseMinorAmount(value: string, digits: number): number | null {
  if (!Number.isInteger(digits) || digits < 0 || digits > 3 || !/^(0|[1-9]\d*)(\.\d+)?$/.test(value)) return null
  const [whole, fraction = ''] = value.split('.')
  if (fraction.length > digits) return null
  const minor = BigInt(whole) * 10n ** BigInt(digits) + BigInt(fraction.padEnd(digits, '0') || '0')
  return minor <= BigInt(Number.MAX_SAFE_INTEGER) ? Number(minor) : null
}
export function minorAmountInput(value: number, digits: number): string {
  if (!Number.isSafeInteger(value) || value < 0) return ''
  const text = String(value).padStart(digits + 1, '0')
  return digits ? `${text.slice(0, -digits)}.${text.slice(-digits)}` : text
}
