package newapi

// ModelList 为空：上游同为 new-api 实例，模型由上游管理员动态配置，
// 通过渠道「获取模型」接口拉取，这里不内置固定列表。
var ModelList = []string{}

var ChannelName = "newapi"
