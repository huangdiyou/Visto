package notification

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"review-studio.local/core/internal/outbound"
)

type Sender interface {
	Send(context.Context, DeliveryTarget, DeliveryPayload) error
}

type NoopSender struct{}

func (NoopSender) Send(context.Context, DeliveryTarget, DeliveryPayload) error {
	return nil
}

type DefaultSender struct{}

func NewDefaultSender() *DefaultSender {
	return &DefaultSender{}
}

func (sender *DefaultSender) Send(
	ctx context.Context,
	target DeliveryTarget,
	payload DeliveryPayload,
) error {
	switch target.Channel.Kind {
	case "email":
		return sender.sendEmail(ctx, target, payload)
	case "feishu", "wechat_work":
		return sender.sendWebhook(ctx, target, payload)
	default:
		return DeliveryError{
			Code:    "notification.channel_unsupported",
			Message: "notification channel is unsupported",
		}
	}
}

func (sender *DefaultSender) sendEmail(
	ctx context.Context,
	target DeliveryTarget,
	payload DeliveryPayload,
) error {
	if target.RecipientEmail == nil || strings.TrimSpace(*target.RecipientEmail) == "" {
		return DeliveryError{
			Code:    "notification.recipient_email_missing",
			Message: "recipient does not have an email address",
		}
	}
	config := target.Channel.Config
	host := strings.TrimSpace(config["smtpHost"])
	port, err := strconv.Atoi(strings.TrimSpace(config["smtpPort"]))
	if err != nil || port < 1 || port > 65535 || host == "" {
		return DeliveryError{
			Code:    "notification.smtp_invalid",
			Message: "SMTP host or port is invalid",
		}
	}
	fromAddress := strings.TrimSpace(config["smtpFromAddress"])
	if _, err := mail.ParseAddress(fromAddress); err != nil {
		return DeliveryError{
			Code:    "notification.smtp_invalid",
			Message: "SMTP from address is invalid",
		}
	}
	toAddress := strings.TrimSpace(*target.RecipientEmail)
	if _, err := mail.ParseAddress(toAddress); err != nil {
		return DeliveryError{
			Code:    "notification.recipient_email_invalid",
			Message: "recipient email address is invalid",
		}
	}
	var credentials emailCredentials
	if len(target.Secret) > 0 {
		if err := json.Unmarshal(target.Secret, &credentials); err != nil {
			return DeliveryError{
				Code:    "notification.credentials_invalid",
				Message: "SMTP credentials are invalid",
			}
		}
	}
	fromAddressValue := mail.Address{
		Name:    strings.TrimSpace(config["smtpFromName"]),
		Address: fromAddress,
	}
	from := fromAddressValue.String()
	message := buildEmailMessage(
		from,
		toAddress,
		payload.Title,
		payload.Body,
	)
	var auth smtp.Auth
	if strings.TrimSpace(credentials.Username) != "" {
		auth = smtp.PlainAuth(
			"",
			strings.TrimSpace(credentials.Username),
			credentials.Password,
			host,
		)
	}
	if err := sendSMTPMessage(
		ctx,
		host,
		port,
		strings.ToLower(strings.TrimSpace(config["smtpSecurity"])),
		config["allowPrivateNetwork"] == "true",
		auth,
		fromAddress,
		[]string{toAddress},
		[]byte(message),
	); err != nil {
		return DeliveryError{
			Code:    "notification.smtp_failed",
			Message: sanitizeDeliveryError(err.Error()),
		}
	}
	return nil
}

