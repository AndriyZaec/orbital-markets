import posthog from 'posthog-js'

const posthogKey = import.meta.env.VITE_POSTHOG_KEY
const posthogHost = import.meta.env.VITE_POSTHOG_HOST || 'https://eu.i.posthog.com'

export type AnalyticsEvent =
  | 'landing_view'
  | 'access_cta_clicked'
  | 'waitlist_submitted'
  | 'app_opened'
  | 'wallet_connected'
  | 'agent_authorized'
  | 'accounts_ready'
  | 'opportunity_viewed'
  | 'live_open_started'
  | 'live_open_succeeded'
  | 'live_open_failed'
  | 'position_degraded'
  | 'position_closed'

export type AnalyticsProperties = NonNullable<Parameters<typeof posthog.capture>[1]>

let initialized = false

export function initAnalytics(): void {
  if (initialized || !posthogKey) return

  posthog.init(posthogKey, {
    api_host: posthogHost,
    autocapture: false,
    capture_pageview: false,
    disable_session_recording: true,
    person_profiles: 'never',
  })
  initialized = true
}

export function trackAnalytics(event: AnalyticsEvent, properties?: AnalyticsProperties): void {
  initAnalytics()
  if (!initialized) return
  posthog.capture(event, properties)
}
