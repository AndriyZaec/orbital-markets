package main

import (
	"net/http"
)

type externalRoutes interface {
	registerRoutes(*http.ServeMux) bool
}

func buildRootHandler(apiHandler http.Handler, integrations ...externalRoutes) http.Handler {
	mux := http.NewServeMux()
	registered := false
	for _, integration := range integrations {
		if integration != nil && integration.registerRoutes(mux) {
			registered = true
		}
	}
	if !registered {
		return apiHandler
	}
	mux.Handle("/", apiHandler)
	return mux
}
