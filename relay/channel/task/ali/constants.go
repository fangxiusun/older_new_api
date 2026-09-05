package ali

var ModelList = []string{
	"wan3.0-video",       // 万相3.0是 all-in-one 视频生成模型
	"wan3.0-video-prime", // Wan3.0-Video-Prime 是万相3.0 的高速版视频生成模型

	// wan2.7 系列 (新格式 - media 数组)
	"wan2.7-i2v", // 万相2.7图生视频
	"wan2.7-t2v", // 万相2.7文生视频
	"wan2.7-r2v",
	"wan2.7-videoedit",
	"wan2.7-vace",

	// wan2.6 系列 (旧格式 - 扁平字段)
	"wan2.6-i2v-flash",
	"wan2.6-i2v",
	"wan2.6-t2v",
	"wan2.6-r2v-flash",
	"wan2.6-r2v",

	// wan2.5 系列 (旧格式)
	"wan2.5-t2v-preview",
	"wan2.5-i2v-preview", // 万相2.5 preview（有声视频）推荐

	// wan2.2 系列 (旧格式)
	"wan2.2-t2v-plus",
	"wan2.2-i2v-flash", // 万相2.2极速版（无声视频）
	"wan2.2-i2v-plus",  // 万相2.2专业版（无声视频）
	"wan2.2-kf2v-flash",

	// wanx2.1 系列 (旧格式)
	"wanx2.1-t2v-turbo",
	"wanx2.1-t2v-plus",
	"wanx2.1-i2v-turbo", // 万相2.1极速版（无声视频）
	"wanx2.1-i2v-plus",  // 万相2.1专业版（无声视频）
	"wanx2.1-kf2v-plus",
	"wanx2.1-vace-plus",

	// happyhorse 系列 (新格式 - media 数组)
	"happyhorse-1.0-t2v",
	"happyhorse-1.0-i2v",
	"happyhorse-1.0-r2v",
	"happyhorse-1.0-video-edit",

	"happyhorse-1.1-t2v",
	"happyhorse-1.1-i2v",
	"happyhorse-1.1-r2v",
}

var ChannelName = "ali"
