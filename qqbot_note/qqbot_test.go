package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pluginsdk "github.com/chenbstack/media-agent-plugin-sdk-go"
	"github.com/chenbstack/media-agent-plugin-sdk-go/providers"
)

type testSecretResolver string

func (s testSecretResolver) Reveal(context.Context, string, string) (string, error) {
	return string(s), nil
}

func TestSplitRecipients(t *testing.T) {
	got := splitRecipients(" user-one, group-one, user-one ,, ")
	want := []string{"user-one", "group-one"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("splitRecipients() = %v, want %v", got, want)
	}
}

func TestFormatQQMessageAndPayload(t *testing.T) {
	msg := providers.NotificationMessage{
		Title:     "New *movie*",
		Body:      "A [release] is ready",
		ActionURL: "https://media.example/item/1",
		Metadata:  map[string]string{"image_url": "https://media.example/poster.jpg"},
	}
	markdown, plain := formatQQMessage(msg)
	if !strings.Contains(markdown, `**New \*movie\***`) ||
		!strings.Contains(markdown, `A \[release\] is ready`) ||
		!strings.Contains(markdown, "![](https://media.example/poster.jpg)") ||
		!strings.Contains(markdown, "[查看详情](https://media.example/item/1)") {
		t.Fatalf("Markdown content = %q", markdown)
	}
	if !strings.Contains(plain, "New *movie*") || !strings.Contains(plain, "https://media.example/item/1") {
		t.Fatalf("plain content = %q", plain)
	}
	if got := validQQMediaURL("javascript:alert(1)"); got != "" {
		t.Fatalf("unsafe URL accepted: %q", got)
	}

	data, err := json.Marshal(qqMessagePayload("text", false))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"markdown"`) {
		t.Fatalf("plain payload unexpectedly has markdown: %s", data)
	}
	var decoded map[string]any
	payload, err := json.Marshal(qqMessagePayload("**bold**", true))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["msg_type"] != float64(2) || decoded["markdown"].(map[string]any)["content"] != "**bold**" {
		t.Fatalf("markdown payload = %#v", decoded)
	}
}

func TestQQAPIAccessTokenCacheAndRequest(t *testing.T) {
	var tokenRequests, apiRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			tokenRequests.Add(1)
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode token request: %v", err)
			}
			if body["appId"] != "app" || body["clientSecret"] != "private-secret" {
				t.Errorf("token request body = %#v", body)
			}
			_, _ = fmt.Fprint(w, `{"access_token":"access-token","expires_in":"7200"}`)
		case "/v2/users/openid/messages":
			apiRequests.Add(1)
			if got := r.Header.Get("Authorization"); got != "QQBot access-token" {
				t.Errorf("Authorization = %q", got)
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode API request: %v", err)
			}
			if body["content"] != "hello" || body["msg_type"] != float64(0) {
				t.Errorf("API body = %#v", body)
			}
			_, _ = fmt.Fprint(w, `{"id":"message-id"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	api := newQQAPI(server.Client(), server.URL+"/token", server.URL)
	api.now = func() time.Time { return time.Unix(1000, 0) }
	for i := 0; i < 2; i++ {
		token, err := api.accessToken(context.Background(), "app", "private-secret")
		if err != nil || token != "access-token" {
			t.Fatalf("accessToken() = %q, %v", token, err)
		}
	}
	if tokenRequests.Load() != 1 {
		t.Fatalf("token requests = %d, want cached single request", tokenRequests.Load())
	}
	if _, err := api.request(context.Background(), "access-token", http.MethodPost, "/v2/users/openid/messages", qqMessagePayload("hello", false)); err != nil {
		t.Fatal(err)
	}
	if apiRequests.Load() != 1 {
		t.Fatalf("API requests = %d, want 1", apiRequests.Load())
	}
}

func TestQQNotifierMarkdownPermissionFallback(t *testing.T) {
	var sent atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			_, _ = fmt.Fprint(w, `{"access_token":"access-token","expires_in":7200}`)
		case "/v2/groups/group-id/messages":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode message: %v", err)
			}
			if body["msg_type"] == float64(2) {
				w.WriteHeader(http.StatusForbidden)
				_, _ = fmt.Fprint(w, `{"code":11244,"message":"markdown disabled"}`)
				return
			}
			if body["msg_type"] != float64(0) || body["content"] != "Title\n\nBody" {
				t.Errorf("fallback body = %#v", body)
			}
			sent.Add(1)
			_, _ = fmt.Fprint(w, `{"id":"message-id"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	n := &qqNotifier{
		config: Config{
			QQAppID:            "app",
			QQAppSecretRef:     "secret-ref",
			QQRecipientType:    "group",
			QQRecipientOpenIDs: []string{"group-id"},
			QQUseMarkdown:      true,
		},
		secrets: testSecretResolver("private-secret"),
		client:  newQQAPI(server.Client(), server.URL+"/token", server.URL),
	}
	if err := n.Send(context.Background(), providers.NotificationMessage{Title: "Title", Body: "Body"}); err != nil {
		t.Fatal(err)
	}
	if sent.Load() != 1 {
		t.Fatalf("plain-text fallback sends = %d, want 1", sent.Load())
	}
}

func TestQQNotifierDoesNotExposeSecretOnHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = fmt.Fprint(w, `{"message":"private-secret was rejected"}`)
	}))
	defer server.Close()
	n := &qqNotifier{
		config:  Config{QQAppID: "app", QQAppSecretRef: "ref"},
		secrets: testSecretResolver("private-secret"),
		client:  newQQAPI(server.Client(), server.URL+"/token", server.URL),
	}
	err := n.TestConnection(context.Background())
	if err == nil {
		t.Fatal("TestConnection() should fail")
	}
	if strings.Contains(err.Error(), "private-secret") {
		t.Fatalf("error leaked secret: %v", err)
	}
}

func TestConfigParsingAndValidation(t *testing.T) {
	cfg := parseConfig(map[string]any{
		"qq_app_id": " app-id ", "qq_app_secret": "secret-ref", "qq_recipient_type": "group",
		"qq_recipient_openids": "group-one, group-two,group-one", "qq_use_markdown": false,
	})
	if cfg.QQAppID != "app-id" || cfg.QQAppSecretRef != "secret-ref" ||
		cfg.QQRecipientType != "group" || fmt.Sprint(cfg.QQRecipientOpenIDs) != "[group-one group-two]" || cfg.QQUseMarkdown {
		t.Fatalf("parsed config = %+v", cfg)
	}
	if err := validateConfig(map[string]any{
		"qq_app_id": "app-id", "qq_app_secret": "secret-ref",
		"qq_recipient_type": "group", "qq_recipient_openids": "group-one",
	}); err != nil {
		t.Fatalf("valid QQ Bot config rejected: %v", err)
	}
	err := validateConfig(map[string]any{"qq_app_id": "app-id"})
	if err == nil {
		t.Fatal("partial QQ config should fail")
	}
	if !strings.Contains(err.Error(), "qq_app_secret") || !strings.Contains(err.Error(), "qq_recipient_openids") {
		t.Fatalf("validation error does not identify incomplete QQ config: %v", err)
	}
}

func TestPluginRegistersAsQQBotNotifier(t *testing.T) {
	plugin := Plugin()
	if plugin.NewNotifier == nil {
		t.Fatal("Plugin() did not register a notifier provider")
	}
	if plugin.Manifest.ID != "qqbot-notifier" || plugin.Manifest.Category != "notification" {
		t.Fatalf("unexpected plugin identity: %+v", plugin.Manifest)
	}
	if err := pluginsdk.NewRegistry().Register(plugin); err != nil {
		t.Fatalf("plugin manifest/config is invalid: %v", err)
	}
	secretFields := plugin.ConfigSchema.SecretFields()
	if len(secretFields) != 1 || secretFields[0].Name != "qq_app_secret" {
		t.Fatalf("secret config fields = %#v, want only qq_app_secret", secretFields)
	}
}
