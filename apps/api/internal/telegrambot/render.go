package telegrambot

import (
	"fmt"
	"html"
	"math"
	"strings"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/executor"
)

const opportunitiesPerPage = 5
const positionsPerPage = 5

func renderOpportunities(snapshotID string, snapshot opportunitySnapshot, page int, appURL string, now time.Time) (string, InlineKeyboardMarkup) {
	totalPages := max(1, (len(snapshot.opportunities)+opportunitiesPerPage-1)/opportunitiesPerPage)
	page = min(max(page, 0), totalPages-1)

	var text strings.Builder
	text.WriteString("<b>🚀 Opportunities</b>\n")
	if len(snapshot.opportunities) > 0 {
		text.WriteString("Updated " + formatAge(latestDetection(snapshot.opportunities), now) + "\n")
	}
	if len(snapshot.opportunities) == 0 {
		text.WriteString("\nNo opportunities available.")
	} else {
		start := page * opportunitiesPerPage
		end := min(start+opportunitiesPerPage, len(snapshot.opportunities))
		for index, opportunity := range snapshot.opportunities[start:end] {
			longVenue, shortVenue := opportunityVenues(opportunity)
			fmt.Fprintf(&text,
				"\n<b>%d. %s · %s APR</b>\n↗️ Long %s → ↘️ Short %s\n💵 Suggested size: %s",
				start+index+1,
				html.EscapeString(opportunity.Asset),
				formatPercent(opportunity.AnnualizedGrossEdge, 2),
				html.EscapeString(titleVenue(longVenue)),
				html.EscapeString(titleVenue(shortVenue)),
				formatUSD(opportunity.RecommendedNotional),
			)
			if opportunity.ExecutionStatus != "executable" {
				text.WriteString("\n⚠️ Not executable")
			}
			text.WriteByte('\n')
		}
	}

	keyboard := InlineKeyboardMarkup{}
	if totalPages > 1 {
		row := make([]InlineKeyboardButton, 0, 3)
		if page > 0 {
			row = append(row, InlineKeyboardButton{Text: "‹", CallbackData: opportunityPageCallback(snapshotID, page-1)})
		}
		row = append(row, InlineKeyboardButton{Text: fmt.Sprintf("%d / %d", page+1, totalPages), CallbackData: "noop"})
		if page+1 < totalPages {
			row = append(row, InlineKeyboardButton{Text: "›", CallbackData: opportunityPageCallback(snapshotID, page+1)})
		}
		keyboard.InlineKeyboard = append(keyboard.InlineKeyboard, row)
	}
	appendDashboardControls(&keyboard, "opportunities:refresh", appURL)
	return strings.TrimSpace(text.String()), keyboard
}

func opportunityPageCallback(snapshotID string, page int) string {
	return fmt.Sprintf("opportunities:%s:%d", snapshotID, page)
}

func renderPositions(
	positions []executor.LivePosition,
	page int,
	appURL string,
	now time.Time,
) (string, InlineKeyboardMarkup) {
	totalPages := max(1, (len(positions)+positionsPerPage-1)/positionsPerPage)
	page = min(max(page, 0), totalPages-1)

	var text strings.Builder
	text.WriteString("<b>📊 Active Positions</b>\n")
	if len(positions) > 0 {
		text.WriteString("Updated " + formatAge(latestPositionUpdate(positions), now) + "\n")
	}
	keyboard := InlineKeyboardMarkup{}
	if len(positions) == 0 {
		text.WriteString("\nNo active positions found for the linked accounts.")
	} else {
		start := page * positionsPerPage
		end := min(start+positionsPerPage, len(positions))
		for index, position := range positions[start:end] {
			absoluteIndex := start + index
			liquidationRisk := worstLiquidationRisk(position)
			fmt.Fprintf(&text,
				"\n<b>%s %d. %s · %s</b>\n💰 PnL %s · %s Liq %s\n%s · %.1fx\n",
				positionStateEmoji(position.State),
				absoluteIndex+1,
				html.EscapeString(position.Asset),
				html.EscapeString(titleState(position.State)),
				formatSignedUSD(position.TotalPnL),
				liquidationRiskEmoji(liquidationRisk),
				html.EscapeString(liquidationRisk),
				formatUSD(position.Notional),
				position.Leverage,
			)
			keyboard.InlineKeyboard = append(keyboard.InlineKeyboard, []InlineKeyboardButton{{
				Text:         fmt.Sprintf("%d · %s", absoluteIndex+1, position.Asset),
				CallbackData: positionDetailCallback(page, telegramPositionToken(position.ID)),
			}})
		}
	}

	if totalPages > 1 {
		row := make([]InlineKeyboardButton, 0, 3)
		if page > 0 {
			row = append(row, InlineKeyboardButton{Text: "‹", CallbackData: positionPageCallback(page - 1)})
		}
		row = append(row, InlineKeyboardButton{Text: fmt.Sprintf("%d / %d", page+1, totalPages), CallbackData: "noop"})
		if page+1 < totalPages {
			row = append(row, InlineKeyboardButton{Text: "›", CallbackData: positionPageCallback(page + 1)})
		}
		keyboard.InlineKeyboard = append(keyboard.InlineKeyboard, row)
	}
	appendDashboardControls(&keyboard, "positions:refresh", appURL)
	return strings.TrimSpace(text.String()), keyboard
}

