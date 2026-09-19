package notification

import (
	"net"
	"net/http"
	"net/url"
	"testing"
)

func TestWebhookClientDoesNotUseEnvironmentProxy(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:7897")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:7897")
	t.Setenv("ALL_PROXY", "http://127.0.0.1:7897")
	t.Setenv("NO_PROXY", "")

	endpoint, err := url.Parse("https://open.feishu.cn/open-apis/bot/v2/hook/token")
	if err != nil {
		t.Fatalf("parse endpoint: %v", err)
	}
	client := newWebhookHTTPClient(endpoint, false)
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected HTTP transport, got %T", client.Transport)
	}
	if transport.Proxy != nil {
		t.Fatal("webhook client must not inherit environment proxy")
	}
}

func TestForbiddenWebhookIPUsesSharedReservedRangePolicy(t *testing.T) {
	for _, value := range []string{"127.0.0.1", "100.64.0.1", "198.18.0.41"} {
		if !forbiddenWebhookIP(net.ParseIP(value), false) {
			t.Fatalf("expected %s to be blocked", value)
		}
	}
	if forbiddenWebhookIP(net.ParseIP("127.0.0.1"), true) {
		t.Fatal("private network opt-in should allow loopback test endpoints")
	}
}
