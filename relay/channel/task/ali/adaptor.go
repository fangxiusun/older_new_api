package ali

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
)

// ============================
// 旧格式请求结构 (wan2.1 ~ wan2.6)
// ============================

type AliOldVideoRequest struct {
	Model      string                 `json:"model"`
	Input      AliOldVideoInput       `json:"input"`
	Parameters *AliOldVideoParameters `json:"parameters,omitempty"`
}

type AliOldVideoInput struct {
	Prompt         string   `json:"prompt,omitempty"`
	ImgURL         string   `json:"img_url,omitempty"`
	AudioURL       string   `json:"audio_url,omitempty"`
	NegativePrompt string   `json:"negative_prompt,omitempty"`
	Template       string   `json:"template,omitempty"`
	ReferenceURLs  []string `json:"reference_urls,omitempty"`
	FirstFrameURL  string   `json:"first_frame_url,omitempty"`
	LastFrameURL   string   `json:"last_frame_url,omitempty"`
	Function       string   `json:"function,omitempty"`
	RefImagesURL   []string `json:"ref_images_url,omitempty"`
	VideoURL       string   `json:"video_url,omitempty"`
	FirstClipURL   string   `json:"first_clip_url,omitempty"`
	LastClipURL    string   `json:"last_clip_url,omitempty"`
}

type AliOldVideoParameters struct {
	Resolution   string `json:"resolution,omitempty"`
	Size         string `json:"size,omitempty"`
	Duration     int    `json:"duration,omitempty"`
	PromptExtend *bool  `json:"prompt_extend,omitempty"`
	ShotType     string `json:"shot_type,omitempty"`
	Audio        *bool  `json:"audio,omitempty"`
	Watermark    *bool  `json:"watermark,omitempty"`
	Seed         *int   `json:"seed,omitempty"`
}

// ============================
// 新格式请求结构 (wan2.7+, happyhorse)
// ============================

type AliNewVideoRequest struct {
	Model      string                 `json:"model"`
	Input      AliNewVideoInput       `json:"input"`
	Parameters *AliNewVideoParameters `json:"parameters,omitempty"`
}

type AliNewVideoInput struct {
	Prompt         string          `json:"prompt,omitempty"`
	NegativePrompt string          `json:"negative_prompt,omitempty"`
	AudioURL       string          `json:"audio_url,omitempty"`
	Media          []AliVideoMedia `json:"media,omitempty"`
}

type AliVideoMedia struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}

type AliNewVideoParameters struct {
	Resolution   string `json:"resolution,omitempty"`
	Duration     int    `json:"duration,omitempty"`
	PromptExtend *bool  `json:"prompt_extend,omitempty"`
	Watermark    *bool  `json:"watermark,omitempty"`
	Seed         *int   `json:"seed,omitempty"`
	Ratio        string `json:"ratio,omitempty"`
	AudioSetting string `json:"audio_setting,omitempty"`
}

// ============================
// 响应结构 (所有模型通用)
// ============================

type AliVideoResponse struct {
	Output    AliVideoOutput `json:"output"`
	RequestID string         `json:"request_id"`
	Code      string         `json:"code,omitempty"`
	Message   string         `json:"message,omitempty"`
	Usage     *AliUsage      `json:"usage,omitempty"`
}

type AliVideoOutput struct {
	TaskID        string `json:"task_id"`
	TaskStatus    string `json:"task_status"`
	SubmitTime    string `json:"submit_time,omitempty"`
	ScheduledTime string `json:"scheduled_time,omitempty"`
	EndTime       string `json:"end_time,omitempty"`
	OrigPrompt    string `json:"orig_prompt,omitempty"`
	ActualPrompt  string `json:"actual_prompt,omitempty"`
	VideoURL      string `json:"video_url,omitempty"`
	Code          string `json:"code,omitempty"`
	Message       string `json:"message,omitempty"`
}

type AliUsage struct {
	InputVideoDuration  dto.IntValue    `json:"input_video_duration,omitempty"`
	OutputVideoDuration dto.IntValue    `json:"output_video_duration,omitempty"`
	Duration            dto.IntValue    `json:"duration,omitempty"`
	SR                  dto.IntValue    `json:"SR,omitempty"`
	Ratio               dto.StringValue `json:"ratio,omitempty"`
	VideoCount          dto.IntValue    `json:"video_count,omitempty"`
}

type AliMetadata struct {
	// Input 相关
	AudioURL       string          `json:"audio_url,omitempty"`       // 音频URL
	ImgURL         string          `json:"img_url,omitempty"`         // 图片URL（图生视频）
	FirstFrameURL  string          `json:"first_frame_url,omitempty"` // 首帧图片URL（首尾帧生视频）
	LastFrameURL   string          `json:"last_frame_url,omitempty"`  // 尾帧图片URL（首尾帧生视频）
	Media          []AliVideoMedia `json:"media,omitempty"`           // 媒体列表（wan2.7-i2v新协议）
	NegativePrompt string          `json:"negative_prompt,omitempty"` // 反向提示词
	Template       string          `json:"template,omitempty"`        // 视频特效模板

	// Parameters 相关
	Resolution   *string `json:"resolution,omitempty"`    // 分辨率: 480P/720P/1080P
	Size         *string `json:"size,omitempty"`          // 尺寸: 如 "832*480"
	Duration     *int    `json:"duration,omitempty"`      // 时长
	PromptExtend *bool   `json:"prompt_extend,omitempty"` // 是否开启prompt智能改写
	Watermark    *bool   `json:"watermark,omitempty"`     // 是否添加水印
	Audio        *bool   `json:"audio,omitempty"`         // 是否添加音频
	Seed         *int    `json:"seed,omitempty"`          // 随机数种子
}

