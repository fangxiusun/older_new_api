package newapi

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

// parseTaskBillingRequest 按渠道 URL 和映射后的模型校验、迁移请求，计费和转发共用其结果。
// 新站点或模型需明确实现请求契约及计费分支，不继承其他组合的默认规则。
func parseTaskBillingRequest(body []byte, baseURL, model string) (relaycommon.TaskSubmitReq, error) {
	modelKey, err := resolveTaskModel(baseURL, model)
	if err != nil {
		return relaycommon.TaskSubmitReq{}, err
	}
	var req relaycommon.TaskSubmitReq
	switch modelKey {
	case tokeaseMiniMaxH3:
		req, err = parseTokeaseMiniMaxH3Request(body)
	default:
		return req, fmt.Errorf("request handling is not configured for newapi task model %q", modelKey)
	}
	if err != nil {
		return req, err
	}
	_, err = estimateTaskBilling(baseURL, model, req)
	return req, err
}

func validateTaskBillingConfig(baseURL, model, priceModel string) error {
	modelKey, err := resolveTaskModel(baseURL, model)
	if err != nil {
		return err
	}
	switch modelKey {
	case tokeaseMiniMaxH3:
		// if _, ok := ratio_setting.GetModelPrice(priceModel, false); !ok || common.StringsContains(constant.TaskPricePatches, priceModel) {
		// 	return fmt.Errorf("configure ModelPrice for %q as the 768P price per second; token pricing and TaskPricePatches are not supported", priceModel)
		// }
		// if _, ok := ratio_setting.GetModelPrice(priceModel, false); !ok {
		// 	return fmt.Errorf("configure ModelPrice for %q as the 768P price not exist", priceModel)
		// }

		return nil
	default:
		return fmt.Errorf("billing is not configured for newapi task model %q", modelKey)
	}
}

func estimateTaskBilling(baseURL, model string, req relaycommon.TaskSubmitReq) (map[string]float64, error) {
	modelKey, err := resolveTaskModel(baseURL, model)
	if err != nil {
		return nil, err
	}
	switch modelKey {
	case tokeaseMiniMaxH3:
		if req.Duration <= 0 || req.Duration > relaycommon.MaxTaskDurationSeconds {
			return nil, fmt.Errorf("duration must be an integer between 1 and %d", relaycommon.MaxTaskDurationSeconds)
		}
		resolution, _ := req.Metadata["resolution"].(string)
		ratio, err := taskResolutionRatio(modelKey, resolution)
		if err != nil {
			return nil, err
		}
		return map[string]float64{
			"seconds":    float64(req.Duration),
			"resolution": ratio,
		}, nil
	default:
		return nil, fmt.Errorf("billing is not configured for newapi task model %q", modelKey)
	}
}

// parseTokeaseMiniMaxH3Request 使用顶层 duration 和 metadata.resolution 计费。
// 其他 metadata 字段保留原始 JSON，支持扩展参数及大整数，不参与本模型的计费。
func parseTokeaseMiniMaxH3Request(body []byte) (relaycommon.TaskSubmitReq, error) {
	var req relaycommon.TaskSubmitReq
	var fields map[string]json.RawMessage
	if err := common.Unmarshal(body, &fields); err != nil {
		return req, err
	}
	if err := common.Unmarshal(body, &req); err != nil {
		return req, err
	}
	if strings.TrimSpace(req.Model) == "" || strings.TrimSpace(req.Prompt) == "" {
		return req, fmt.Errorf("model and prompt are required")
	}
	// TaskSubmitReq 的兼容解码会忽略部分 duration 解析错误，因此在此读取原值。
	if err := common.Unmarshal(fields["duration"], &req.Duration); err != nil {
		return req, fmt.Errorf("duration must be an integer between 1 and %d", relaycommon.MaxTaskDurationSeconds)
	}
	var metadata map[string]json.RawMessage
	if err := common.Unmarshal(fields["metadata"], &metadata); err != nil || metadata == nil {
		return req, fmt.Errorf("metadata must be an object containing resolution")
	}
	var resolution string
	if err := common.Unmarshal(metadata["resolution"], &resolution); err != nil || strings.TrimSpace(resolution) == "" {
		return req, fmt.Errorf("metadata.resolution is required and must be a non-empty string")
	}
	resolution = strings.ToUpper(strings.TrimSpace(resolution))
	if raw, ok := metadata["ratio"]; ok {
		var ratio string
		if err := common.Unmarshal(raw, &ratio); err != nil || strings.TrimSpace(ratio) == "" {
			return req, fmt.Errorf("metadata.ratio must be a non-empty string")
		}
	}
	var videos []string
	if raw, ok := metadata["video_urls"]; ok {
		if err := common.Unmarshal(raw, &videos); err != nil || videos == nil {
			return req, fmt.Errorf("metadata.video_urls must be an array of video URLs")
		}
	}
	// 和 Ali 的数组媒体一致：保留已有引用，按 Media 中的顺序追加。
	// 此处从原始请求重新解码，避免修改 context 中的共享切片或在重试时重复追加。
	for _, media := range req.Media {
		if strings.TrimSpace(media.URL) == "" {
			return req, fmt.Errorf("media URL must not be empty")
		}
		switch strings.ToLower(strings.TrimSpace(media.Type)) {
		case "images":
			req.Images = append(req.Images, media.URL)
		case "video_urls":
			videos = append(videos, media.URL)
		default:
			return req, fmt.Errorf("unsupported MiniMax-H3 media type %q; use images or video_urls", media.Type)
		}
	}
	req.Media = nil
	for _, urls := range [][]string{req.Images, videos} {
		for _, value := range urls {
			if strings.TrimSpace(value) == "" {
				return req, fmt.Errorf("reference image and video URLs must not be empty")
			}
		}
	}
	req.Metadata = make(map[string]interface{}, len(metadata))
	for key, raw := range metadata {
		req.Metadata[key] = raw
	}
	req.Metadata["resolution"] = resolution
	if videos != nil {
		req.Metadata["video_urls"] = videos
	}
	return req, nil
}
