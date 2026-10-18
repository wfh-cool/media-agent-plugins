package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	pluginsdk "github.com/chenbstack/media-agent-plugin-sdk-go"
	"github.com/chenbstack/media-agent-plugin-sdk-go/providers"
)

const (
	qqTokenURL = "https://bots.qq.com/app/getAppAccessToken"
	qqAPIBase  = "https://api.sgroup.qq.com"
	qqTimeout  = 30 * time.Second
)

type qqNotifier struct {
	config  Config
	secrets pluginsdk.SecretResolver
	client  *qqAPI
}

func newNotifier(_ context.Context, inst pluginsdk.Instance, secrets pluginsdk.SecretResolver) (providers.NotifierProvider, error) {
	return &qqNotifier{
		config:  parseConfig(inst.Config),
		secrets: secrets,
		client:  newQQAPI(nil, qqTokenURL, qqAPIBase),
	}, nil
}

func (n *qqNotifier) Kind() string { return "qqbot" }

func (n *qqNotifier) TestConnection(ctx context.Context) error {
	if _, err := n.accessToken(ctx); err != nil {
		return err
	}
	_, err := n.client.request(ctx, "", http.MethodGet, "/users/@me", nil)
	if err != nil {
		return fmt.Errorf("QQ Bot 连通性检查失败: %w", err)
	}
	return nil
}

func (n *qqNotifier) Send(ctx context.Context, msg providers.NotificationMessage) error {
	targets := n.config.QQRecipientOpenIDs
	if len(targets) == 0 {
		return fmt.Errorf("未配置 QQ Bot 接收者 OpenID")
	}
	content, plain := formatQQMessage(msg)
	if strings.TrimSpace(content) == "" {
		return fmt.Errorf("QQ Bot 通知内容为空")
	}
	token, err := n.accessToken(ctx)
	if err != nil {
		return err
	}

	var sendErrors []error
	for _, target := range targets {
		path := "/v2/users/" + url.PathEscape(target) + "/messages"
		if n.config.QQRecipientType == "group" {
			path = "/v2/groups/" + url.PathEscape(target) + "/messages"
		}

		body := qqMessagePayload(content, n.config.QQUseMarkdown)
		_, sendErr := n.client.request(ctx, token, http.MethodPost, path, body)
		if sendErr != nil && n.config.QQUseMarkdown && isQQMarkdownPermissionError(sendErr) {
			_, sendErr = n.client.request(ctx, token, http.MethodPost, path, qqMessagePayload(plain, false))
		}
		if sendErr != nil {
			sendErrors = append(sendErrors, sendErr)
		}
	}
	if len(sendErrors) > 0 {
		return fmt.Errorf("QQ Bot 向 %d 个目标发送失败: %w", len(sendErrors), sendErrors[0])
	}
	return nil
}

func (n *qqNotifier) accessToken(ctx context.Context) (string, error) {
	if n.secrets == nil {
		return "", fmt.Errorf("宿主未提供 QQ Bot 密钥解析服务")
	}
	secret, err := n.secrets.Reveal(ctx, n.config.QQAppSecretRef, "获取 QQ Bot 访问令牌")
	if err != nil {
		return "", fmt.Errorf("读取 QQ Bot 密钥失败")
	}
	token, err := n.client.accessToken(ctx, n.config.QQAppID, secret)
	if err != nil {
		return "", fmt.Errorf("获取 QQ Bot 访问令牌失败: %w", err)
	}
	return token, nil
}

type qqAPI struct {
	httpClient *http.Client
	tokenURL   string
	apiBase    string
	mu         sync.Mutex
	token      string
	tokenAppID string
	tokenUntil time.Time
	now        func() time.Time
}

func newQQAPI(client *http.Client, tokenURL, apiBase string) *qqAPI {
	if client == nil {
		client = &http.Client{Timeout: qqTimeout}
	}
	return &qqAPI{
		httpClient: client,
		tokenURL:   tokenURL,
		apiBase:    strings.TrimRight(apiBase, "/"),
		now:        time.Now,
	}
}

func (c *qqAPI) accessToken(ctx context.Context, appID, secret string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()
	if c.token != "" && c.tokenAppID == appID && now.Before(c.tokenUntil.Add(-5*time.Minute)) {
		return c.token, nil
	}

	payload, err := json.Marshal(map[string]string{"appId": appID, "clientSecret": secret})
	if err != nil {
		return "", fmt.Errorf("构造 QQ Bot 令牌请求失败")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("构造 QQ Bot 令牌请求失败")
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("请求 QQ Bot 令牌接口失败")
	}
	defer res.Body.Close()
	data, err := decodeQQResponse(res)
	if err != nil {
		return "", err
	}
	var tokenResponse struct {
		AccessToken string          `json:"access_token"`
		ExpiresIn   json.RawMessage `json:"expires_in"`
	}
	if err := json.Unmarshal(data, &tokenResponse); err != nil || strings.TrimSpace(tokenResponse.AccessToken) == "" {
		return "", fmt.Errorf("QQ Bot 令牌接口响应格式无效")
	}
	expiresIn := 7200
	if len(tokenResponse.ExpiresIn) > 0 {
		var seconds int
		if err := json.Unmarshal(tokenResponse.ExpiresIn, &seconds); err == nil && seconds > 0 {
			expiresIn = seconds
		} else {
			var text string
			if json.Unmarshal(tokenResponse.ExpiresIn, &text) != nil {
				return "", fmt.Errorf("QQ Bot 令牌接口过期时间无效")
			}
			seconds, err = strconv.Atoi(text)
			if err != nil || seconds <= 0 {
				return "", fmt.Errorf("QQ Bot 令牌接口过期时间无效")
			}
			expiresIn = seconds
		}
	}
	c.token = tokenResponse.AccessToken
	c.tokenAppID = appID
	c.tokenUntil = now.Add(time.Duration(expiresIn) * time.Second)
	return c.token, nil
}