func renderPositionDetail(
	position executor.LivePosition,
	page int,
	appURL string,
	now time.Time,
) (string, InlineKeyboardMarkup) {
	var text strings.Builder
	fmt.Fprintf(&text,
		"%s <b>%s · %s</b>\n%s / %s\n%s · %.1fx · %s\n\n💰 <b>PnL %s</b>\nPrice %s · Funding %s (%s)\n\n⚖️ Mismatch %s\n📈 Spread %s APR\n\n🛡 <b>Liquidation</b>\n%s %s · %s\n%s %s · %s\n\nUpdated %s",
		positionStateEmoji(position.State),
		html.EscapeString(position.Asset),
		html.EscapeString(titleState(position.State)),
		html.EscapeString(titleVenue(position.VenueA)),
		html.EscapeString(titleVenue(position.VenueB)),
		formatUSD(position.Notional),
		position.Leverage,
		formatHoldTime(position.HoldHours),
		formatSignedUSD(position.TotalPnL),
		formatSignedUSD(position.PricePnL),
		formatSignedUSD(position.FundingPnL),
		html.EscapeString(titleFundingSource(position.FundingPnLSource)),
		formatUnsignedPercent(position.HedgeMismatch, 2),
		formatPercent(position.CurrentSpread, 2),
		liquidationRiskEmoji(position.Leg1LiqRisk),
		html.EscapeString(titleVenue(position.VenueA)),
		formatLiquidation(position.Leg1LiqPrice, position.Leg1LiqDist, position.Leg1LiqRisk),
		liquidationRiskEmoji(position.Leg2LiqRisk),
		html.EscapeString(titleVenue(position.VenueB)),
		formatLiquidation(position.Leg2LiqPrice, position.Leg2LiqDist, position.Leg2LiqRisk),
		formatAge(positionUpdate(position), now),
	)
	keyboard := InlineKeyboardMarkup{InlineKeyboard: [][]InlineKeyboardButton{
		{{Text: "← Back to positions", CallbackData: positionPageCallback(page)}},
	}}
	appendDashboardControls(&keyboard, positionDetailCallback(page, telegramPositionToken(position.ID)), appURL)
	return text.String(), keyboard
}

func renderMissingPosition(page int, appURL string) (string, InlineKeyboardMarkup) {
	keyboard := InlineKeyboardMarkup{InlineKeyboard: [][]InlineKeyboardButton{
		{{Text: "← Back to positions", CallbackData: positionPageCallback(page)}},
	}}
	appendDashboardControls(&keyboard, "positions:refresh", appURL)
	return "<b>⚪ Position unavailable</b>\n\nIt no longer belongs to the linked accounts or is no longer available.", keyboard
}

func appendDashboardControls(keyboard *InlineKeyboardMarkup, refreshCallback, appURL string) {
	keyboard.InlineKeyboard = append(keyboard.InlineKeyboard,
		sectionNavigationRow(),
		[]InlineKeyboardButton{
			{Text: "↻ Refresh", CallbackData: refreshCallback},
			{Text: "Open Orbital", URL: appURL},
		},
	)
}

func mainMenuKeyboard(appURL string) InlineKeyboardMarkup {
	return InlineKeyboardMarkup{InlineKeyboard: [][]InlineKeyboardButton{
		sectionNavigationRow(),
		{{Text: "Open Orbital", URL: appURL}},
	}}
}

func sectionNavigationRow() []InlineKeyboardButton {
	return []InlineKeyboardButton{
		{Text: "🚀 Opportunities", CallbackData: "opportunities:refresh"},
		{Text: "📊 Positions", CallbackData: "positions:refresh"},
	}
}

func positionPageCallback(page int) string {
	return fmt.Sprintf("positions:%d", page)
}

func positionDetailCallback(page int, token string) string {
	return fmt.Sprintf("position:%d:%s", page, token)
}

func opportunityVenues(opportunity domain.Opportunity) (string, string) {
	if opportunity.Direction == domain.DirectionLongA {
		return opportunity.VenuePair.VenueA, opportunity.VenuePair.VenueB
	}
	return opportunity.VenuePair.VenueB, opportunity.VenuePair.VenueA
}

func latestDetection(opportunities []domain.Opportunity) time.Time {
	var latest time.Time
	for _, opportunity := range opportunities {
		if opportunity.DetectedAt.After(latest) {
			latest = opportunity.DetectedAt
		}
	}
	return latest
}

