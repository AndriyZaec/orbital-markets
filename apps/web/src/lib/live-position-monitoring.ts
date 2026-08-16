interface MonitoredFill {
  leg: number
  venue: string
}

export function monitoredLegVenues(
  fills: MonitoredFill[],
  fallbackLeg1Venue: string,
  fallbackLeg2Venue: string,
): [string, string] {
  return [
    fills.find((fill) => fill.leg === 1)?.venue ?? fallbackLeg1Venue,
    fills.find((fill) => fill.leg === 2)?.venue ?? fallbackLeg2Venue,
  ]
}
