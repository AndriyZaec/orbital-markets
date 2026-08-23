const posthogKey = import.meta.env.VITE_POSTHOG_KEY
const posthogHost = import.meta.env.VITE_POSTHOG_HOST || 'https://eu.i.posthog.com'

type PostHogClient = typeof import('posthog-js').default
type PendingEvent = {
  event: AnalyticsEvent
  properties?: AnalyticsProperties
}

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

export type AnalyticsProperties = NonNullable<Parameters<PostHogClient['capture']>[1]>

let client: PostHogClient | null = null
let loading: Promise<PostHogClient | null> | null = null
let pendingEvents: PendingEvent[] = []

export function initAnalytics(): void {
  if (client || loading || !posthogKey) return

  loading = import('posthog-js')
    .then(({ default: posthog }) => {
      posthog.init(posthogKey, {
        api_host: posthogHost,
        autocapture: false,
        capture_pageview: false,
        disable_session_recording: true,
        person_profiles: 'never',
      })
      client = posthog
      const events = pendingEvents
      pendingEvents = []
      events.forEach(({ event, properties }) => client?.capture(event, properties))
      return posthog
    })
    .catch(() => {
      pendingEvents = []
      return null
    })
    .finally(() => {
      loading = null
    })
}

export function trackAnalytics(event: AnalyticsEvent, properties?: AnalyticsProperties): void {
  if (!posthogKey) return
  if (client) {
    client.capture(event, properties)
    return
  }
  if (pendingEvents.length < 20) pendingEvents.push({ event, properties })
  initAnalytics()
}
