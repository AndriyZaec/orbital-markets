import bs58 from 'bs58'

export async function signPacifica(
  unsignedPayload: unknown,
  signMessage: (message: Uint8Array) => Promise<Uint8Array>,
): Promise<string> {
  const payloadBytes = new TextEncoder().encode(JSON.stringify(unsignedPayload))
  const signature = await signMessage(payloadBytes)
  return bs58.encode(signature)
}
