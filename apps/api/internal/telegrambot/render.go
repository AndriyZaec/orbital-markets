package telegrambot

import (
	"fmt"
	"html"
	"math"
	"strings"
	"time"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/domain"
)

const opportunitiesPerPage = 5

func renderOpportunities(snapshotID string, snapshot opportunitySnapshot, page int, appURL string, now time.Time) (string, InlineKeyboardMarkup) {
	totalPages := max(1, (len(snapshot.opportunities)+opportunitiesPerPage-1)/opportunitiesPerPage)
	page = min(max(page, 0), totalPages-1)

	var text strings.Builder
	text.WriteString("<b>Top Opportunities</b>\n")
	text.WriteString("Updated " + formatAge(latestDetection(snapshot.opportunities), now) + "\n")
	if len(snapshot.opportunities) == 0 {
		text.WriteString("\nNo opportunities available.")
	} else {
		start := page * opportunitiesPerPage
		end := min(start+opportunitiesPerPage, len(snapshot.opportunities))
		for index, opportunity := range snapshot.opportunities[start:end] {
			longVenue, shortVenue := opportunityVenues(opportunity)
			fmt.Fprintf(&text,
				"\n<b>%d. %s · %s APR</b>\nLong %s / Short %s\nFunding: %s / %s per hour\nSize: %s",
				start+index+1,
				html.EscapeString(opportunity.Asset),
				formatPercent(opportunity.AnnualizedGrossEdge, 2),
				html.EscapeString(titleVenue(longVenue)),
				html.EscapeString(titleVenue(shortVenue)),
				formatPercent(fundingForVenue(opportunity, longVenue), 4),
				formatPercent(fundingForVenue(opportunity, shortVenue), 4),
				formatUSD(opportunity.RecommendedNotional),
			)
			if opportunity.ExecutionStatus != "executable" {
				text.WriteString("\nStatus: Blocked")
			}
			text.WriteByte('\n')
		}
	}

	keyboard := InlineKeyboardMarkup{}
	if totalPages > 1 {
		row := make([]InlineKeyboardButton, 0, 3)
		if page > 0 {
			row = append(row, InlineKeyboardButton{Text: "<", CallbackData: opportunityPageCallback(snapshotID, page-1)})
		}
		row = append(row, InlineKeyboardButton{Text: fmt.Sprintf("%d / %d", page+1, totalPages), CallbackData: "noop"})
		if page+1 < totalPages {
			row = append(row, InlineKeyboardButton{Text: ">", CallbackData: opportunityPageCallback(snapshotID, page+1)})
		}
		keyboard.InlineKeyboard = append(keyboard.InlineKeyboard, row)
	}
	keyboard.InlineKeyboard = append(keyboard.InlineKeyboard, []InlineKeyboardButton{
		{Text: "Refresh", CallbackData: "opportunities:refresh"},
		{Text: "Open in Orbital", URL: appURL},
	})
	return strings.TrimSpace(text.String()), keyboard
}

func opportunityPageCallback(snapshotID string, page int) string {
	return fmt.Sprintf("opportunities:%s:%d", snapshotID, page)
}

func opportunityVenues(opportunity domain.Opportunity) (string, string) {
	if opportunity.Direction == domain.DirectionLongA {
		return opportunity.VenuePair.VenueA, opportunity.VenuePair.VenueB
	}
	return opportunity.VenuePair.VenueB, opportunity.VenuePair.VenueA
}

func fundingForVenue(opportunity domain.Opportunity, venue string) float64 {
	if strings.EqualFold(opportunity.VenuePair.VenueA, venue) {
		return opportunity.FundingRateA
	}
	return opportunity.FundingRateB
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
