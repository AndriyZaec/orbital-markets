import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import App from './App.tsx'
import { WalletProviders } from './providers/WalletProviders'
import { GateProvider } from './providers/GateProvider'
import { VenueReadinessProvider } from './hooks/useVenueReadiness'
import { LiveExecutionProvider } from './hooks/useLiveExecution'
import { TradingAgentProvider } from './agents/TradingAgentProvider'
import { LivePositionsProvider } from './hooks/useLivePositions'
import { VenueAccountsProvider } from './accounts/VenueAccountsProvider'
import { MigrateVenueAccounts } from './accounts/MigrateVenueAccounts'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <GateProvider>
      <WalletProviders>
        <VenueAccountsProvider>
          <MigrateVenueAccounts />
          <TradingAgentProvider>
            <LiveExecutionProvider>
              <VenueReadinessProvider>
                <LivePositionsProvider><App /></LivePositionsProvider>
              </VenueReadinessProvider>
            </LiveExecutionProvider>
          </TradingAgentProvider>
        </VenueAccountsProvider>
      </WalletProviders>
    </GateProvider>
  </StrictMode>,
)
