export interface PositionOpportunityContext {
  opportunity_id: string
  asset: string
  venue_a: string
  venue_b: string
}

interface OpportunityContext {
  id: string
  asset: string
  venue_pair: {
    venue_a: string
    venue_b: string
  }
}

function hasSameVenuePair(opportunity: OpportunityContext, position: PositionOpportunityContext) {
  const { venue_a: venueA, venue_b: venueB } = opportunity.venue_pair
  return (venueA === position.venue_a && venueB === position.venue_b)
    || (venueA === position.venue_b && venueB === position.venue_a)
}

export function findPositionOpportunity<T extends OpportunityContext>(
  opportunities: T[],
  position: PositionOpportunityContext,
): T | null {
  return opportunities.find((opportunity) => opportunity.id === position.opportunity_id)
    ?? opportunities.find((opportunity) => (
      opportunity.asset === position.asset && hasSameVenuePair(opportunity, position)
    ))
    ?? null
}