// ============================
// Adaptor 实现
// ============================

type TaskAdaptor struct {
	taskcommon.BaseBilling
	ChannelType int
	apiKey      string
	baseURL     string
}

func (a *TaskAdaptor) Init(info *relaycommon.RelayInfo) {
	a.ChannelType = info.ChannelType
	a.baseURL = info.ChannelBaseUrl
	a.apiKey = info.ApiKey
}

func (a *TaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) (taskErr *taskdto.TaskError) {
	// ValidateMultipartDirect 负责解析并将原始 TaskSubmitReq 存入 context
	return relaycommon.ValidateMultipartDirect(c, info)
}

func (a *TaskAdaptor) BuildRequestURL(info *relaycommon.RelayInfo) (string, error) {
	model := info.UpstreamModelName
	if model == "" {
		model = info.OriginModelName
	}
	return fmt.Sprintf("%s%s", a.baseURL, getAliVideoEndpoint(model)), nil
}

func (a *TaskAdaptor) BuildRequestHeader(c *gin.Context, req *http.Request, info *relaycommon.RelayInfo) error {
	req.Header.Set("Authorization", "Bearer "+a.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-DashScope-Async", "enable")
	return nil
}

func (a *TaskAdaptor) BuildRequestBody(c *gin.Context, info *relaycommon.RelayInfo) (io.Reader, error) {
	taskReq, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil, errors.Wrap(err, "get_task_request_failed")
	}

	upstreamModel := taskReq.Model
	if info.IsModelMapped {
		upstreamModel = info.UpstreamModelName
	}

	var reqBody any
	migrateTaskImagesToMedia(upstreamModel, &taskReq)
	migrateTaskParamsToMetadata(&taskReq)
	if isNewFormatModel(upstreamModel) {
		reqBody, err = buildNewFormatRequest(upstreamModel, taskReq)
	} else {
		reqBody, err = buildOldFormatRequest(upstreamModel, taskReq)
	}
	if err != nil {
		return nil, errors.Wrap(err, "build_request_failed")
	}

	logger.LogJson(c, "ali video request body", reqBody)

	bodyBytes, err := common.Marshal(reqBody)
	if err != nil {
		return nil, errors.Wrap(err, "marshal_request_failed")
	}
	return bytes.NewReader(bodyBytes), nil
}

// EstimateBilling 根据用户请求参数计算 OtherRatios（时长、分辨率等）。
// 在 ValidateRequestAndSetAction 之后、价格计算之前调用。
func (a *TaskAdaptor) EstimateBilling(c *gin.Context, info *relaycommon.RelayInfo) map[string]float64 {
	taskReq, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil
	}

	upstreamModel := taskReq.Model
	if info.IsModelMapped {
		upstreamModel = info.UpstreamModelName
	}

	var size, resolution string
	var duration int
	var audio *bool

	if isNewFormatModel(upstreamModel) {
		migrateTaskImagesToMedia(upstreamModel, &taskReq)
		migrateTaskParamsToMetadata(&taskReq)
		req, err := buildNewFormatRequest(upstreamModel, taskReq)
		if err != nil {
			return nil
		}
		resolution = req.Parameters.Resolution
		duration = req.Parameters.Duration
	} else {
		migrateTaskImagesToMedia(upstreamModel, &taskReq)
		migrateTaskParamsToMetadata(&taskReq)
		req, err := buildOldFormatRequest(upstreamModel, taskReq)
		if err != nil {
			return nil
		}
		size = req.Parameters.Size
		resolution = req.Parameters.Resolution
		duration = req.Parameters.Duration
		audio = req.Parameters.Audio
	}

	// metadata can override Duration past standard request validation;
	// cap it because it is used as a billing multiplier.
	otherRatios := map[string]float64{
		"seconds": float64(min(duration, relaycommon.MaxTaskDurationSeconds)),
	}

	ratios, err := ProcessAliOtherRatios(upstreamModel, size, resolution, audio)
	if err != nil {
		return otherRatios
	}
	for k, v := range ratios {
		otherRatios[k] = v
	}

	if common.IsBillingDebugEnabled() {
		logMap := make(map[string]any, len(otherRatios)+5)
		for k, v := range otherRatios {
			logMap[k] = v
		}
		logMap["function"] = "EstimateBilling"
		logMap["size_v"] = size
		logMap["resolution_v"] = resolution
		if audio != nil {
			logMap["audio_v"] = *audio
		}
		logMap["duration_v"] = duration
		logger.BillingDebugMap(c, logMap)
	}

	return otherRatios
}

