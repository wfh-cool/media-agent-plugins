package main

import (
	_ "embed"
	"fmt"

	pluginsdk "github.com/chenbstack/media-agent-plugin-sdk-go"
	"github.com/chenbstack/media-agent-plugin-sdk-go/providers"
)

//go:embed plugin.yaml
var manifestYAML []byte

//go:embed config.schema.json
var schemaJSON []byte

//go:embed icon.svg
var iconSVG []byte

func Plugin() pluginsdk.Plugin {
	return pluginsdk.Plugin{
		Manifest:       pluginsdk.MustParseManifest(manifestYAML),
		ConfigSchema:   pluginsdk.MustParseConfigSchema(schemaJSON),
		IconSVG:        iconSVG,
		NewNotifier:    newNotifier,
		ValidateConfig: validateConfig,
	}
}

func validateConfig(config map[string]any) error {
	cfg := parseConfig(config)
	errs := map[string]string{}
	if cfg.QQAppID == "" {
		errs["qq_app_id"] = "QQ 机器人 AppID 不能为空"
	}
	if cfg.QQAppSecretRef == "" {
		errs["qq_app_secret"] = "QQ 机器人密钥不能为空"
	}
	if cfg.QQRecipientType != "c2c" && cfg.QQRecipientType != "group" {
		errs["qq_recipient_type"] = "接收目标类型必须是 c2c 或 group"
	}
	if len(cfg.QQRecipientOpenIDs) == 0 {
		errs["qq_recipient_openids"] = "至少配置一个接收者 OpenID"
	}
	if len(errs) > 0 {
		return &pluginsdk.ValidationError{Fields: errs}
	}
	return nil
}

var _ providers.NotifierProvider = (*qqNotifier)(nil)

func init() {
	if len(manifestYAML) == 0 || len(schemaJSON) == 0 || len(iconSVG) == 0 {
		panic(fmt.Sprintf("插件资源缺失: manifest=%d schema=%d icon=%d", len(manifestYAML), len(schemaJSON), len(iconSVG)))
	}
}
