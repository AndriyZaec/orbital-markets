export function formatSignedUsdPnL(value: number) {
  const sign = value < 0 ? '-' : '+'
  return `${sign}$${Math.abs(value).toFixed(2)}`
}
