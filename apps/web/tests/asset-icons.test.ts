import assert from 'node:assert/strict'
import test from 'node:test'

import { assetIconUrls } from '../src/lib/asset-icons.ts'

test('falls back to Pacifica for assets missing from Hyperliquid', () => {
  assert.deepEqual(assetIconUrls('BP'), [
    'https://app.hyperliquid.xyz/coins/BP.svg',
    'https://app.pacifica.fi/imgs/optimized/tokens/BP.922b1f81fd98703c.svg',
  ])
})

test('uses the underlying token symbol for multiplied contracts', () => {
  assert.deepEqual(assetIconUrls('KPEPE'), [
    'https://app.hyperliquid.xyz/coins/PEPE.svg',
  ])
})
