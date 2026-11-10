package api

import (
	"fmt"
	"net/http"
	"time"
)

// A finite server WriteTimeout bounds normal responses, not a long-lived SSE
// session. Bound each actual flush instead; quiet time between heartbeats must
// not kill a healthy stream or retain a stalled writer indefinitely.
func streamController(w http.ResponseWriter) *http.ResponseController {
	c := http.NewResponseController(w)
	_ = c.SetWriteDeadline(time.Time{})
	return c
}

func flushStream(c *http.ResponseController) error {
	_ = c.SetWriteDeadline(time.Now().Add(10 * time.Second))
	err := c.Flush()
	_ = c.SetWriteDeadline(time.Time{})
	return err
}

func writeStream(w http.ResponseWriter, c *http.ResponseController, format string, values ...any) error {
	_ = c.SetWriteDeadline(time.Now().Add(10 * time.Second))
	defer c.SetWriteDeadline(time.Time{})
	if _, err := fmt.Fprintf(w, format, values...); err != nil {
		return err
	}
	return c.Flush()
}
