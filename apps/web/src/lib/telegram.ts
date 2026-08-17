export function validatedTelegramURL(value: string | undefined): string {
  if (!value) throw new Error('Telegram link was not returned')
  let url: URL
  try {
    url = new URL(value)
  } catch {
    throw new Error('Invalid Telegram link')
  }
  if (url.protocol !== 'https:' || url.hostname !== 't.me') {
    throw new Error('Invalid Telegram link')
  }
  return url.toString()
}
