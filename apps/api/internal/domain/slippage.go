package domain

// SlippageLevel classifies execution cost severity.
type SlippageLevel string

const (
	SlippageOK   SlippageLevel = "ok"   // <= 1%
	SlippageWarn SlippageLevel = "warn" // > 1%, <= 3%
	SlippageHigh SlippageLevel = "high" // > 3%, <= 5%
	SlippageBlock SlippageLevel = "block" // > 5%, not executable
)

// ClassifySlippage returns the canonical slippage level for a given entry cost.
// entryCost is a fraction (e.g. 0.02 = 2%).
func ClassifySlippage(entryCost float64) SlippageLevel {
	switch {
	case entryCost <= 0.01:
		return SlippageOK
	case entryCost <= 0.03:
		return SlippageWarn
	case entryCost <= 0.05:
		return SlippageHigh
	default:
		return SlippageBlock
	}
}

// SlippageExecutable returns true if the slippage level allows execution.
func SlippageExecutable(level SlippageLevel) bool {
	return level != SlippageBlock
}
