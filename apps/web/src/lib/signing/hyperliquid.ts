import type { SignTypedDataParameters } from 'wagmi/actions'

interface HyperliquidTypedData {
  domain: SignTypedDataParameters['domain']
  types: SignTypedDataParameters['types']
  primaryType: string
  message: Record<string, unknown>
}

export async function signHyperliquid(
  unsignedPayload: unknown,
  signTypedDataAsync: (params: {
    domain: SignTypedDataParameters['domain']
    types: SignTypedDataParameters['types']
    primaryType: string
    message: Record<string, unknown>
  }) => Promise<string>,
): Promise<string> {
  const typed = unsignedPayload as HyperliquidTypedData
  if (!typed?.primaryType || !typed.types?.[typed.primaryType]) {
    throw new Error('Invalid Hyperliquid EIP-712 signing payload')
  }
  return signTypedDataAsync({
    domain: typed.domain,
    types: typed.types,
    primaryType: typed.primaryType,
    message: typed.message,
  })
}