func (c *qqAPI) request(ctx context.Context, token, method, path string, body any) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("构造 QQ Bot 消息失败")
		}
		reader = bytes.NewReader(payload)
	}
	endpoint := c.apiBase + path
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return nil, fmt.Errorf("构造 QQ Bot API 请求失败")
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "QQBot "+token)
	}
	res, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 QQ Bot API 失败")
	}
	defer res.Body.Close()
	data, err := decodeQQResponse(res)
	if err != nil {
		return nil, err
	}
	var response struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(data, &response); err == nil && response.Code != 0 {
		return nil, qqAPIError{code: response.Code}
	}
	return data, nil
}

func decodeQQResponse(res *http.Response) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("读取 QQ Bot API 响应失败")
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		var response struct {
			Code int `json:"code"`
		}
		_ = json.Unmarshal(data, &response)
		return nil, qqAPIError{status: res.StatusCode, code: response.Code}
	}
	if !json.Valid(data) {
		return nil, fmt.Errorf("QQ Bot API 返回无效 JSON")
	}
	return data, nil
}

type qqAPIError struct {
	status int
	code   int
}

func (e qqAPIError) Error() string {
	if e.code != 0 {
		return fmt.Sprintf("QQ Bot API 错误（HTTP %d，错误码 %d）", e.status, e.code)
	}
	return fmt.Sprintf("QQ Bot API 错误（HTTP %d）", e.status)
}

func isQQMarkdownPermissionError(err error) bool {
	var apiErr qqAPIError
	return errors.As(err, &apiErr) && apiErr.code == 11244
}

type qqMessage struct {
	Content  string `json:"content,omitempty"`
	Markdown *struct {
		Content string `json:"content"`
	} `json:"markdown,omitempty"`
	MessageType int `json:"msg_type"`
}

func qqMessagePayload(content string, markdown bool) qqMessage {
	msg := qqMessage{MessageType: 0}
	if markdown {
		msg.MessageType = 2
		msg.Markdown = &struct {
			Content string `json:"content"`
		}{Content: content}
	} else {
		msg.Content = content
	}
	return msg
}

func formatQQMessage(msg providers.NotificationMessage) (string, string) {
	title := strings.TrimSpace(strings.ReplaceAll(msg.Title, "\n", " "))
	body := strings.TrimSpace(msg.Body)
	plainParts := make([]string, 0, 3)
	markdownParts := make([]string, 0, 3)
	if title != "" {
		plainParts = append(plainParts, title)
		markdownParts = append(markdownParts, "**"+escapeQQMarkdown(title)+"**")
	}
	if body != "" {
		plainParts = append(plainParts, body)
		markdownParts = append(markdownParts, escapeQQMarkdown(body))
	}
	if imageURL := validQQMediaURL(msg.Metadata["image_url"]); imageURL != "" {
		plainParts = append(plainParts, imageURL)
		markdownParts = append(markdownParts, "![]("+imageURL+")")
	}
	if actionURL := validQQMediaURL(msg.ActionURL); actionURL != "" {
		label := strings.TrimSpace(msg.ActionLabel)
		if label == "" {
			label = "查看详情"
		}
		plainParts = append(plainParts, label+": "+actionURL)
		markdownParts = append(markdownParts, "["+escapeQQMarkdown(label)+"]("+actionURL+")")
	}
	return strings.Join(markdownParts, "\n\n"), strings.Join(plainParts, "\n\n")
}

func escapeQQMarkdown(text string) string {
	text = strings.ReplaceAll(text, "\\", "\\\\")
	for _, char := range []string{"*", "_", "[", "]", "`"} {
		text = strings.ReplaceAll(text, char, "\\"+char)
	}
	return text
}

func validQQMediaURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
		return ""
	}
	return parsed.String()
}

func splitRecipients(raw string) []string {
	seen := map[string]struct{}{}
	recipients := make([]string, 0)
	for _, target := range strings.Split(raw, ",") {
		target = strings.TrimSpace(target)
		if target == "" {
			continue
		}
		if _, ok := seen[target]; ok {
			continue
		}
		seen[target] = struct{}{}
		recipients = append(recipients, target)
	}
	return recipients
}
