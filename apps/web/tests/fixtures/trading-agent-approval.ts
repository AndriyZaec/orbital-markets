// Read-only local contract fixture: no wallet provider or remote venue calls.
import { readFileSync } from 'node:fs'
import assert from 'node:assert/strict'
import { privateKeyToAccount } from 'viem/accounts'
import { fetchAgentApprovalName } from '../../src/agents/approval-config.ts'
import { buildHyperliquidApproveAgentAction, buildHyperliquidApproveAgentTypedData } from '../../src/agents/hyperliquid-agent.ts'
import { buildAsterApproveAgentAction, buildAsterApproveAgentTypedData, buildAsterApproveBuilderTypedData } from '../../src/agents/aster-agent.ts'

const input = JSON.parse(readFileSync(0, 'utf8')) as { venue: 'hyperliquid' | 'aster'; config: string }
globalThis.fetch = async (url) => {
  assert.equal(String(url), `/api/v1/live/agents/${input.venue}/approval-config`)
  return new Response(input.config, { headers: { 'Content-Type': 'application/json' } })
}
const name = await fetchAgentApprovalName(input.venue)
const owner = privateKeyToAccount('0x0123456789012345678901234567890123456789012345678901234567890123')
const agent = '0x2222222222222222222222222222222222222222'
if (input.venue === 'hyperliquid') {
  const action = buildHyperliquidApproveAgentAction(agent, 1, Date.now(), name)
  const signature = await owner.signTypedData(buildHyperliquidApproveAgentTypedData(action))
  process.stdout.write(JSON.stringify({
    owner_address: owner.address, action,
    signature: { r: signature.slice(0, 66), s: `0x${signature.slice(66, 130)}`, v: Number.parseInt(signature.slice(130), 16) },
  }))
} else {
  const action = buildAsterApproveAgentAction(owner.address, agent, Date.now(), name)
  const signature = await owner.signTypedData(buildAsterApproveAgentTypedData(action))
  const builderSignature = await owner.signTypedData(buildAsterApproveBuilderTypedData(action))
  process.stdout.write(JSON.stringify({ ...action, signature, builderSignature }))
}
