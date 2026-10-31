package infrastructure

import (
	"bytes"
	"encoding/json"

	"agyq/internal/application"
)

// ParseEnvelope parses an agy response JSON envelope from raw stdout.
// It is tolerant of unknown fields and surrounding output.
// If multiple JSON objects exist, the last valid one is selected.
// Returns nil if no valid JSON envelope is found, and never panics.
func ParseEnvelope(data []byte) *application.Envelope {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil
	}

	// Fast path: entire data is a valid envelope
	var env application.Envelope
	if err := json.Unmarshal(data, &env); err == nil && env.Status != "" {
		return &env
	}

	// Scan backwards for potential JSON objects: find matching '{' and '}'
	// Spec rule (05 §3): "take the last top-level JSON object in the stream"
	var candidate *application.Envelope

	// Find the positions of '{' and '}'
	for i := len(data) - 1; i >= 0; i-- {
		if data[i] == '}' {
			// Search backwards for the corresponding '{'
			depth := 0
			for j := i; j >= 0; j-- {
				if data[j] == '}' {
					depth++
				} else if data[j] == '{' {
					depth--
					if depth == 0 {
						// Candidate JSON slice data[j : i+1]
						var candidateEnv application.Envelope
						if err := json.Unmarshal(data[j:i+1], &candidateEnv); err == nil && candidateEnv.Status != "" {
							return &candidateEnv
						}
					}
				}
			}
		}
	}

	return candidate
}
