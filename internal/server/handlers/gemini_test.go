package handlers

import "testing"

func TestParseGeminiModelMethod(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		wantModel  string
		wantMethod string
		wantErr    bool
	}{
		{
			name:       "generate content",
			path:       "/gemini-4-neon-c-ep:generateContent",
			wantModel:  "gemini-4-neon-c-ep",
			wantMethod: "generateContent",
		},
		{
			name:       "stream generate content",
			path:       "/models/gemini-3.1-pro-preview-c-ep:streamGenerateContent",
			wantModel:  "gemini-3.1-pro-preview-c-ep",
			wantMethod: "streamGenerateContent",
		},
		{
			name:    "missing method",
			path:    "/gemini-4-neon-c-ep",
			wantErr: true,
		},
		{
			name:    "unsupported method",
			path:    "/gemini-4-neon-c-ep:countTokens",
			wantErr: true,
		},
		{
			name:    "empty path",
			path:    "/",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			modelName, method, err := parseGeminiModelMethod(tt.path)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got model=%q method=%q", modelName, method)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if modelName != tt.wantModel || method != tt.wantMethod {
				t.Fatalf("got model=%q method=%q, want model=%q method=%q", modelName, method, tt.wantModel, tt.wantMethod)
			}
		})
	}
}
