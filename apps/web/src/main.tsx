import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import App from './App.tsx'
import { WalletProviders } from './providers/WalletProviders'
import { GateProvider } from './providers/GateProvider'
import { VenueReadinessProvider } from './hooks/useVenueReadiness'
import { LiveExecutionProvider } from './hooks/useLiveExecution'
import { TradingAgentProvider } from './agents/TradingAgentProvider'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <GateProvider>
      <WalletProviders>
        <TradingAgentProvider>
          <LiveExecutionProvider>
            <VenueReadinessProvider>
              <App />
            </VenueReadinessProvider>
          </LiveExecutionProvider>
        </TradingAgentProvider>
      </WalletProviders>
    </GateProvider>
  </StrictMode>,
)
