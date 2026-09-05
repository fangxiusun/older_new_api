package newapi

import (
	"sort"
	"strings"
)

// videoSizeRatio 为 newapi 视频任务的内置尺寸倍率表（结构与 ali 渠道的 aliRatios 一致：
// 模型名或前缀 → {尺寸: 倍率}），倍率参考各厂商官方定价换算（如 1080P 价 / 720P 价）。
// "*" 为通配条目：模型组内未命中尺寸时先回退组内 "*"，再回退全局 "*"，最后为 1。
var videoSizeRatio = map[string]map[string]float64{
	// 全局通配：未按模型配置时使用，与 sora 渠道历史硬编码一致
	// （宽高比 16:9 以外的大尺寸按 5/3 计费）
	"*": {
		"1792x1024": 1.666667,
		"1024x1792": 1.666667,
	},

	// Sora 系列
	"sora-2":     {"720x1280": 1, "1280x720": 1, "1792x1024": 1.666667, "1024x1792": 1.666667},
	"sora-2-pro": {"720x1280": 1, "1280x720": 1, "1792x1024": 1.666667, "1024x1792": 1.666667},

	// Kling 系列（尺寸为可灵 mode 字段值）
	"kling-v1":        {"720p": 1, "1080p": 1.6 / 0.9},
	"kling-v1-5":      {"720p": 1, "1080p": 1.6 / 0.9},
	"kling-v1-6":      {"720p": 1, "1080p": 1.6 / 0.9},
	"kling-v2":        {"720p": 1, "1080p": 1.6 / 0.9},
	"kling-v2-master": {"720p": 1, "1080p": 1.6 / 0.9},

	// Jimeng 系列（尺寸为分辨率档位）
	"jimeng": {"480P": 1, "720P": 1, "1080P": 2},

	// Vidu 系列
	"vidu":  {"360P": 1, "720P": 1, "1080P": 1.6 / 0.9},
	"viduq": {"720P": 1, "1080P": 1.6 / 0.9},

	// MiniMax / 海螺系列
	"T2V-01":       {"768P": 1},
	"I2V-01":       {"768P": 1},
	"hailuo-video": {"768P": 1, "1080P": 1.6 / 0.9},

	// Doubao / Seedance 系列
	"doubao-seedance": {"480P": 1, "720P": 2, "1080P": 4.8},

	// Wan / 通义系列（尺寸为分辨率档位，倍率与 ali 渠道 aliRatios 一致）
	"wan2.5-t2v-preview": {"480P": 1, "720P": 2, "1080P": 1 / 0.3},
	"wan2.2-t2v-plus":    {"480P": 1, "1080P": 0.7 / 0.14},
	"wanx2.1-t2v-turbo":  {"480P": 1, "720P": 1},
	"wanx2.1-t2v-plus":   {"720P": 1},
	"wan2.5-i2v-preview": {"480P": 1, "720P": 2, "1080P": 1 / 0.3},
	"wan2.2-i2v-flash":   {"480P": 1, "720P": 2, "1080P": 4.8},
	"wan2.2-i2v-plus":    {"480P": 1, "1080P": 5},
	"wanx2.1-i2v-turbo":  {"480P": 1, "720P": 1},
	"wanx2.1-i2v-plus":   {"720P": 1},
	"wan2.2-kf2v-flash":  {"480P": 1, "720P": 2, "1080P": 4.8},
	"wanx2.1-kf2v-plus":  {"720P": 1},

	// HappyHorse 系列
	"happyhorse-1.0-t2v":        {"720P": 1, "1080P": 1.6 / 0.9},
	"happyhorse-1.0-i2v":        {"720P": 1, "1080P": 1.6 / 0.9},
	"happyhorse-1.0-r2v":        {"720P": 1, "1080P": 1.6 / 0.9},
	"happyhorse-1.0-video-edit": {"720P": 1, "1080P": 1.6 / 0.9},
	"happyhorse-1.1-t2v":        {"720P": 1, "1080P": 1.2 / 0.9},
	"happyhorse-1.1-i2v":        {"720P": 1, "1080P": 1.2 / 0.9},
	"happyhorse-1.1-r2v":        {"720P": 1, "1080P": 1.2 / 0.9},

	"wan2.2-s2v": {"480P": 1, "720P": 0.9 / 0.5},
}

// getVideoSizeRatio 按模型查找尺寸计费倍率：精确匹配 → 最长前缀匹配 →
// 命中组内 "*" 通配 → 全局 "*" 通配 → 缺省 1。
func getVideoSizeRatio(model, size string) float64 {
	modelRatios := findVideoModelSizeRatios(model)
	if modelRatios != nil {
		if ratio, ok := modelRatios[size]; ok {
			return ratio
		}
		if ratio, ok := modelRatios["*"]; ok {
			return ratio
		}
	}
	if ratio, ok := videoSizeRatio["*"][size]; ok {
		return ratio
	}
	return 1
}

// findVideoModelSizeRatios 使用最长前缀匹配查找模型的尺寸倍率配置。
func findVideoModelSizeRatios(model string) map[string]float64 {
	if ratios, ok := videoSizeRatio[model]; ok {
		return ratios
	}

	type entry struct {
		prefix string
		ratios map[string]float64
	}
	var entries []entry
	for prefix, ratios := range videoSizeRatio {
		if prefix != "*" && strings.HasPrefix(model, prefix) {
			entries = append(entries, entry{prefix, ratios})
		}
	}
	if len(entries) == 0 {
		return nil
	}
	sort.Slice(entries, func(i, j int) bool {
		return len(entries[i].prefix) > len(entries[j].prefix)
	})
	return entries[0].ratios
}
