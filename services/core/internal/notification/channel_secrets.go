package notification

type emailCredentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type webhookSecret struct {
	URL string `json:"url"`
}
