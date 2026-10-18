# QQ Bot 通知插件

独立的 Media Agent `NotifierProvider` 插件，通过 QQ 开放平台向已授权的 QQ 用户或群聊发送主动通知。

## 配置

在插件配置中填写 QQ 机器人 AppID、AppSecret、目标类型和接收者 OpenID。多个接收者使用逗号分隔。
AppSecret 使用插件的 password/secret 配置，由宿主 SecretResolver 解密；插件不会在日志或错误中输出密钥。

支持纯文本和 QQ Markdown 消息。通知标题和正文会组合发送；`action_url` 会附加详情链接，metadata 中的
`image_url` 会作为 Markdown 图片展示。Markdown 开启时需要 QQ 开放平台的消息权限；接口返回权限错误码
`11244` 时会向该目标回退发送纯文本。其他目标的失败会作为发送错误返回。

插件仅支持 SDK `NotifierProvider` 定义的出站连接测试和消息发送。SDK 未提供入站网关接口，因此不接收
QQ 用户消息。

## 构建与测试

```sh
make build
make test
make vet
```
