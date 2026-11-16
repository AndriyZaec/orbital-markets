import { useEffect, useRef, useState } from 'react'
import { useWallet } from '@solana/wallet-adapter-react'
import { useWalletModal } from '@solana/wallet-adapter-react-ui'
import { useAccount, useConfig, useConnect, useDisconnect } from 'wagmi'
import { getConnections } from 'wagmi/actions'
import type { Venue } from '@/agents/types'
import { trackAnalytics } from '@/lib/analytics'
import { useVenueAccounts } from './useVenueAccounts'

type ConnectIntent = { venue: Venue; previousAddress: string | null; linkedAddress?: string }

export function useConnectVenue() {
  const { accounts, link } = useVenueAccounts()
  const solana = useWallet()
  const solanaModal = useWalletModal()
  const evm = useAccount()
  const config = useConfig()
  const { connectAsync, connectors } = useConnect()
  const { disconnectAsync } = useDisconnect()
  const [choice, setChoice] = useState<{ venue: Venue; address: string } | null>(null)
  const [picker, setPicker] = useState<ConnectIntent | null>(null)
  const [feedback, setFeedback] = useState<{ venue: Venue; text: string } | null>(null)
  const pendingSolana = useRef<ConnectIntent | null>(null)
  const solanaModalWasVisible = useRef(false)
  const solanaAddress = solana.connected ? solana.publicKey?.toBase58() ?? null : null

  const complete = (intent: ConnectIntent, address: string) => {
    if (intent.linkedAddress) {
      const matches = intent.venue === 'aster' || intent.venue === 'hyperliquid'
        ? intent.linkedAddress.toLowerCase() === address.toLowerCase() : intent.linkedAddress === address
      setFeedback(matches ? null : { venue: intent.venue, text: `Switch to ${intent.linkedAddress.slice(0, 6)}…${intent.linkedAddress.slice(-4)} in your wallet` })
      return
    }
    link(intent.venue, address)
    trackAnalytics('wallet_connected', { venue: intent.venue })
    const same = intent.previousAddress && (intent.venue === 'aster' || intent.venue === 'hyperliquid'
      ? intent.previousAddress.toLowerCase() === address.toLowerCase() : intent.previousAddress === address)
    setFeedback(same ? { venue: intent.venue, text: 'Switch accounts in your wallet to use a different address.' } : null)
  }

  useEffect(() => {
    if (solanaModal.visible) solanaModalWasVisible.current = true
    const intent = pendingSolana.current
    if (!intent) return
    if (solanaAddress) {
      pendingSolana.current = null
      // The wallet boundary updates asynchronously; only an explicit intent links.
      void Promise.resolve().then(() => complete(intent, solanaAddress))
    } else if (solanaModalWasVisible.current && !solanaModal.visible && !solana.connecting) {
      // Selection closes the picker before the extension's approval resolves.
      // Only dismissal or a finished failed connection cancels the intent.
      pendingSolana.current = null
    }
  })

  const connectEvm = async (intent: ConnectIntent, connector: typeof connectors[number]) => {
    try {
      const result = await connectAsync({ connector })
      if (!intent.linkedAddress) {
        for (const { connector: other } of getConnections(config)) {
          if (other.uid !== connector.uid) await disconnectAsync({ connector: other })
        }
      }
      complete(intent, result.accounts[0])
    } catch {
      // Wallet cancellation leaves existing venue bindings and agents untouched.
    }
  }

  const openPicker = (intent: ConnectIntent) => {
    if (intent.venue === 'pacifica' || intent.venue === 'phoenix') {
      pendingSolana.current = intent
      solanaModalWasVisible.current = false
      solanaModal.setVisible(true)
    } else if (connectors.length === 1) {
      void connectEvm(intent, connectors[0])
    } else {
      setPicker(intent)
    }
  }

  const connect = async (venue: Venue) => {
    setFeedback(null)
    const address = venue === 'pacifica' || venue === 'phoenix' ? solanaAddress : evm.isConnected ? evm.address ?? null : null
    const linkedAddress = accounts[venue]
    if (linkedAddress) {
      const intent = { venue, previousAddress: null, linkedAddress }
      const matches = address && (venue === 'aster' || venue === 'hyperliquid'
        ? address.toLowerCase() === linkedAddress.toLowerCase() : address === linkedAddress)
      if (matches) { complete(intent, address); return }
      try {
        if (address) {
          if (venue === 'pacifica' || venue === 'phoenix') await solana.disconnect()
          else if (evm.connector) await disconnectAsync({ connector: evm.connector })
        }
        openPicker(intent)
      } catch {
        setFeedback({ venue, text: 'Unable to disconnect the wallet. Please try again.' })
      }
      return
    }
    if (address) setChoice({ venue, address })
    else openPicker({ venue, previousAddress: null })
  }

  const useConnected = () => {
    if (choice) complete({ venue: choice.venue, previousAddress: null }, choice.address)
    setChoice(null)
  }

  const useAnother = async () => {
    if (!choice) return
    const intent = { venue: choice.venue, previousAddress: choice.address }
    setChoice(null)
    try {
      if (intent.venue === 'pacifica' || intent.venue === 'phoenix') await solana.disconnect()
      else for (const { connector } of getConnections(config)) await disconnectAsync({ connector })
      openPicker(intent)
    } catch {
      setFeedback({ venue: intent.venue, text: 'Unable to disconnect the wallet. Please try again.' })
    }
  }

  return { connect, choice, closeChoice: () => setChoice(null), useConnected, useAnother, picker, connectors, feedback,
    closePicker: () => setPicker(null),
    pickConnector: (uid: string) => {
      const connector = connectors.find((candidate) => candidate.uid === uid)
      if (picker && connector) void connectEvm(picker, connector)
      setPicker(null)
    },
  }
}