func (a *TaskAdaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (*http.Response, error) {
	return channel.DoTaskApiRequest(a, c, info, requestBody)
}

// DoResponse handles upstream response
func (a *TaskAdaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (taskID string, taskData []byte, taskErr *taskdto.TaskError) {
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		taskErr = service.TaskErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError)
		return
	}
	_ = resp.Body.Close()

	var aliResp AliVideoResponse
	if err := common.Unmarshal(responseBody, &aliResp); err != nil {
		taskErr = service.TaskErrorWrapper(errors.Wrapf(err, "body: %s", responseBody), "unmarshal_response_body_failed", http.StatusInternalServerError)
		return
	}

	if aliResp.Code != "" {
		taskErr = service.TaskErrorWrapper(fmt.Errorf("%s: %s", aliResp.Code, aliResp.Message), "ali_api_error", resp.StatusCode)
		return
	}

	if aliResp.Output.TaskID == "" {
		taskErr = service.TaskErrorWrapper(fmt.Errorf("task_id is empty"), "invalid_response", http.StatusInternalServerError)
		return
	}

	// Ali 透传模式：直接输出原生 Ali 响应格式
	upstreamTaskID := aliResp.Output.TaskID // 备份上游ID，避免被下面的错误污染
	if isAliPassthrough, exists := c.Get("ali_passthrough"); exists && isAliPassthrough.(bool) {
		// 设置公共 task ID 到响应中（替换上游 task ID）
		aliResp.Output.TaskID = info.PublicTaskID
		c.JSON(http.StatusOK, aliResp)
		return upstreamTaskID, responseBody, nil
	}

	// 标准模式：输出 OpenAI 兼容格式
	openAIResp := dto.NewOpenAIVideo()
	openAIResp.ID = info.PublicTaskID
	openAIResp.TaskID = info.PublicTaskID
	openAIResp.Model = c.GetString("model")
	if openAIResp.Model == "" && info != nil {
		openAIResp.Model = info.OriginModelName
	}
	openAIResp.Status = convertAliStatus(aliResp.Output.TaskStatus)
	openAIResp.CreatedAt = common.GetTimestamp()

	c.JSON(http.StatusOK, openAIResp)

	return upstreamTaskID, responseBody, nil
}

func (a *TaskAdaptor) FetchTask(baseUrl, key string, body map[string]any, proxy string) (*http.Response, error) {
	taskID, ok := body["task_id"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid task_id")
	}

	uri := fmt.Sprintf("%s/api/v1/tasks/%s", baseUrl, url.PathEscape(taskID))

	req, err := http.NewRequest(http.MethodGet, uri, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+key)

	client, err := service.GetHttpClientWithProxy(proxy)
	if err != nil {
		return nil, fmt.Errorf("new proxy http client failed: %w", err)
	}
	return client.Do(req)
}

func (a *TaskAdaptor) GetModelList() []string {
	return ModelList
}

func (a *TaskAdaptor) GetChannelName() string {
	return ChannelName
}

func (a *TaskAdaptor) ParseTaskResult(respBody []byte) (*relaycommon.TaskInfo, error) {
	var aliResp AliVideoResponse
	if err := common.Unmarshal(respBody, &aliResp); err != nil {
		return nil, errors.Wrap(err, "unmarshal task result failed")
	}

	taskResult := relaycommon.TaskInfo{
		Code: 0,
	}

	switch aliResp.Output.TaskStatus {
	case "PENDING":
		taskResult.Status = model.TaskStatusQueued
	case "RUNNING":
		taskResult.Status = model.TaskStatusInProgress
	case "SUCCEEDED":
		taskResult.Status = model.TaskStatusSuccess
		taskResult.Url = aliResp.Output.VideoURL
	case "FAILED", "CANCELED", "UNKNOWN":
		taskResult.Status = model.TaskStatusFailure
		if aliResp.Message != "" {
			taskResult.Reason = aliResp.Message
		} else if aliResp.Output.Message != "" {
			taskResult.Reason = fmt.Sprintf("task failed, code: %s , message: %s", aliResp.Output.Code, aliResp.Output.Message)
		} else {
			taskResult.Reason = "task failed"
		}
	default:
		taskResult.Status = model.TaskStatusQueued
	}

	// 提取 Usage 用于任务完成后的计费重算
	if aliResp.Usage != nil && aliResp.Usage.Duration > 0 {
		taskResult.CompletionTokens = int(aliResp.Usage.Duration)
	}

	return &taskResult, nil
}

func (a *TaskAdaptor) ConvertToOpenAIVideo(task *model.Task) ([]byte, error) {
	var aliResp AliVideoResponse
	if err := common.Unmarshal(task.Data, &aliResp); err != nil {
		return nil, errors.Wrap(err, "unmarshal ali response failed")
	}

	openAIResp := dto.NewOpenAIVideo()
	openAIResp.ID = task.TaskID
	openAIResp.Status = convertAliStatus(aliResp.Output.TaskStatus)
	openAIResp.Model = task.Properties.OriginModelName
	openAIResp.SetProgressStr(task.Progress)
	openAIResp.CreatedAt = task.CreatedAt
	openAIResp.CompletedAt = task.UpdatedAt

	openAIResp.SetMetadata("url", aliResp.Output.VideoURL)

	if aliResp.Code != "" {
		openAIResp.Error = &dto.OpenAIVideoError{
			Code:    aliResp.Code,
			Message: aliResp.Message,
		}
	} else if aliResp.Output.Code != "" {
		openAIResp.Error = &dto.OpenAIVideoError{
			Code:    aliResp.Output.Code,
			Message: aliResp.Output.Message,
		}
	}

	return common.Marshal(openAIResp)
}

// ============================
// 辅助函数
// ============================

func boolPtr(b bool) *bool { return &b }

