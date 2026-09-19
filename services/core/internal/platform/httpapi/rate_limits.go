package httpapi

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (h *handler) consumeRateLimit(
	response http.ResponseWriter,
	request *http.Request,
	namespace string,
	key string,
	limit int,
	window time.Duration,
) bool {
	if h.rateLimits == nil {
		return true
	}
	allowed, err := h.rateLimits.Consume(request.Context(), namespace, key, limit, window)
	if err != nil {
		h.internalError(response, request, err)
		return false
	}
	if allowed {
		return true
	}
	h.logger.Warn(
		"request rate limited",
		"request_id", requestContextID(request),
		"namespace", namespace,
	)
	response.Header().Set("Retry-After", retryAfterSeconds(window))
	writeError(
		response,
		http.StatusTooManyRequests,
		requestID(response),
		"rate_limit.exceeded",
		"尝试次数过多，请稍后再试",
	)
	return false
}

// Guest text-change budgets. A reviewer legitimately submits a burst of change
// notes while scrubbing a timeline, so the per-minute allowance is generous and
// the hourly allowance is what stops a script from posting all night. The
// per-share client tier bounds one network as a whole, while the per-visitor
// tier keeps one noisy visitor from consuming the whole share budget.
//
// Image attachments deliberately keep their own, much smaller budget: they
// decode a full image in memory and write to disk or remote storage, so they
// cost far more than a text comment.
const (
	guestTextChangesPerMinute      = 50
	guestTextChangesPerHour        = 500
	shareClientTextChangesPerMin   = 200
	shareClientTextChangesPerHour  = 2000
	guestTextChangeMinuteNamespace = "share-text-visitor-minute"
	guestTextChangeHourNamespace   = "share-text-visitor-hour"
	shareTextChangeMinNamespace    = "share-text-client-minute"
	shareTextChangeHourNamespace   = "share-text-client-hour"
)

// consumeGuestTextChangeBudget applies the tiered guest budget for comment
// creation, replies, edits, deletions, and decision submissions. All of them
// spend from the same text-change allowance, because to a reviewer they are the
// same activity.
//
// It must be called before the request body is read and before any database
// write, audit record, or notification, so a rejected request has no business
// side effect. It only needs the resolved share session, which costs one
// indexed lookup and no body read.
func (h *handler) consumeGuestTextChangeBudget(
	response http.ResponseWriter,
	request *http.Request,
	shareID string,
	visitorID string,
) bool {
	if h.rateLimits == nil {
		return true
	}
	clientIdentity := h.requestRateLimitClientIdentity(request)
	if clientIdentity == "" {
		clientIdentity = "unknown"
	}
	visitorKey := "share:" + shareID + "|visitor:" + visitorID
	clientKey := "share:" + shareID + "|client:" + clientIdentity

	tiers := []struct {
		namespace string
		key       string
		limit     int
		window    time.Duration
	}{
		{guestTextChangeMinuteNamespace, visitorKey, guestTextChangesPerMinute, time.Minute},
		{guestTextChangeHourNamespace, visitorKey, guestTextChangesPerHour, time.Hour},
		{shareTextChangeMinNamespace, clientKey, shareClientTextChangesPerMin, time.Minute},
		{shareTextChangeHourNamespace, clientKey, shareClientTextChangesPerHour, time.Hour},
	}
	for _, tier := range tiers {
		decision, err := h.rateLimits.ConsumeToken(
			request.Context(),
			tier.namespace,
			tier.key,
			tier.limit,
			tier.window,
		)
		if err != nil {
			h.internalError(response, request, err)
			return false
		}
		if decision.Allowed {
			continue
		}
		h.logger.Warn(
			"guest text change rate limited",
			"request_id", requestContextID(request),
			"namespace", tier.namespace,
		)
		h.writeRateLimited(response, decision.RetryAfter)
		return false
	}
	return true
}

func (h *handler) writeRateLimited(
	response http.ResponseWriter,
	retryAfter time.Duration,
) {
	seconds := int64(retryAfter / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	response.Header().Set("Retry-After", strconv.FormatInt(seconds, 10))
	writeError(
		response,
		http.StatusTooManyRequests,
		requestID(response),
		"rate_limit.exceeded",
		fmt.Sprintf("操作太频繁，请在 %d 秒后重试", seconds),
	)
}

func (h *handler) clearRateLimit(request *http.Request, namespace string, key string) {
	if h.rateLimits == nil {
		return
	}
	if err := h.rateLimits.Clear(request.Context(), namespace, key); err != nil {
		h.logger.Warn("rate limit clear failed", "namespace", namespace, "error", err)
	}
}

func requestRateLimitSource(request *http.Request) string {
	if address, ok := requestClientIP(request); ok {
		return address.String()
	}
	return "unknown"
}

func loginAccountKey(email string) string {
	value := strings.ToLower(strings.TrimSpace(email))
	if value == "" {
		return "legacy-owner"
	}
	return value
}

func retryAfterSeconds(window time.Duration) string {
	seconds := int64(window / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	return strconv.FormatInt(seconds, 10)
}
