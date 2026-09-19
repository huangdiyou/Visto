package notification

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"review-studio.local/core/internal/outbound"
)

func validateWebhookURL(
	ctx context.Context,
	value string,
	allowPrivate bool,
) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.Hostname() == "" ||
		parsed.User != nil ||
		parsed.Fragment != "" {
		return nil, DeliveryError{
			Code:    "notification.webhook_invalid",
			Message: "webhook URL is invalid",
		}
	}
	if err := validateWebhookHost(ctx, parsed.Hostname(), allowPrivate); err != nil {
		return nil, err
	}
	return parsed, nil
}

func validateWebhookHost(
	ctx context.Context,
	host string,
	allowPrivate bool,
) error {
	_, err := outbound.ResolveAllowed(ctx, host, allowPrivate)
	if err != nil {
		return DeliveryError{
			Code:    "notification.webhook_unresolved",
			Message: fmt.Sprintf("validate webhook host: %v", err),
		}
	}
	return nil
}

func forbiddenWebhookIP(ip net.IP, allowPrivate bool) bool {
	return outbound.ForbiddenIP(ip, allowPrivate)
}

func newWebhookHTTPClient(endpoint *url.URL, allowPrivate bool) *http.Client {
	return outbound.NewHTTPClient(endpoint, outbound.HTTPPolicy{
		AllowPrivate: allowPrivate,
		Timeout:      2 * time.Minute,
	})
}
