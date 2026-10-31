package infrastructure

import (
	"reflect"
	"testing"
)

func TestParseEnvelope_MalformedAndFuzz(t *testing.T) {
	tests := []struct {
		name       string
		input      []byte
		wantNil    bool
		wantStatus string
	}{
		{
			name:       "valid standard envelope",
			input:      []byte(`{"conversation_id":"c1","status":"SUCCESS","response":"ok","usage":{"input_tokens":5}}`),
			wantNil:    false,
			wantStatus: "SUCCESS",
		},
		{
			name:       "valid envelope with unknown extra fields",
			input:      []byte(`{"conversation_id":"c2","status":"SUCCESS","future_field":123,"extra_obj":{"a":1}}`),
			wantNil:    false,
			wantStatus: "SUCCESS",
		},
		{
			name:       "valid envelope surrounded by noisy stdout logs",
			input:      []byte("warning: starting up language server...\n{\"conversation_id\":\"c3\",\"status\":\"SUCCESS\",\"response\":\"done\"}\nclosing logs"),
			wantNil:    false,
			wantStatus: "SUCCESS",
		},
		{
			name:       "multiple json objects picks last valid",
			input:      []byte("{\"status\":\"INFO\"}\nsome text\n{\"conversation_id\":\"c4\",\"status\":\"SUCCESS\",\"response\":\"final\"}"),
			wantNil:    false,
			wantStatus: "SUCCESS",
		},
		{
			name:    "empty input",
			input:   []byte(""),
			wantNil: true,
		},
		{
			name:    "whitespace only",
			input:   []byte("   \n\t  "),
			wantNil: true,
		},
		{
			name:    "plain text without json",
			input:   []byte("Error: agy connection refused"),
			wantNil: true,
		},
		{
			name:    "truncated json unclosed brace",
			input:   []byte(`{"conversation_id":"c5","status":"SUCCESS"`),
			wantNil: true,
		},
		{
			name:    "truncated json mid string",
			input:   []byte(`{"conversation_id":"c6","status":"SUC`),
			wantNil: true,
		},
		{
			name:    "json array instead of object",
			input:   []byte(`["status", "SUCCESS"]`),
			wantNil: true,
		},
		{
			name:    "json primitive string",
			input:   []byte(`"SUCCESS"`),
			wantNil: true,
		},
		{
			name:    "json primitive number",
			input:   []byte(`12345`),
			wantNil: true,
		},
		{
			name:    "nested json with missing status",
			input:   []byte(`{"conversation_id":"c7","response":"no status field"}`),
			wantNil: true,
		},
		{
			name:    "corrupted binary noise",
			input:   []byte{0x00, 0xFF, 0xFE, '{', '"', 's', 't', 'a', 't', 0x01, '}'},
			wantNil: true,
		},
		{
			name:    "unclosed quotes inside json",
			input:   []byte(`{"conversation_id": "c8, "status": "SUCCESS"}`),
			wantNil: true,
		},
		{
			name:    "double braces unclosed and unparseable",
			input:   []byte(`{ { "status": SUCCESS`),
			wantNil: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := ParseEnvelope(tc.input)
			if tc.wantNil {
				if res != nil {
					t.Errorf("expected nil envelope, got %+v", res)
				}
			} else {
				if res == nil {
					t.Fatalf("expected non-nil envelope for %q", tc.input)
				}
				if res.Status != tc.wantStatus {
					t.Errorf("status got %q, want %q", res.Status, tc.wantStatus)
				}
			}
		})
	}
}

func TestParseEnvelope_UsagePreserved(t *testing.T) {
	input := []byte(`{"conversation_id":"c10","status":"SUCCESS","usage":{"input_tokens":42,"raw_rate":3.14}}`)
	res := ParseEnvelope(input)
	if res == nil {
		t.Fatal("expected non-nil envelope")
	}
	if res.Usage == nil {
		t.Fatal("expected usage map")
	}
	if !reflect.DeepEqual(res.Usage["input_tokens"], float64(42)) {
		t.Errorf("input_tokens got %v", res.Usage["input_tokens"])
	}
}

func TestParseEnvelope_DeniedActionsPreserved(t *testing.T) {
	input := []byte(`{"conversation_id":"c11","status":"SUCCESS","response":"","denied_actions":[{"action":"command"}]}`)
	res := ParseEnvelope(input)
	if res == nil {
		t.Fatal("expected non-nil envelope")
	}
	if len(res.DeniedActions) != 1 {
		t.Fatalf("expected 1 denied action, got %d", len(res.DeniedActions))
	}
}