// isNewFormatModel 判断是否为新格式模型 (wan2.7+ 或 happyhorse)
func isNewFormatModel(model string) bool {
	return strings.HasPrefix(model, "wan2.7") || strings.HasPrefix(model, "happyhorse")
}

// getAliVideoEndpoint 根据模型返回对应的 API 路径
func getAliVideoEndpoint(model string) string {
	if strings.Contains(model, "kf2v") {
		return "/api/v1/services/aigc/image2video/video-synthesis"
	}
	return "/api/v1/services/aigc/video-generation/video-synthesis"
}

// 从任意类型获取String 数组
func getStringSlice(v any) ([]string, bool) {
	switch value := v.(type) {
	case []string:
		return value, true
	case []interface{}:
		items := make([]string, 0, len(value))
		for _, item := range value {
			s, ok := item.(string)
			if !ok {
				return nil, false
			}
			items = append(items, s)
		}
		return items, true
	default:
		return nil, false
	}
}

func toBool(v interface{}) (bool, bool) {
	switch val := v.(type) {
	case bool:
		return val, true
	case string:
		b, err := strconv.ParseBool(val)
		return b, err == nil
	default:
		return false, false
	}
}

func toInt(v interface{}) (int, bool) {
	switch val := v.(type) {
	case int:
		return val, true
	case int64:
		return int(val), true
	case float64:
		return int(val), true
	case string:
		n, err := strconv.Atoi(val)
		return n, err == nil
	default:
		return 0, false
	}
}

// ============================
// Image/Images/InputReference → Media 迁移
// ============================

// 仅支持图片参考，不支持视频参考与视频编辑：
var aliImageRefModels = []string{
	"wan2.6-i2v-flash", "wan2.6-i2v", "wan2.5-i2v-preview",
	"wan2.2-i2v-flash", "wan2.2-i2v-plus", "wan2.1-i2v-turbo", "wan2.1-i2v-plus",
}

// 首尾帧模型：first → first_frame_url，second → last_frame_url（wan2.x 系旧格式），
// wan3.0 系新格式为 first_frame / last_frame。
var aliKF2VModels = []string{"wan2.2-kf2v-flash", "wan2.1-kf2v-plus"}

// wan2.7-i2v：首帧/首片段自动判断图片或视频。
var aliFirstClipModels = []string{"wan2.7-i2v", "wan2.7-i2v-2026-04-25"}

// 仅首帧模型（新格式）。
var aliFirstFrameOnlyModels = []string{"happyhorse-1.1-i2v", "happyhorse-1.0-i2v"}

var aliNewFirstLastFrameModels = []string{"wan3.0-video", "wan3.0-video-prime"}

// taskImagePool 将 req.Image、req.Images 顺序拼接为图片来源数组。
func taskImagePool(req relaycommon.TaskSubmitReq) []string {
	pool := make([]string, 0, len(req.Images)+1)
	if req.Image != "" {
		pool = append(pool, req.Image)
	}
	pool = append(pool, req.Images...)
	return pool
}

// firstTaskImage 返回 Image/Images 顺序拼接后第一个非空值。
func firstTaskImage(req relaycommon.TaskSubmitReq) string {
	for _, img := range taskImagePool(req) {
		if img != "" {
			return img
		}
	}
	return ""
}

// secondTaskImage 返回 Image/Images 顺序拼接后第二个非空值。
func secondTaskImage(req relaycommon.TaskSubmitReq) string {
	found := 0
	for _, img := range taskImagePool(req) {
		if img == "" {
			continue
		}
		found++
		if found == 2 {
			return img
		}
	}
	return ""
}

