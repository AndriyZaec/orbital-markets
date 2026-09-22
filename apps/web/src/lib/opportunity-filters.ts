type VenuePair = {
  venue_a: string
  venue_b: string
}

export function matchesVenueFilter(venuePair: VenuePair, selectedVenues: readonly string[]) {
  const selected = new Set(selectedVenues.map((venue) => venue.toLowerCase()))
  return selected.has(venuePair.venue_a.toLowerCase())
    && selected.has(venuePair.venue_b.toLowerCase())
}

export function enforceMinimumVenueSelection(
  current: readonly string[],
  next: readonly string[],
  minimum = 2,
) {
  return [...(next.length >= minimum ? next : current)]
}
