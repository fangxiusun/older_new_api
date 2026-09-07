package newapi

// ModelList 仅列出已确认请求格式、直链响应和计费规则的模型。
// 上游配置的路径前后缀及 -/_ 后缀由 resolveTaskModel 识别，转发时保留完整名称。
var ModelList = []string{"MiniMax-H3"}

// 处理规则按上游站点和标准模型共同区分，同名模型不能跨站点复用计费规则。
const tokeaseMiniMaxH3 = "tokease.cn/MiniMax-H3"

var ChannelName = "newapi"