func formatAge(timestamp, now time.Time) string {
	if timestamp.IsZero() {
		return "unknown"
	}
	age := max(time.Duration(0), now.Sub(timestamp))
	if age < time.Minute {
		return fmt.Sprintf("%ds ago", int(age.Seconds()))
	}
	return fmt.Sprintf("%dm ago", int(age.Minutes()))
}

func formatPercent(value float64, decimals int) string {
	return fmt.Sprintf("%+.*f%%", decimals, value*100)
}

func formatUSD(value float64) string {
	abs := math.Abs(value)
	switch {
	case abs >= 1_000_000:
		return fmt.Sprintf("$%.2fm", value/1_000_000)
	case abs >= 1_000:
		return fmt.Sprintf("$%.2fk", value/1_000)
	default:
		return fmt.Sprintf("$%.2f", value)
	}
}

func formatSignedUSD(value float64) string {
	formatted := formatUSD(math.Abs(value))
	switch {
	case value > 0:
		return "+" + formatted
	case value < 0:
		return "-" + formatted
	default:
		return formatted
	}
}

func formatUnsignedPercent(value float64, decimals int) string {
	return fmt.Sprintf("%.*f%%", decimals, math.Abs(value)*100)
}

func formatHoldTime(hours float64) string {
	if hours <= 0 {
		return "--"
	}
	duration := time.Duration(hours * float64(time.Hour))
	if duration < time.Hour {
		return fmt.Sprintf("%dm", max(1, int(duration.Minutes())))
	}
	return fmt.Sprintf("%dh %dm", int(duration.Hours()), int(duration.Minutes())%60)
}

func formatLiquidation(liquidationPrice, distance float64, risk string) string {
	if liquidationPrice <= 0 {
		parts := []string{"liq unavailable"}
		if distance != 0 {
			parts = append(parts, formatLiquidationDistance(distance))
		}
		if strings.TrimSpace(risk) != "" {
			parts = append(parts, html.EscapeString(titleState(risk)))
		}
		return strings.Join(parts, " · ")
	}
	return fmt.Sprintf(
		"liq %s · %s · %s",
		formatPrice(liquidationPrice),
		formatLiquidationDistance(distance),
		html.EscapeString(titleState(risk)),
	)
}

func formatLiquidationDistance(value float64) string {
	if value < 0 {
		return fmt.Sprintf("past by %.1f%%", math.Abs(value)*100)
	}
	return formatUnsignedPercent(value, 1) + " away"
}

func positionStateEmoji(state string) string {
	switch strings.ToLower(state) {
	case "pending":
		return "⏳"
	case "open":
		return "🟢"
	case "degraded":
		return "🟠"
	case "closing":
		return "🔵"
	default:
		return "⚪"
	}
}

func liquidationRiskEmoji(risk string) string {
	switch strings.ToLower(risk) {
	case "safe":
		return "🟢"
	case "elevated":
		return "🟡"
	case "warning":
		return "🟠"
	case "critical":
		return "🔴"
	default:
		return "⚪"
	}
}

func formatPrice(value float64) string {
	decimals := 2
	if math.Abs(value) < 1 {
		decimals = 6
	}
	return fmt.Sprintf("$%.*f", decimals, value)
}

func latestPositionUpdate(positions []executor.LivePosition) time.Time {
	var latest time.Time
	for _, position := range positions {
		if updated := positionUpdate(position); updated.After(latest) {
			latest = updated
		}
	}
	return latest
}

func positionUpdate(position executor.LivePosition) time.Time {
	var latest time.Time
	for _, value := range []string{position.MonitorAt, position.UpdatedAt, position.CompletedAt, position.OpenedAt, position.StartedAt} {
		if timestamp, err := time.Parse(time.RFC3339Nano, value); err == nil && timestamp.After(latest) {
			latest = timestamp
		}
	}
	return latest
}

func worstLiquidationRisk(position executor.LivePosition) string {
	severity := map[string]int{"safe": 1, "elevated": 2, "warning": 3, "critical": 4}
	worst := ""
	for _, risk := range []string{position.Leg1LiqRisk, position.Leg2LiqRisk} {
		if severity[strings.ToLower(risk)] > severity[strings.ToLower(worst)] {
			worst = risk
		}
	}
	if worst == "" {
		return "Unavailable"
	}
	return titleState(worst)
}

func titleState(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "Unknown"
	}
	return strings.ToUpper(value[:1]) + strings.ToLower(value[1:])
}

func titleFundingSource(source string) string {
	switch strings.ToLower(source) {
	case "realized":
		return "realized"
	case "estimated":
		return "estimated"
	case "pending":
		return "pending"
	default:
		return "unknown"
	}
}

func titleVenue(venue string) string {
	switch strings.ToLower(venue) {
	case "hyperliquid":
		return "Hyperliquid"
	case "pacifica":
		return "Pacifica"
	default:
		if venue == "" {
			return venue
		}
		return strings.ToUpper(venue[:1]) + venue[1:]
	}
}
