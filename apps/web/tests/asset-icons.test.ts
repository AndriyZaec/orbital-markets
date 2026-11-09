import assert from 'node:assert/strict'
import test from 'node:test'

import { assetIconUrls, parsePhoenixAssetIcons } from '../src/lib/asset-icons.ts'

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

test('appends Phoenix metadata after existing sources without guessing a CDN hash', () => {
  const url = 'https://phoenixcdn.trade/logos/BP/example.svg'
  assert.deepEqual(assetIconUrls('bp', url), [
    'https://app.hyperliquid.xyz/coins/BP.svg',
    'https://app.pacifica.fi/imgs/optimized/tokens/BP.922b1f81fd98703c.svg',
    url,
  ])
})

test('reads optional Phoenix logos using native symbols and preserves multiplied contract identity', () => {
  const dell = 'https://phoenixcdn.trade/logos/DELL/example.svg'
  const pepe = 'https://phoenixcdn.trade/logos/kPEPE/example.svg'
  assert.deepEqual(parsePhoenixAssetIcons({ markets: [
    { symbol: 'DELL-PERP', metadata: { logoUri: dell } },
    { symbol: 'kPEPE', metadata: { logoUri: pepe } },
    { symbol: 'MON', metadata: null },
    { symbol: 'BTC', metadata: { logoUri: null } },
  ] }), { DELL: dell, KPEPE: pepe })
})

test('ignores malformed catalogs and unsupported image URLs', () => {
  for (const payload of [null, [], {}, { markets: null }]) assert.deepEqual(parsePhoenixAssetIcons(payload), {})
  assert.deepEqual(parsePhoenixAssetIcons({ markets: [
    null,
    { symbol: 123, metadata: { logoUri: 'https://phoenixcdn.trade/logo.svg' } },
    { symbol: 'MON', metadata: { logoUri: 'not a URL' } },
    { symbol: 'BTC', metadata: { logoUri: 'http://phoenixcdn.trade/logo.svg' } },
    { symbol: 'DELL', metadata: { logoUri: 'https://example.com/logo.svg' } },
    { symbol: 'GOLD', metadata: { logoUri: 'https://user:password@phoenixcdn.trade/logo.svg' } },
  ] }), {})
})
