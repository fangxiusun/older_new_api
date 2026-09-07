package newapi

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// 按上游站点和模型记录相对于其计费基价的分辨率倍率。
var videoSizeRatio = map[string]map[string]float64{
	// Tokease H3 以 768P 每秒单价为基价，各种参考输入使用同一倍率。
	tokeaseMiniMaxH3: {"768P": 1, "2K": 0.8 / 0.5},
}

func resolveTaskUpstream(baseURL string) (string, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || strings.Contains(baseURL, "#") {
		return "", fmt.Errorf("unsupported newapi task upstream; supported base URL: https://tokease.cn")
	}
	// 使用解析后的完整主机名，避免把路径、用户名或相似域名中的 tokease.cn 当作站点。
	switch strings.ToLower(parsed.Hostname()) {
	case "tokease.cn":
		if (parsed.Port() == "" || parsed.Port() == "443") && strings.Trim(parsed.EscapedPath(), "/") == "" {
			return "tokease.cn", nil
		}
	}
	return "", fmt.Errorf("unsupported newapi task upstream; supported base URL: https://tokease.cn")
}

// resolveTaskModel 以渠道 URL 和映射后的模型名共同决定处理分支。
// 模型只匹配完整路径段或带 -/_ 后缀的已知名称，避免将 H30 当作 H3。
func resolveTaskModel(baseURL, name string) (string, error) {
	upstream, err := resolveTaskUpstream(baseURL)
	if err != nil {
		return "", err
	}
	matched := ""
	for _, segment := range strings.Split(name, "/") {
		for _, model := range ModelList {
			if segment != model && !strings.HasPrefix(segment, model+"-") && !strings.HasPrefix(segment, model+"_") {
				continue
			}
			if matched != "" && matched != model {
				return "", fmt.Errorf("ambiguous newapi task model %q", name)
			}
			matched = model
		}
	}
	if matched == "" {
		return "", fmt.Errorf("unsupported newapi task model %q; supported models: %s", name, strings.Join(ModelList, ", "))
	}
	return upstream + "/" + matched, nil
}

func taskResolutionRatio(modelKey, resolution string) (float64, error) {
	modelRatios, ok := videoSizeRatio[modelKey]
	if !ok {
		return 0, fmt.Errorf("resolution pricing is not configured for %s", modelKey)
	}
	ratio, ok := modelRatios[strings.ToUpper(strings.TrimSpace(resolution))]
	if !ok {
		supported := make([]string, 0, len(modelRatios))
		for name := range modelRatios {
			supported = append(supported, name)
		}
		sort.Strings(supported)
		return 0, fmt.Errorf("unsupported resolution %q for %s; supported resolutions: %s", resolution, modelKey, strings.Join(supported, ", "))
	}
	return ratio, nil
}
