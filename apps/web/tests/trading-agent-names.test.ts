import assert from 'node:assert/strict'
import test from 'node:test'
import { fetchAgentApprovalName } from '../src/agents/approval-config.ts'
import { buildHyperliquidApproveAgentAction, buildHyperliquidApproveAgentTypedData } from '../src/agents/hyperliquid-agent.ts'
import { buildAsterApproveAgentAction, buildAsterApproveAgentTypedData, buildAsterApproveBuilderTypedData } from '../src/agents/aster-agent.ts'

const owner = '0x1111111111111111111111111111111111111111'
const agent = '0x2222222222222222222222222222222222222222'
const now = 1_800_000_000_000

test('backend approval config names survive JSON parsing and typed data unchanged', async () => {
  const original = globalThis.fetch
  try {
    for (const venue of ['hyperliquid', 'aster'] as const) {
      const names = venue === 'hyperliquid' ? ['Orbital Markets', 'OrbitalStg'] : ['OrbitalMarkets', 'OrbitalStg']
      for (const name of names) {
        globalThis.fetch = async (url) => {
          assert.equal(String(url), `/api/v1/live/agents/${venue}/approval-config`)
          return new Response(JSON.stringify({ agent_name: name }))
        }
        const configured = await fetchAgentApprovalName(venue)
        if (venue === 'hyperliquid') {
          const action = buildHyperliquidApproveAgentAction(agent, 1, now, configured)
          assert.equal(buildHyperliquidApproveAgentTypedData(action).message.agentName, name)
          assert.equal(JSON.parse(JSON.stringify(action)).agentName, name)
        } else {
          const action = buildAsterApproveAgentAction(owner, agent, now, configured)
          assert.equal(buildAsterApproveAgentTypedData(action).message.AgentName, name)
          assert.equal(buildAsterApproveBuilderTypedData(action).message.BuilderName, 'OrbitalMarkets')
          assert.equal(JSON.parse(JSON.stringify(action)).agentName, name)
        }
      }
    }
  } finally {
    globalThis.fetch = original
  }
})

test('every allowed Hyperliquid agent name fits the 16-character venue limit', () => {
  for (const name of ['Orbital Markets', 'OrbitalStg']) {
    const action = buildHyperliquidApproveAgentAction(agent, 1, now, name)
    assert.ok([...action.agentName].length <= 16, `${name} exceeds the Hyperliquid name limit`)
  }
})

test('approval builders reject every name outside each fixed allowlist before signing', () => {
  for (const name of ['', 'Other', 'Orbital Markets Stg ', 'OrbitalStg\n', 'OrbitalMarketsStg', 'Orbital Markets valid_until 9999999999', 'OrbitalData', null, undefined, 123]) {
    assert.throws(() => buildHyperliquidApproveAgentAction(agent, 1, now, name), /invalid Hyperliquid agent name/)
    assert.throws(() => buildAsterApproveAgentAction(owner, agent, now, name), /invalid Aster agent name/)
    const hl = buildHyperliquidApproveAgentAction(agent, 1, now, 'Orbital Markets')
    Reflect.set(hl, 'agentName', name)
    assert.throws(() => buildHyperliquidApproveAgentTypedData(hl), /invalid Hyperliquid agent name/)
    const ast = buildAsterApproveAgentAction(owner, agent, now, 'OrbitalMarkets')
    Reflect.set(ast, 'agentName', name)
    assert.throws(() => buildAsterApproveAgentTypedData(ast), /invalid Aster agent name/)
  }
  assert.throws(() => buildHyperliquidApproveAgentAction(agent, 1, now, 'Orbital Markets Stg'), /invalid Hyperliquid agent name/)
  assert.throws(() => buildAsterApproveAgentAction(owner, agent, now, 'Orbital Markets Stg'), /invalid Aster agent name/)
})
