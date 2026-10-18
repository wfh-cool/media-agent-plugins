package main

import "strings"

type Config struct {
	QQAppID            string
	QQAppSecretRef     string
	QQRecipientType    string
	QQRecipientOpenIDs []string
	QQUseMarkdown      bool
}

func parseConfig(raw map[string]any) Config {
	cfg := Config{
		QQRecipientType: "c2c",
		QQUseMarkdown:   true,
	}
	if raw == nil {
		return cfg
	}
	cfg.QQAppID = strings.TrimSpace(stringConfig(raw["qq_app_id"]))
	cfg.QQAppSecretRef = strings.TrimSpace(stringConfig(raw["qq_app_secret"]))
	if value := strings.TrimSpace(stringConfig(raw["qq_recipient_type"])); value != "" {
		cfg.QQRecipientType = value
	}
	cfg.QQRecipientOpenIDs = splitRecipients(stringConfig(raw["qq_recipient_openids"]))
	if value, ok := raw["qq_use_markdown"].(bool); ok {
		cfg.QQUseMarkdown = value
	}
	return cfg
}

func stringConfig(value any) string {
	str, _ := value.(string)
	return str
}
