package relay

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/gin-gonic/gin"
)

func TestCopyHeadersDoesNotOverwriteGeminiAPIKey(t *testing.T) {
	gin.SetMode(gin.TestMode)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/test:generateContent", nil)
	c.Request.Header.Set("x-goog-api-key", "client-octopus-key")
	c.Request.Header.Set("x-custom-header", "forwarded")

	outboundRequest := httptest.NewRequest(http.MethodPost, "https://upstream.example/v1beta/models/test:generateContent", nil)
	outboundRequest.Header.Set("x-goog-api-key", "upstream-channel-key")

	attempt := &relayAttempt{
		relayRequest: &relayRequest{c: c},
		channel:      &model.Channel{},
	}
	attempt.copyHeaders(outboundRequest)

	if got := outboundRequest.Header.Get("x-goog-api-key"); got != "upstream-channel-key" {
		t.Fatalf("x-goog-api-key = %q, want upstream-channel-key", got)
	}
	if got := outboundRequest.Header.Get("x-custom-header"); got != "forwarded" {
		t.Fatalf("x-custom-header = %q, want forwarded", got)
	}
}