// looksLikeVideoURL 判断首帧来源是视频还是图片：优先 data URI 的 MIME，
// 其次 URL 后缀，默认按图片处理。
func looksLikeVideoURL(url string) bool {
	lower := strings.ToLower(url)
	if strings.HasPrefix(lower, "data:video/") {
		return true
	}
	if strings.HasPrefix(lower, "data:image/") {
		return false
	}
	for _, ext := range []string{".mp4", ".mov", ".webm", ".avi", ".mkv", ".m4v"} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

// migrateTaskImagesToMedia 将 TaskSubmitReq 中的 Image/Images/InputReference 迁移到
// Media，便于兼容仅提供 image/images/input_reference 的调用格式（当前仅支持图片参考，
// 不支持视频参考与视频编辑）。req.Media 非空时跳过；迁移后清空原字段。
// 必须在 buildNewFormatRequest / buildOldFormatRequest 之前调用。
func migrateTaskImagesToMedia(model string, req *relaycommon.TaskSubmitReq) {
	if len(req.Media) > 0 {
		return
	}
	first, second := firstTaskImage(*req), secondTaskImage(*req)
	if first == "" {
		return
	}

	switch {
	case strContains(aliImageRefModels, model):
		req.Media = append(req.Media, relaycommon.Media{Type: "img_url", URL: first})
	case strContains(aliKF2VModels, model):
		req.Media = append(req.Media, relaycommon.Media{Type: "first_frame_url", URL: first})
		if second != "" {
			req.Media = append(req.Media, relaycommon.Media{Type: "last_frame_url", URL: second})
		}
	case strContains(aliFirstClipModels, model):
		if looksLikeVideoURL(first) {
			req.Media = append(req.Media, relaycommon.Media{Type: "first_clip", URL: first})
		} else {
			req.Media = append(req.Media, relaycommon.Media{Type: "first_frame", URL: first})
		}
		if second != "" {
			req.Media = append(req.Media, relaycommon.Media{Type: "last_frame", URL: second})
		}
	case strContains(aliFirstFrameOnlyModels, model):
		req.Media = append(req.Media, relaycommon.Media{Type: "first_frame", URL: first})
	case strContains(aliNewFirstLastFrameModels, model):
		req.Media = append(req.Media, relaycommon.Media{Type: "first_frame", URL: first})
		if second != "" {
			req.Media = append(req.Media, relaycommon.Media{Type: "last_frame", URL: second})
		}
	default:
		// 未收录的模型不做迁移，保持原有校验行为
		return
	}

	req.Image = ""
	req.Images = nil
}

// migrateTaskParamsToMetadata 将 TaskSubmitReq 中的 Size/Duration/Seconds 转移到
// Metadata（键 size/duration），兼容仅提供标准字段的调用格式；Metadata 已有对应
// 键时以 Metadata 为准，不覆盖。转移后清空原字段，必须在 buildNewFormatRequest /
// buildOldFormatRequest 之前调用。
func migrateTaskParamsToMetadata(req *relaycommon.TaskSubmitReq) {
	if req.Metadata == nil {
		req.Metadata = make(map[string]interface{})
	}
	if req.Size != "" {
		if _, ok := req.Metadata["size"]; !ok {
			req.Metadata["size"] = req.Size
		}
		req.Size = ""
	}
	if req.Duration > 0 {
		if _, ok := req.Metadata["duration"]; !ok {
			req.Metadata["duration"] = req.Duration
		}
	} else if req.Seconds != "" {
		if _, ok := req.Metadata["duration"]; !ok {
			req.Metadata["duration"] = req.Seconds
		}
	}
	req.Duration = 0
	req.Seconds = ""
}

// buildOldFormatRequest 构建旧格式请求 (wan2.1 ~ wan2.6)
func buildOldFormatRequest(model string, req relaycommon.TaskSubmitReq) (*AliOldVideoRequest, error) {
	aliReq := &AliOldVideoRequest{
		Model: model,
		Input: AliOldVideoInput{
			Prompt: req.Prompt,
		},
		Parameters: &AliOldVideoParameters{
			PromptExtend: boolPtr(true),
			Watermark:    boolPtr(false),
		},
	}

	// 字段可以从多个域获取，优先级为，先看 Metadata， 再看 Media
	if req.Metadata != nil {
		// 同步 input 字段
		// 如果Meta中存在input的key，则直接使用input来替换
		tmpInput := req.Metadata
		rawInput, ok := req.Metadata["input"]
		if ok && rawInput != nil {
			inputMap, ok := rawInput.(map[string]interface{})
			if ok {
				tmpInput = inputMap
			}
		}

		if v, ok := tmpInput["prompt"].(string); ok {
			aliReq.Input.Prompt = v
		}
		if v, ok := tmpInput["img_url"].(string); ok {
			aliReq.Input.ImgURL = v
		}
		if v, ok := tmpInput["audio_url"].(string); ok {
			aliReq.Input.AudioURL = v
		}
		if v, ok := tmpInput["negative_prompt"].(string); ok {
			aliReq.Input.NegativePrompt = v
		}
		if v, ok := tmpInput["template"].(string); ok {
			aliReq.Input.Template = v
		}
		if v, ok := tmpInput["reference_urls"]; ok {
			if urls, ok := getStringSlice(v); ok {
				aliReq.Input.ReferenceURLs = urls
			}
		}
		if v, ok := tmpInput["first_frame_url"].(string); ok {
			aliReq.Input.FirstFrameURL = v
		}
		if v, ok := tmpInput["last_frame_url"].(string); ok {
			aliReq.Input.LastFrameURL = v
		}
		if v, ok := tmpInput["function"].(string); ok {
			aliReq.Input.Function = v
		}
		if v, ok := tmpInput["ref_images_url"]; ok {
			if urls, ok := getStringSlice(v); ok {
				aliReq.Input.RefImagesURL = urls
			}
		}
		if v, ok := tmpInput["video_url"].(string); ok {
			aliReq.Input.VideoURL = v
		}
		if v, ok := tmpInput["first_clip_url"].(string); ok {
			aliReq.Input.FirstClipURL = v
		}
		if v, ok := tmpInput["last_clip_url"].(string); ok {
			aliReq.Input.LastClipURL = v
		}

		// 同步 parameter 字段
		if v, ok := req.Metadata["resolution"].(string); ok {
			v = strings.ToUpper(strings.TrimSpace(v))
			if !strings.HasSuffix(v, "P") {
				v = v + "P"
			}
			aliReq.Parameters.Resolution = strings.ToUpper(v)
		}
		if v, ok := req.Metadata["size"].(string); ok {
			if strings.Contains(v, "*") {
				aliReq.Parameters.Size = v
			}
		}
		if v, ok := req.Metadata["duration"]; ok {
			if n, ok := toInt(v); ok {
				aliReq.Parameters.Duration = n
			}
		}
		if v, ok := req.Metadata["prompt_extend"]; ok {
			if b, ok := toBool(v); ok {
				aliReq.Parameters.PromptExtend = &b
			}
		}
		if v, ok := req.Metadata["shot_type"].(string); ok {
			aliReq.Parameters.ShotType = v
		}
		if v, ok := req.Metadata["audio"]; ok {
			if b, ok := toBool(v); ok {
				aliReq.Parameters.Audio = &b
			}
		}
		if v, ok := req.Metadata["watermark"]; ok {
			if b, ok := toBool(v); ok {
				aliReq.Parameters.Watermark = &b
			}
		}
		if v, ok := req.Metadata["seed"]; ok {
			if n, ok := toInt(v); ok {
				aliReq.Parameters.Seed = &n
			}
		}
	}

	for _, m := range req.Media {
		if m.URL == "" {
			continue
		}
		switch strings.ToLower(m.Type) {
		case "img_url":
			aliReq.Input.ImgURL = m.URL
		case "audio_url":
			aliReq.Input.AudioURL = m.URL
		case "reference_urls":
			aliReq.Input.ReferenceURLs = append(aliReq.Input.ReferenceURLs, m.URL)
		case "first_frame_url":
			aliReq.Input.FirstFrameURL = m.URL
		case "last_frame_url":
			aliReq.Input.LastFrameURL = m.URL
		case "ref_images_url":
			aliReq.Input.RefImagesURL = append(aliReq.Input.RefImagesURL, m.URL)
		case "video_url":
			aliReq.Input.VideoURL = m.URL
		case "first_clip_url":
			aliReq.Input.FirstClipURL = m.URL
		case "last_clip_url":
			aliReq.Input.LastClipURL = m.URL
		}
	}

	if req.Mode != "" {
		return nil, fmt.Errorf("Parameter Key [ mode ] is unused")
	}
	if req.InputReference != "" {
		return nil, fmt.Errorf("Parameter Key [ input_reference ] is unused, use media please")
	}

	if aliReq.Parameters.Resolution == "" {
		aliReq.Parameters.Resolution = defaultResolution(model)
	}

	if aliReq.Parameters.Duration <= 0 {
		aliReq.Parameters.Duration = 5
	}
	return aliReq, nil
}

// buildNewFormatRequest 构建新格式请求 (wan2.7+, happyhorse)
func buildNewFormatRequest(model string, req relaycommon.TaskSubmitReq) (*AliNewVideoRequest, error) {
	aliReq := &AliNewVideoRequest{
		Model: model,
		Input: AliNewVideoInput{
			Prompt: req.Prompt,
		},
		Parameters: &AliNewVideoParameters{
			PromptExtend: boolPtr(true),
			Watermark:    boolPtr(false),
		},
	}

	// 字段可以从多个域获取，优先级为，先看 Metadata， 再看 Media
	if req.Metadata != nil {
		// 同步 input 字段
		// 如果Meta中存在input的key，则直接使用input来替换
		tmpInput := req.Metadata
		rawInput, ok := req.Metadata["input"]
		if ok && rawInput != nil {
			inputMap, ok := rawInput.(map[string]interface{})
			if ok {
				tmpInput = inputMap
			}
		}

		if v, ok := tmpInput["prompt"].(string); ok {
			aliReq.Input.Prompt = v
		}
		if v, ok := tmpInput["negative_prompt"].(string); ok {
			aliReq.Input.NegativePrompt = v
		}
		if v, ok := tmpInput["audio_url"].(string); ok {
			aliReq.Input.AudioURL = v
		}
		if mediaList, ok := tmpInput["media"].([]interface{}); ok {
			for _, item := range mediaList {
				if m, ok := item.(map[string]interface{}); ok {
					var media AliVideoMedia
					if t, ok := m["type"].(string); ok {
						media.Type = t
					}
					if u, ok := m["url"].(string); ok {
						media.URL = u
					}
					aliReq.Input.Media = append(aliReq.Input.Media, media)
				}
			}
		}

		// 同步 parameter 字段
		if v, ok := req.Metadata["resolution"].(string); ok {
			v = strings.ToUpper(strings.TrimSpace(v))
			if !strings.HasSuffix(v, "P") {
				v = v + "P"
			}
			aliReq.Parameters.Resolution = strings.ToUpper(v)
		}
		if v, ok := req.Metadata["duration"]; ok {
			if n, ok := toInt(v); ok {
				aliReq.Parameters.Duration = n
			}
		}
		if v, ok := req.Metadata["prompt_extend"]; ok {
			if b, ok := toBool(v); ok {
				aliReq.Parameters.PromptExtend = &b
			}
		}
		if v, ok := req.Metadata["watermark"]; ok {
			if b, ok := toBool(v); ok {
				aliReq.Parameters.Watermark = &b
			}
		}
		if v, ok := req.Metadata["seed"]; ok {
			if n, ok := toInt(v); ok {
				aliReq.Parameters.Seed = &n
			}
		}
		if v, ok := req.Metadata["ratio"].(string); ok {
			if strings.Contains(v, ":") {
				aliReq.Parameters.Ratio = v
			}
		}
		if v, ok := req.Metadata["audio_setting"].(string); ok {
			aliReq.Parameters.AudioSetting = v
		}
	}

	for _, m := range req.Media {
		if m.URL == "" {
			continue
		}
		switch strings.ToLower(m.Type) {
		case "audio_url":
			aliReq.Input.AudioURL = m.URL
		}
		aliReq.Input.Media = append(aliReq.Input.Media, AliVideoMedia{
			Type: m.Type,
			URL:  m.URL,
		})
	}

	if req.Mode != "" {
		return nil, fmt.Errorf("Parameter Key [ mode ] is unused")
	}
	if req.InputReference != "" {
		return nil, fmt.Errorf("Parameter Key [ input_reference ] is unused, use media please")
	}

	if aliReq.Parameters.Resolution == "" {
		aliReq.Parameters.Resolution = "1080P"
	}

	if aliReq.Parameters.Duration <= 0 {
		aliReq.Parameters.Duration = 5
	}

	return aliReq, nil
}

// defaultResolution 返回模型的默认分辨率
func defaultResolution(model string) string {
	switch {
	case strings.HasPrefix(model, "wan2.6"):
		return "1080P"
	case strings.HasPrefix(model, "wan2.5"):
		return "1080P"
	case strings.HasPrefix(model, "wan2.2-i2v-flash"):
		return "720P"
	case strings.HasPrefix(model, "wan2.2-i2v-plus"):
		return "1080P"
	default:
		return "720P"
	}
}

// sizeToResolution 将像素尺寸映射到分辨率标签
var (
	size480p  = []string{"832*480", "480*832", "624*624"}
	size720p  = []string{"1280*720", "720*1280", "960*960", "1088*832", "832*1088"}
	size1080p = []string{"1920*1080", "1080*1920", "1440*1440", "1632*1248", "1248*1632"}
)

func sizeToResolution(size string) (string, error) {
	if strContains(size480p, size) {
		return "480P", nil
	} else if strContains(size720p, size) {
		return "720P", nil
	} else if strContains(size1080p, size) {
		return "1080P", nil
	}
	return "", fmt.Errorf("invalid size: %s", size)
}

func strContains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// ProcessAliOtherRatios 计算分辨率和音频计费倍率
func ProcessAliOtherRatios(model, size, resolution string, audio *bool) (map[string]float64, error) {
	otherRatios := make(map[string]float64)

	aliRatios := map[string]map[string]float64{
		"wan3.0-video":       {"480P": 1, "720P": 2, "1080P": 4},
		"wan3.0-video-prime": {"480P": 1, "720P": 2, "1080P": 4},

		"wan2.7-t2v":         {"720P": 1, "1080P": 1 / 0.6},
		"wan2.6-t2v":         {"720P": 1, "1080P": 1 / 0.6},
		"wan2.5-t2v-preview": {"480P": 1, "720P": 2, "1080P": 1 / 0.3},
		"wan2.2-t2v-plus":    {"480P": 1, "1080P": 0.7 / 0.14},
		"wanx2.1-t2v-turbo":  {"480P": 1, "720P": 1},
		"wanx2.1-t2v-plus":   {"720P": 1},

		"wan2.7-i2v":         {"720P": 1, "1080P": 1 / 0.6},
		"wan2.6-i2v":         {"720P": 1, "1080P": 1 / 0.6},
		"wan2.6-i2v-flash":   {"720P": 1, "1080P": 1 / 0.6},
		"wan2.5-i2v-preview": {"480P": 1, "720P": 2, "1080P": 1 / 0.3},
		"wan2.2-i2v-flash":   {"480P": 1, "720P": 2, "1080P": 4.8},
		"wan2.2-i2v-plus":    {"480P": 1, "1080P": 5},
		"wanx2.1-i2v-turbo":  {"480P": 1, "720P": 1},
		"wanx2.1-i2v-plus":   {"720P": 1},

		"wan2.2-kf2v-flash": {"480P": 1, "720P": 2, "1080P": 4.8},
		"wanx2.1-kf2v-plus": {"720P": 1},

		"wan2.7-r2v":       {"720P": 1, "1080P": 1 / 0.6},
		"wan2.6-r2v":       {"720P": 1, "1080P": 1 / 0.6},
		"wan2.6-r2v-flash": {"720P": 1, "1080P": 1 / 0.6},

		"wan2.7-videoedit":  {"720P": 1, "1080P": 1 / 0.6},
		"wanx2.1-vace-plus": {"720P": 1},

		"happyhorse-1.0-t2v":        {"720P": 1, "1080P": 1.6 / 0.9},
		"happyhorse-1.0-i2v":        {"720P": 1, "1080P": 1.6 / 0.9},
		"happyhorse-1.0-r2v":        {"720P": 1, "1080P": 1.6 / 0.9},
		"happyhorse-1.0-video-edit": {"720P": 1, "1080P": 1.6 / 0.9},

		"happyhorse-1.1-t2v": {"720P": 1, "1080P": 1.2 / 0.9},
		"happyhorse-1.1-i2v": {"720P": 1, "1080P": 1.2 / 0.9},
		"happyhorse-1.1-r2v": {"720P": 1, "1080P": 1.2 / 0.9},

		"wan2.2-s2v": {"480P": 1, "720P": 0.9 / 0.5},
	}

	var resolvedRes string
	if size != "" {
		toRes, err := sizeToResolution(size)
		if err != nil {
			return nil, err
		}
		resolvedRes = toRes
	} else {
		resolvedRes = strings.ToUpper(resolution)
		if !strings.HasSuffix(resolvedRes, "P") {
			resolvedRes = resolvedRes + "P"
		}
	}

	modelRatios := findModelRatios(model, aliRatios)
	if modelRatios != nil {
		if ratio, ok := modelRatios[resolvedRes]; ok {
			otherRatios[fmt.Sprintf("resolution-%s", resolvedRes)] = ratio
		}
	}

	if audio != nil && *audio {
		if strings.HasSuffix(model, "-i2v-flash") || strings.HasSuffix(model, "-r2v-flash") {
			otherRatios["audio"] = 2
		}
	}

	return otherRatios, nil
}

// findModelRatios 使用最长前缀匹配查找模型的计费倍率
func findModelRatios(model string, aliRatios map[string]map[string]float64) map[string]float64 {
	if ratios, ok := aliRatios[model]; ok {
		return ratios
	}

	type entry struct {
		prefix string
		ratios map[string]float64
	}
	var entries []entry
	for prefix, ratios := range aliRatios {
		if strings.HasPrefix(model, prefix) {
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

// AdjustBillingOnComplete 任务完成时基于 AliUsage 实际值重新计算扣费。
// 依赖：Duration（实际时长）、VideoCount（视频数量）、SR（超分辨率）、继承的 audio 设置。
// 通过 actualOtherProduct / estimatedOtherProduct 缩放预扣额度。
func (a *TaskAdaptor) AdjustBillingOnComplete(task *model.Task, taskResult *relaycommon.TaskInfo) int {
	if taskResult.Status != model.TaskStatusSuccess {
		return 0
	}

	var aliResp AliVideoResponse
	if err := common.Unmarshal(task.Data, &aliResp); err != nil {
		return 0
	}
	if aliResp.Usage == nil || aliResp.Usage.Duration <= 0 {
		return 0
	}

	bc := task.PrivateData.BillingContext
	if bc == nil || len(bc.OtherRatios) == 0 {
		return 0
	}

	// 1. 提取实际值
	actualDuration := float64(aliResp.Usage.Duration)
	videoCount := float64(aliResp.Usage.VideoCount)
	if videoCount <= 0 {
		videoCount = 1
	}
	sr := int(aliResp.Usage.SR)

	// 2. 获取预估秒数
	estimatedSeconds, ok := bc.OtherRatios["seconds"]
	if !ok || estimatedSeconds <= 0 {
		return 0
	}

	// 3. 确定模型名
	modelName := bc.OriginModelName
	if modelName == "" {
		modelName = task.Properties.OriginModelName
	}
	if modelName == "" {
		modelName = task.Properties.UpstreamModelName
	}

	// 4. 从预估 OtherRatios 推断原始分辨率和 audio 状态
	var resolution string
	for key := range bc.OtherRatios {
		if strings.HasPrefix(key, "resolution-") {
			resolution = strings.TrimPrefix(key, "resolution-")
			break
		}
	}

	var audio *bool
	if _, hasAudio := bc.OtherRatios["audio"]; hasAudio {
		audioVal := true
		audio = &audioVal
	}

	// 5. SR 分辨率
	if sr > 0 {
		resolution = fmt.Sprintf("%dP", sr)
	}

	// 6. 用实际分辨率重新计算 ratio
	newRatios, err := ProcessAliOtherRatios(modelName, "", resolution, audio)
	if err != nil {
		// 降级：仅按时长和视频数量缩放
		actualQuota := int(float64(task.Quota) * actualDuration * videoCount / estimatedSeconds)
		if actualQuota < 0 {
			return 0
		}
		return actualQuota
	}

	// 7. 计算实际 other 乘积：时长 x 视频数 x 分辨率ratio x [audio ratio]
	actualMultiplier := actualDuration * videoCount
	for _, v := range newRatios {
		if v > 0 {
			actualMultiplier *= v
		}
	}

	// 8. 计算预估 other 乘积（来自 BillingContext）
	estimatedMultiplier := 1.0
	for _, v := range bc.OtherRatios {
		if v > 0 {
			estimatedMultiplier *= v
		}
	}
	if estimatedMultiplier <= 0 {
		return 0
	}

	// 9. 缩放得到实际应扣额度
	actualQuota := int(float64(task.Quota) * actualMultiplier / estimatedMultiplier)
	if actualQuota < 0 {
		return 0
	}

	if common.IsBillingDebugEnabled() {
		logMap := make(map[string]any, len(newRatios)+5)
		for k, v := range newRatios {
			logMap[k] = v
		}
		logMap["function"] = "AdjustBillingOnComplete"
		logMap["resolution_v"] = resolution
		if audio != nil {
			logMap["audio_v"] = *audio
		}
		logMap["actualDuration_v"] = actualDuration
		logMap["task_quota"] = task.Quota
		logMap["videoCount"] = videoCount
		logMap["estimatedSeconds"] = estimatedSeconds
		logMap["actualMultiplier"] = actualMultiplier
		logMap["estimatedMultiplier"] = estimatedMultiplier
		logMap["actualQuota"] = actualQuota
		logger.BillingDebugMap(nil, logMap)
	}

	return actualQuota
}

func convertAliStatus(aliStatus string) string {
	switch aliStatus {
	case "PENDING":
		return dto.VideoStatusQueued
	case "RUNNING":
		return dto.VideoStatusInProgress
	case "SUCCEEDED":
		return dto.VideoStatusCompleted
	case "FAILED", "CANCELED", "UNKNOWN":
		return dto.VideoStatusFailed
	default:
		return dto.VideoStatusUnknown
	}
}
