export function validatedTelegramURL(value: string | undefined): string {
  if (!value) throw new Error('Telegram link was not returned')
  const url = new URL(value)
  if (url.protocol !== 'https:' || url.hostname !== 't.me') {
    throw new Error('Invalid Telegram link')
  }
  return url.toString()
}