func sendSMTPMessage(
	ctx context.Context,
	host string,
	port int,
	security string,
	allowPrivate bool,
	auth smtp.Auth,
	from string,
	recipients []string,
	message []byte,
) error {
	if security == "" {
		security = "starttls"
	}
	if security != "starttls" && security != "tls" && security != "plain" {
		return errors.New("unsupported SMTP security mode")
	}

	address := net.JoinHostPort(host, strconv.Itoa(port))
	conn, err := outbound.DialContext(allowPrivate, 15*time.Second)(ctx, "tcp", address)
	if err != nil {
		return fmt.Errorf("connect SMTP server: %w", err)
	}
	defer conn.Close()
	deadline := time.Now().Add(30 * time.Second)
	if value, ok := ctx.Deadline(); ok && value.Before(deadline) {
		deadline = value
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return err
	}

	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: host,
	}
	if security == "tls" {
		tlsConn := tls.Client(conn, tlsConfig)
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			return fmt.Errorf("establish SMTP TLS: %w", err)
		}
		conn = tlsConn
	}

	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return fmt.Errorf("open SMTP session: %w", err)
	}
	defer client.Close()
	if security == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return errors.New("SMTP server does not offer required STARTTLS")
		}
		if err := client.StartTLS(tlsConfig); err != nil {
			return fmt.Errorf("establish SMTP STARTTLS: %w", err)
		}
	}
	if auth != nil {
		if ok, _ := client.Extension("AUTH"); !ok {
			return errors.New("SMTP server does not offer authentication")
		}
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("authenticate SMTP session: %w", err)
		}
	}
	if err := client.Mail(from); err != nil {
		return err
	}
	for _, recipient := range recipients {
		if err := client.Rcpt(recipient); err != nil {
			return err
		}
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := io.Copy(writer, bytes.NewReader(message)); err != nil {
		_ = writer.Close()
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return client.Quit()
}

func (sender *DefaultSender) sendWebhook(
	ctx context.Context,
	target DeliveryTarget,
	payload DeliveryPayload,
) error {
	var secret webhookSecret
	if err := json.Unmarshal(target.Secret, &secret); err != nil ||
		strings.TrimSpace(secret.URL) == "" {
		return DeliveryError{
			Code:    "notification.webhook_invalid",
			Message: "webhook URL is invalid",
		}
	}
	endpoint, err := validateWebhookURL(
		ctx,
		secret.URL,
		target.Channel.Config["allowPrivateNetwork"] == "true",
	)
	if err != nil {
		return err
	}
	body, err := encodeWebhookBody(target.Channel.Kind, payload)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		endpoint.String(),
		bytes.NewReader(body),
	)
	if err != nil {
		return DeliveryError{
			Code:    "notification.webhook_invalid",
			Message: "webhook request is invalid",
		}
	}
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	client := newWebhookHTTPClient(endpoint, target.Channel.Config["allowPrivateNetwork"] == "true")
	response, err := client.Do(request)
	if err != nil {
		return DeliveryError{
			Code:    "notification.webhook_failed",
			Message: sanitizeDeliveryError(err.Error()),
		}
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return DeliveryError{
			Code:    "notification.webhook_rejected",
			Message: fmt.Sprintf("webhook returned HTTP %d", response.StatusCode),
		}
	}
	return nil
}

func buildEmailMessage(from, to, subject, body string) string {
	headers := []string{
		"From: " + from,
		"To: " + to,
		"Subject: " + mime.QEncoding.Encode("utf-8", subject),
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=utf-8",
		"Content-Transfer-Encoding: 8bit",
		"Date: " + time.Now().UTC().Format(time.RFC1123Z),
	}
	return strings.Join(headers, "\r\n") + "\r\n\r\n" + body + "\r\n"
}

func encodeWebhookBody(kind string, payload DeliveryPayload) ([]byte, error) {
	text := strings.TrimSpace(payload.Title + "\n" + payload.Body)
	switch kind {
	case "feishu":
		return json.Marshal(map[string]any{
			"msg_type": "text",
			"content":  map[string]string{"text": text},
		})
	case "wechat_work":
		return json.Marshal(map[string]any{
			"msgtype": "text",
			"text":    map[string]string{"content": text},
		})
	default:
		return nil, DeliveryError{
			Code:    "notification.channel_unsupported",
			Message: "webhook channel is unsupported",
		}
	}
}

func (err DeliveryError) Error() string {
	if strings.TrimSpace(err.Message) != "" {
		return err.Message
	}
	if strings.TrimSpace(err.Code) != "" {
		return err.Code
	}
	return "notification delivery failed"
}

func deliveryErrorFrom(err error) DeliveryError {
	var deliveryErr DeliveryError
	if errors.As(err, &deliveryErr) {
		deliveryErr.Message = sanitizeDeliveryError(deliveryErr.Message)
		if strings.TrimSpace(deliveryErr.Code) == "" {
			deliveryErr.Code = "notification.delivery_failed"
		}
		return deliveryErr
	}
	return DeliveryError{
		Code:    "notification.delivery_failed",
		Message: sanitizeDeliveryError(err.Error()),
	}
}
