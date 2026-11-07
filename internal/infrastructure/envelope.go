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

	var lastCandidate *application.Envelope

	// Scan through data finding top-level JSON objects.
	// For every '{' outside string literals, scan forward tracking
	// braces and string escape sequences so that '{' and '}' inside
	// strings (e.g. in code snippets, regex, diffs) do not alter depth.
	for i := 0; i < len(data); i++ {
		if data[i] != '{' {
			continue
		}

		depth := 0
		inString := false
		escapeCount := 0
		matchedEnd := -1

		for j := i; j < len(data); j++ {
			b := data[j]
			if inString {
				if b == '\\' {
					escapeCount++
				} else {
					if b == '"' && escapeCount%2 == 0 {
						inString = false
					}
					escapeCount = 0
				}
				continue
			}

			// Outside string literal
			if b == '"' {
				inString = true
				escapeCount = 0
			} else if b == '{' {
				depth++
			} else if b == '}' {
				depth--
				if depth == 0 {
					matchedEnd = j
					break
				}
			}
		}

		if matchedEnd > i {
			candidateSlice := data[i : matchedEnd+1]
			var candidateEnv application.Envelope
			if err := json.Unmarshal(candidateSlice, &candidateEnv); err == nil && candidateEnv.Status != "" {
				lastCandidate = &candidateEnv
				i = matchedEnd
			}
		}
	}

	return lastCandidate
}
