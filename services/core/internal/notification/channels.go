package notification

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"strconv"
	"strings"
)

func (service *Service) ListChannels(
	ctx context.Context,
	workspaceID string,
) ([]Channel, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return nil, ErrInvalidInput
	}
	return service.repository.ListChannels(ctx, workspaceID)
}

func (service *Service) Channel(
	ctx context.Context,
	workspaceID string,
	id string,
) (Channel, error) {
	return service.repository.Channel(
		ctx,
		strings.TrimSpace(workspaceID),
		strings.TrimSpace(id),
	)
}

func (service *Service) CreateChannel(
	ctx context.Context,
	input CreateChannelInput,
) (Channel, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.Kind = strings.ToLower(strings.TrimSpace(input.Kind))
	input.Name = strings.TrimSpace(input.Name)
	if input.WorkspaceID == "" ||
		len([]rune(input.Name)) < 1 ||
		len([]rune(input.Name)) > 120 {
		return Channel{}, ErrInvalidInput
	}
	config, secret, err := service.normalizeChannelInput(ctx, input, nil)
	if err != nil {
		return Channel{}, err
	}
	id, err := notificationID()
	if err != nil {
		return Channel{}, err
	}
	var secretRef string
	if len(secret) > 0 {
		if service.secrets == nil {
			return Channel{}, errors.New("notification secret store is unavailable")
		}
		reference, err := service.secrets.Put(
			ctx,
			input.WorkspaceID,
			"notification."+input.Kind+".credentials",
			secret,
		)
		if err != nil {
			return Channel{}, err
		}
		secretRef = reference.ID
	}
	item, err := service.repository.CreateChannel(ctx, createChannelRecord{
		Channel: Channel{
			ID:          id,
			WorkspaceID: input.WorkspaceID,
			Kind:        input.Kind,
			Name:        input.Name,
			Status:      "disabled",
			Config:      config,
			Revision:    1,
		},
		SecretRef: secretRef,
		Now:       service.clock().UTC(),
	})
	if err != nil && secretRef != "" {
		_ = service.secrets.Delete(
			context.WithoutCancel(ctx),
			input.WorkspaceID,
			secretRef,
		)
	}
	return item, err
}

func (service *Service) UpdateChannel(
	ctx context.Context,
	input UpdateChannelInput,
) (Channel, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.ID = strings.TrimSpace(input.ID)
	input.Name = strings.TrimSpace(input.Name)
	if input.WorkspaceID == "" || input.ID == "" ||
		len([]rune(input.Name)) < 1 ||
		len([]rune(input.Name)) > 120 ||
		input.Revision < 1 {
		return Channel{}, ErrInvalidInput
	}
	current, err := service.repository.ResolvedChannel(
		ctx,
		input.WorkspaceID,
		input.ID,
	)
	if err != nil {
		return Channel{}, err
	}
	createInput := CreateChannelInput{
		WorkspaceID:         input.WorkspaceID,
		Kind:                current.Kind,
		Name:                input.Name,
		SMTPHost:            input.SMTPHost,
		SMTPPort:            input.SMTPPort,
		SMTPSecurity:        input.SMTPSecurity,
		SMTPFromAddress:     input.SMTPFromAddress,
		SMTPFromName:        input.SMTPFromName,
		SMTPUsername:        input.SMTPUsername,
		SMTPPassword:        input.SMTPPassword,
		TestRecipient:       input.TestRecipient,
		WebhookURL:          input.WebhookURL,
		AllowPrivateNetwork: input.AllowPrivateNetwork,
	}
	config, secret, err := service.normalizeChannelInput(ctx, createInput, &current)
	if err != nil {
		return Channel{}, err
	}
	var newSecretRef *string
	if len(secret) > 0 {
		if service.secrets == nil {
			return Channel{}, errors.New("notification secret store is unavailable")
		}
		reference, err := service.secrets.Put(
			ctx,
			input.WorkspaceID,
			"notification."+current.Kind+".credentials",
			secret,
		)
		if err != nil {
			return Channel{}, err
		}
		newSecretRef = &reference.ID
	}
	item, err := service.repository.UpdateChannel(ctx, updateChannelRecord{
		WorkspaceID: input.WorkspaceID,
		ID:          input.ID,
		Name:        input.Name,
		Config:      config,
		SecretRef:   newSecretRef,
		Revision:    input.Revision,
		Now:         service.clock().UTC(),
	})
	if err != nil {
		if newSecretRef != nil {
			_ = service.secrets.Delete(
				context.WithoutCancel(ctx),
				input.WorkspaceID,
				*newSecretRef,
			)
		}
		return Channel{}, err
	}
	if newSecretRef != nil && current.SecretRef != "" {
		_ = service.secrets.Delete(
			context.WithoutCancel(ctx),
			input.WorkspaceID,
			current.SecretRef,
		)
	}
	return item, nil
}

