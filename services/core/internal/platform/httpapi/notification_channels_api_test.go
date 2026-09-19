package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestNotificationChannelAPIHidesSecretsAndUpdatesPreferences(t *testing.T) {
	handler := NewHandler(testConfig(t))
	setupResponse := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/setup",
		`{
			"workspaceName": "Studio",
			"ownerName": "Owner",
			"ownerEmail": "owner@example.com",
			"password": "local-password-123",
			"locale": "zh-CN",
			"timezone": "Asia/Shanghai"
		}`,
		nil,
	)
	if setupResponse.Code != http.StatusCreated {
		t.Fatalf("setup failed with %d: %s", setupResponse.Code, setupResponse.Body.String())
	}
	cookie := findSessionCookie(t, setupResponse.Result().Cookies())

	createResponse := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/notification-channels",
		`{
			"kind": "email",
			"name": "SMTP",
			"smtpHost": "smtp.example.com",
			"smtpPort": 587,
			"smtpSecurity": "starttls",
			"smtpFromAddress": "review@example.com",
			"smtpFromName": "Review Studio",
			"smtpUsername": "smtp-user",
			"smtpPassword": "smtp-password",
			"testRecipient": "owner@example.com"
		}`,
		cookie,
	)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf(
			"create notification channel failed with %d: %s",
			createResponse.Code,
			createResponse.Body.String(),
		)
	}
	body := createResponse.Body.String()
	if strings.Contains(body, "smtp-password") || strings.Contains(body, "smtp-user") {
		t.Fatalf("channel response exposed SMTP credentials: %s", body)
	}
	var channel notificationChannelResponse
	if err := json.Unmarshal([]byte(body), &channel); err != nil {
		t.Fatalf("decode notification channel: %v", err)
	}
	if !channel.SMTPAuthConfigured || channel.Status != "disabled" {
		t.Fatalf("unexpected channel response: %#v", channel)
	}

	testResponse := performJSONRequest(
		handler,
		http.MethodPost,
		"/api/v1/notification-channels/"+channel.ID+"/test",
		`{}`,
		cookie,
	)
	if testResponse.Code != http.StatusOK {
		t.Fatalf("test notification channel failed with %d: %s", testResponse.Code, testResponse.Body.String())
	}

	preferenceResponse := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/notification-preferences/me",
		"",
		cookie,
	)
	if preferenceResponse.Code != http.StatusOK {
		t.Fatalf("get preferences failed with %d: %s", preferenceResponse.Code, preferenceResponse.Body.String())
	}
	var preferences notificationPreferencesResponse
	if err := json.NewDecoder(preferenceResponse.Body).Decode(&preferences); err != nil {
		t.Fatalf("decode preferences: %v", err)
	}
	if !preferences.EmailEnabled || !preferences.FeishuEnabled ||
		!preferences.WechatWorkEnabled {
		t.Fatalf("expected preferences to default on: %#v", preferences)
	}

	updatePreferenceResponse := performJSONRequest(
		handler,
		http.MethodPut,
		"/api/v1/notification-preferences/me",
		`{
			"emailEnabled": false,
			"feishuEnabled": true,
			"wechatWorkEnabled": false
		}`,
		cookie,
	)
	if updatePreferenceResponse.Code != http.StatusOK {
		t.Fatalf(
			"update preferences failed with %d: %s",
			updatePreferenceResponse.Code,
			updatePreferenceResponse.Body.String(),
		)
	}
	if err := json.NewDecoder(updatePreferenceResponse.Body).Decode(&preferences); err != nil {
		t.Fatalf("decode updated preferences: %v", err)
	}
	if preferences.EmailEnabled || !preferences.FeishuEnabled ||
		preferences.WechatWorkEnabled {
		t.Fatalf("unexpected updated preferences: %#v", preferences)
	}

	deliveriesResponse := performJSONRequest(
		handler,
		http.MethodGet,
		"/api/v1/notification-deliveries",
		"",
		cookie,
	)
	if deliveriesResponse.Code != http.StatusOK {
		t.Fatalf(
			"list notification deliveries failed with %d: %s",
			deliveriesResponse.Code,
			deliveriesResponse.Body.String(),
		)
	}
}
