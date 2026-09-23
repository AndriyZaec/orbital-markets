import type { Address } from 'viem'

const zeroAddress = '0x0000000000000000000000000000000000000000' as const
const ownerChainId = 56

export interface AsterApproveAgentAction {
  user: Address
  nonce: number
  agentName: string
  agentAddress: Address
  ipWhitelist: string
  expired: number
  canSpotTrade: boolean
  canPerpTrade: boolean
  canWithdraw: boolean
  asterChain: 'Mainnet'
  signatureChainId: typeof ownerChainId
}

export function buildAsterApproveAgentTypedData(action: AsterApproveAgentAction) {
  return {
    domain: {
      name: 'AsterSignTransaction',
      version: '1',
      chainId: BigInt(ownerChainId),
      verifyingContract: zeroAddress,
    },
    types: {
      EIP712Domain: [
        { name: 'name', type: 'string' },
        { name: 'version', type: 'string' },
        { name: 'chainId', type: 'uint256' },
        { name: 'verifyingContract', type: 'address' },
      ] as const,
      ApproveAgent: [
        { name: 'AgentName', type: 'string' },
        { name: 'AgentAddress', type: 'string' },
        { name: 'IpWhitelist', type: 'string' },
        { name: 'Expired', type: 'uint256' },
        { name: 'CanSpotTrade', type: 'bool' },
        { name: 'CanPerpTrade', type: 'bool' },
        { name: 'CanWithdraw', type: 'bool' },
        { name: 'AsterChain', type: 'string' },
        { name: 'User', type: 'string' },
        { name: 'Nonce', type: 'uint256' },
      ] as const,
    },
    primaryType: 'ApproveAgent' as const,
    message: {
      AgentName: action.agentName,
      AgentAddress: action.agentAddress,
      IpWhitelist: action.ipWhitelist,
      Expired: BigInt(action.expired),
      CanSpotTrade: action.canSpotTrade,
      CanPerpTrade: action.canPerpTrade,
      CanWithdraw: action.canWithdraw,
      AsterChain: action.asterChain,
      User: action.user,
      Nonce: BigInt(action.nonce),
    },
  }
}