func (service *Service) DeleteChannel(
	ctx context.Context,
	input ChannelStateInput,
) error {
	record, err := service.repository.ResolvedChannel(
		ctx,
		strings.TrimSpace(input.WorkspaceID),
		strings.TrimSpace(input.ID),
	)
	if err != nil {
		return err
	}
	if input.Revision < 1 {
		return ErrInvalidInput
	}
	if err := service.repository.DeleteChannel(
		ctx,
		input,
		service.clock().UTC(),
	); err != nil {
		return err
	}
	if record.SecretRef != "" && service.secrets != nil {
		_ = service.secrets.Delete(
			context.WithoutCancel(ctx),
			record.WorkspaceID,
			record.SecretRef,
		)
	}
	return nil
}

func (service *Service) TestChannel(
	ctx context.Context,
	workspaceID string,
	id string,
) (ChannelTestReport, error) {
	startedAt := service.clock().UTC()
	record, err := service.repository.ResolvedChannel(
		ctx,
		strings.TrimSpace(workspaceID),
		strings.TrimSpace(id),
	)
	if err != nil {
		return ChannelTestReport{}, err
	}
	secret, err := service.loadChannelSecret(ctx, record.WorkspaceID, record.SecretRef)
	if err != nil {
		service.recordChannelFailure(ctx, record, err)
		return ChannelTestReport{}, err
	}
	target := DeliveryTarget{
		Channel: record.Channel,
		Secret:  secret,
	}
	if record.Kind == "email" {
		recipient := strings.TrimSpace(record.Config["testRecipient"])
		if recipient == "" {
			recipient = strings.TrimSpace(record.Config["smtpFromAddress"])
		}
		target.RecipientEmail = &recipient
		target.RecipientName = "Review Studio"
	}
	payload := DeliveryPayload{
		EventType:    "notification.channel_test",
		ResourceType: "notification_channel",
		ResourceID:   record.ID,
		Title:        "Review Studio notification test",
		Body:         "This is a test message from Review Studio.",
	}
	if err := service.sender.Send(ctx, target, payload); err != nil {
		deliveryErr := deliveryErrorFrom(err)
		service.recordChannelFailure(ctx, record, deliveryErr)
		return ChannelTestReport{}, deliveryErr
	}
	testedAt := service.clock().UTC()
	if _, err := service.repository.UpdateChannelTest(
		context.WithoutCancel(ctx),
		record.WorkspaceID,
		record.ID,
		"succeeded",
		"",
		"",
		testedAt,
	); err != nil {
		return ChannelTestReport{}, err
	}
	return ChannelTestReport{
		ChannelID: record.ID,
		Status:    "succeeded",
		Latency:   testedAt.Sub(startedAt),
		TestedAt:  testedAt,
	}, nil
}

func (service *Service) Preferences(
	ctx context.Context,
	workspaceID string,
	userID string,
) (Preferences, error) {
	return service.repository.Preferences(
		ctx,
		strings.TrimSpace(workspaceID),
		strings.TrimSpace(userID),
	)
}

func (service *Service) SetPreferences(
	ctx context.Context,
	input PreferenceInput,
) (Preferences, error) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.UserID = strings.TrimSpace(input.UserID)
	if input.WorkspaceID == "" || input.UserID == "" {
		return Preferences{}, ErrInvalidInput
	}
	return service.repository.SetPreferences(ctx, input, service.clock().UTC())
}

func (service *Service) normalizeChannelInput(
	ctx context.Context,
	input CreateChannelInput,
	current *channelRecord,
) (map[string]string, []byte, error) {
	switch input.Kind {
	case "email":
		return service.normalizeEmailInput(input, current)
	case "feishu", "wechat_work":
		return service.normalizeWebhookInput(ctx, input, current)
	default:
		return nil, nil, ErrChannelKindUnsupported
	}
}

