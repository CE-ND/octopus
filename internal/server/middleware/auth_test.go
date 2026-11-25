package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestResolveAPIKeyGeminiSources(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name        string
		headerName  string
		headerValue string
		query       string
		wantKey     string
		wantType    string
	}{
		{name: "x-goog-api-key", headerName: "x-goog-api-key", headerValue: "sk-octopus-google", wantKey: "sk-octopus-google", wantType: "gemini"},
		{name: "bearer", headerName: "Authorization", headerValue: "Bearer sk-octopus-bearer", wantKey: "sk-octopus-bearer", wantType: "openai"},
		{name: "query key", query: "sk-octopus-query", wantKey: "sk-octopus-query", wantType: "gemini"},
		{name: "x-api-key wins", headerName: "x-api-key", headerValue: "sk-octopus-anthropic", wantKey: "sk-octopus-anthropic", wantType: "anthropic"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			target := "/v1beta/models/test:generateContent"
			if tt.query != "" {
				target += "?key=" + tt.query
			}
			c.Request = httptest.NewRequest(http.MethodPost, target, nil)
			if tt.headerName != "" {
				c.Request.Header.Set(tt.headerName, tt.headerValue)
			}

			key, requestType := resolveAPIKey(c, true)
			if key != tt.wantKey || requestType != tt.wantType {
				t.Fatalf("resolveAPIKey() = (%q, %q), want (%q, %q)", key, requestType, tt.wantKey, tt.wantType)
			}
		})
	}
}

func TestResolveAPIKeyDoesNotAcceptGoogleHeaderForOpenAI(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Request.Header.Set("x-goog-api-key", "sk-octopus-google")

	key, requestType := resolveAPIKey(c, false)
	if key != "" || requestType != "" {
		t.Fatalf("resolveAPIKey() = (%q, %q), want empty", key, requestType)
	}
}
