// Test-only bridge for Rust helper -> Go signing metadata -> browser validation.
// It performs no RPC, signing, or submission and is excluded from normal builds.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/helper"
	"github.com/AndriyZaec/orbital-markets/apps/api/internal/venue/phoenix/live"
)

func main() {
	var response struct {
		Result helper.BuildOrderResult `json:"result"`
	}
	if err := json.NewDecoder(os.Stdin).Decode(&response); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := json.NewEncoder(os.Stdout).Encode(live.SigningMetadata{Built: response.Result}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
