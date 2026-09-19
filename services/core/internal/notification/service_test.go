package notification

import (
	"strings"
	"testing"
)

func TestSanitizeDeliveryErrorRedactsWebhookURL(t *testing.T) {
	message := sanitizeDeliveryError(
		`Post "https://open.feishu.cn/open-apis/bot/v2/hook/example-webhook-token": proxyconnect tcp: webhook redirects are blocked`,
	)
	if strings.Contains(message, "example-webhook-token") ||
		strings.Contains(message, "open-apis/bot") {
		t.Fatalf("webhook URL was not redacted: %s", message)
	}
	if !strings.Contains(message, "https://open.feishu.cn/<redacted>") {
		t.Fatalf("expected redacted host to remain visible, got %s", message)
	}
}