func (service *Service) normalizeEmailInput(
	input CreateChannelInput,
	current *channelRecord,
) (map[string]string, []byte, error) {
	host := strings.TrimSpace(input.SMTPHost)
	port := input.SMTPPort
	if port == 0 {
		port = 587
	}
	security := strings.ToLower(strings.TrimSpace(input.SMTPSecurity))
	if security == "" {
		security = "starttls"
	}
	if host == "" || port < 1 || port > 65535 ||
		(security != "starttls" && security != "tls" && security != "plain") {
		return nil, nil, ErrInvalidInput
	}
	if security == "plain" &&
		(strings.TrimSpace(input.SMTPUsername) != "" || input.SMTPPassword != "" ||
			(current != nil && current.SecretRef != "")) {
		return nil, nil, ErrSMTPPlaintextAuth
	}
	fromAddress := strings.TrimSpace(input.SMTPFromAddress)
	if _, err := mail.ParseAddress(fromAddress); err != nil {
		return nil, nil, ErrInvalidInput
	}
	testRecipient := strings.TrimSpace(input.TestRecipient)
	if testRecipient != "" {
		if _, err := mail.ParseAddress(testRecipient); err != nil {
			return nil, nil, ErrInvalidInput
		}
	}
	config := map[string]string{
		"smtpHost":            host,
		"smtpPort":            strconv.Itoa(port),
		"smtpSecurity":        security,
		"smtpFromAddress":     fromAddress,
		"smtpFromName":        strings.TrimSpace(input.SMTPFromName),
		"testRecipient":       testRecipient,
		"allowPrivateNetwork": fmt.Sprint(input.AllowPrivateNetwork),
		"smtpAuthConfigured":  "false",
	}
	username := strings.TrimSpace(input.SMTPUsername)
	password := input.SMTPPassword
	if username == "" && password == "" {
		if current != nil && current.SecretRef != "" {
			if smtpIdentityChanged(current, config) {
				return nil, nil, ErrSMTPEndpointChanged
			}
			config["smtpAuthConfigured"] = "true"
		}
		return config, nil, nil
	}
	if username == "" {
		return nil, nil, ErrInvalidInput
	}
	if password == "" && current != nil && current.SecretRef != "" {
		if smtpIdentityChanged(current, config) {
			return nil, nil, ErrSMTPEndpointChanged
		}
		secret, err := service.loadChannelSecret(
			context.Background(),
			current.WorkspaceID,
			current.SecretRef,
		)
		if err != nil {
			return nil, nil, err
		}
		var credentials emailCredentials
		if err := json.Unmarshal(secret, &credentials); err != nil {
			return nil, nil, ErrInvalidInput
		}
		password = credentials.Password
	}
	secret, err := json.Marshal(emailCredentials{
		Username: username,
		Password: password,
	})
	config["smtpAuthConfigured"] = "true"
	return config, secret, err
}

func smtpIdentityChanged(current *channelRecord, config map[string]string) bool {
	if current == nil {
		return false
	}
	return current.Config["smtpHost"] != config["smtpHost"] ||
		current.Config["smtpPort"] != config["smtpPort"] ||
		current.Config["smtpSecurity"] != config["smtpSecurity"]
}

func (service *Service) normalizeWebhookInput(
	ctx context.Context,
	input CreateChannelInput,
	current *channelRecord,
) (map[string]string, []byte, error) {
	webhookURL := strings.TrimSpace(input.WebhookURL)
	config := map[string]string{
		"allowPrivateNetwork": fmt.Sprint(input.AllowPrivateNetwork),
	}
	if webhookURL == "" {
		if current == nil || current.SecretRef == "" {
			return nil, nil, ErrInvalidInput
		}
		config["webhookHost"] = current.Config["webhookHost"]
		return config, nil, nil
	}
	endpoint, err := validateWebhookURL(ctx, webhookURL, input.AllowPrivateNetwork)
	if err != nil {
		return nil, nil, err
	}
	config["webhookHost"] = endpoint.Hostname()
	secret, err := json.Marshal(webhookSecret{URL: endpoint.String()})
	return config, secret, err
}

func (service *Service) loadChannelSecret(
	ctx context.Context,
	workspaceID string,
	reference string,
) ([]byte, error) {
	if reference == "" {
		return nil, nil
	}
	if service.secrets == nil {
		return nil, errors.New("notification secret store is unavailable")
	}
	return service.secrets.Get(ctx, workspaceID, reference)
}

func (service *Service) recordChannelFailure(
	ctx context.Context,
	record channelRecord,
	testErr error,
) {
	deliveryErr := deliveryErrorFrom(testErr)
	_, _ = service.repository.UpdateChannelTest(
		context.WithoutCancel(ctx),
		record.WorkspaceID,
		record.ID,
		"failed",
		deliveryErr.Code,
		deliveryErr.Message,
		service.clock().UTC(),
	)
}
