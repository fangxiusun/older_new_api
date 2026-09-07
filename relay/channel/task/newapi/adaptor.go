package newapi

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/task/ali"
	taskcommon "github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
	"github.com/tidwall/sjson"
)

// ============================
// Request / Response structures
// ============================

// responseTask 是上游 new-api 提交/查询接口返回的 OpenAI video 对象。
// 仅提取任务状态等必要字段；对外响应和入库均保留原始 JSON，不按此结构重新序列化。
type responseTask struct {
	ID       string `json:"id"`
	TaskID   string `json:"task_id,omitempty"` // 兼容旧接口
	Object   string `json:"object"`
	Model    string `json:"model"`
	Status   string `json:"status"`
	Progress int    `json:"progress"`
	// created_at 在不同响应中可能是 RFC3339 字符串或 Unix 秒，保留原文而不强制解码。
	Output struct {
		VideoURL string `json:"video_url"`
	} `json:"output"`
	Metadata struct {
		URL string `json:"url"`
	} `json:"metadata"`
	Error *struct {
		Message string `json:"message"`
		Code    string `json:"code"`
	} `json:"error,omitempty"`
}

// ============================
// Adaptor implementation
// ============================

// TaskAdaptor 将视频类异步任务透传到上游 new-api 实例。
// 上游对外提供与本项目一致的任务端点，因此请求与响应均为 OpenAI video 格式。
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

func (a *TaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) *dto.TaskError {
	if info.Action == constant.TaskActionRemix || c.GetBool("ali_passthrough") {
		return service.TaskErrorWrapperLocal(fmt.Errorf("newapi tasks only support direct-URL video generation; remix and native Ali requests are not supported"), "unsupported_task_action", http.StatusBadRequest)
	}
	contentType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil || contentType != "application/json" {
		return service.TaskErrorWrapperLocal(fmt.Errorf("newapi video tasks require application/json"), "invalid_request", http.StatusBadRequest)
	}
	var req relaycommon.TaskSubmitReq
	if err := common.UnmarshalBodyReusable(c, &req); err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_request", http.StatusBadRequest)
	}
	if info.OriginModelName == "" {
		info.OriginModelName = req.Model
	}
	if info.ChannelMeta == nil {
		info.ChannelMeta = &relaycommon.ChannelMeta{}
	}
	// 验证渠道映射后的真实上游名称；后续通用提交流程仍按原模型配置价格。
	info.UpstreamModelName = info.OriginModelName
	if err := helper.ModelMappedHelper(c, info, nil); err != nil {
		return service.TaskErrorWrapperLocal(err, "model_mapping_failed", http.StatusBadRequest)
	}
	if _, err := resolveTaskUpstream(info.ChannelBaseUrl); err != nil {
		return service.TaskErrorWrapperLocal(err, "unsupported_task_upstream", http.StatusBadRequest)
	}
	if _, err := resolveTaskModel(info.ChannelBaseUrl, info.UpstreamModelName); err != nil {
		return service.TaskErrorWrapperLocal(err, "unsupported_task_model", http.StatusBadRequest)
	}
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_request", http.StatusBadRequest)
	}
	body, err := storage.Bytes()
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_request", http.StatusBadRequest)
	}
	req, err = parseTaskBillingRequest(body, info.ChannelBaseUrl, info.UpstreamModelName)
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_request", http.StatusBadRequest)
	}
	if err := validateTaskBillingConfig(info.ChannelBaseUrl, info.UpstreamModelName, info.OriginModelName); err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_billing_config", http.StatusBadRequest)
	}
	info.Action = constant.TaskActionGenerate
	c.Set("task_request", req)
	return nil
}

// EstimateBilling 按渠道 URL 和模型计算倍率，与实际上游请求共用已校验和迁移的参数。
func (a *TaskAdaptor) EstimateBilling(c *gin.Context, info *relaycommon.RelayInfo) map[string]float64 {
	if info.Action == constant.TaskActionRemix {
		return nil
	}

	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil
	}

	ratios, err := estimateTaskBilling(info.ChannelBaseUrl, info.UpstreamModelName, req)
	if err != nil {
		return nil
	}
	return ratios
}

func (a *TaskAdaptor) BuildRequestURL(info *relaycommon.RelayInfo) (string, error) {
	if info.Action == constant.TaskActionRemix {
		return "", fmt.Errorf("newapi task remix is not supported")
	}
	if _, err := resolveTaskUpstream(a.baseURL); err != nil {
		return "", err
	}
	return strings.TrimRight(a.baseURL, "/") + "/v1/video/generations", nil
}

func (a *TaskAdaptor) BuildRequestHeader(c *gin.Context, req *http.Request, info *relaycommon.RelayInfo) error {
	req.Header.Set("Authorization", "Bearer "+a.apiKey)
	req.Header.Set("Content-Type", c.Request.Header.Get("Content-Type"))
	return nil
}

func (a *TaskAdaptor) BuildRequestBody(c *gin.Context, info *relaycommon.RelayInfo) (io.Reader, error) {
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil, err
	}
	if _, err := estimateTaskBilling(info.ChannelBaseUrl, info.UpstreamModelName, req); err != nil {
		return nil, err
	}
	// 请求结构和媒体迁移由模型分支确定，这里只序列化同一份已校验参数。
	// 保留完整上游模型名称，包含其路径和后缀。
	req.Model = info.UpstreamModelName
	body, err := common.Marshal(req)
	if err != nil {
		return nil, errors.Wrap(err, "marshal_request_failed")
	}
	return bytes.NewReader(body), nil
}

func (a *TaskAdaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (*http.Response, error) {
	return channel.DoTaskApiRequest(a, c, info, requestBody)
}

func (a *TaskAdaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (taskID string, taskData []byte, taskErr *dto.TaskError) {
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		taskErr = service.TaskErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError)
		return
	}

	var dResp responseTask
	if err := common.Unmarshal(responseBody, &dResp); err != nil {
		taskErr = service.TaskErrorWrapper(errors.Wrapf(err, "body: %s", responseBody), "unmarshal_response_body_failed", http.StatusInternalServerError)
		return
	}

	upstreamID := dResp.ID
	if upstreamID == "" {
		upstreamID = dResp.TaskID
	}
	if upstreamID == "" {
		taskErr = service.TaskErrorWrapper(fmt.Errorf("task_id is empty"), "invalid_response", http.StatusInternalServerError)
		return
	}

	// 保留上游扩展字段，客户端只看到本站公开 ID 和原始模型名。
	publicTask := &model.Task{
		TaskID:     info.PublicTaskID,
		Data:       responseBody,
		Properties: model.Properties{OriginModelName: info.OriginModelName},
	}
	var publicBody []byte
	if c.GetBool("ali_passthrough") {
		publicBody, err = a.ConvertToAliVideo(publicTask)
	} else {
		publicBody, err = a.ConvertToOpenAIVideo(publicTask)
	}
	if err != nil {
		return "", nil, service.TaskErrorWrapper(err, "convert_response_failed", http.StatusInternalServerError)
	}
	c.Data(http.StatusOK, "application/json", publicBody)
	return upstreamID, responseBody, nil
}

// FetchTask 通过上游 /v1/video/generations/{task_id} 查询任务状态。
func (a *TaskAdaptor) FetchTask(baseUrl, key string, body map[string]any, proxy string) (*http.Response, error) {
	if _, err := resolveTaskUpstream(baseUrl); err != nil {
		return nil, err
	}
	taskID, ok := body["task_id"].(string)
	if !ok || strings.TrimSpace(taskID) == "" {
		return nil, fmt.Errorf("invalid task_id")
	}

	uri := fmt.Sprintf("%s/v1/video/generations/%s", strings.TrimRight(baseUrl, "/"), url.PathEscape(taskID))

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
	resTask := responseTask{}
	if err := common.Unmarshal(respBody, &resTask); err != nil {
		return nil, errors.Wrap(err, "unmarshal task result failed")
	}

	taskResult := relaycommon.TaskInfo{
		Code: 0,
	}

	switch resTask.Status {
	case "queued", "pending":
		taskResult.Status = model.TaskStatusQueued
	case "submitted":
		taskResult.Status = model.TaskStatusSubmitted
	case "processing", "in_progress":
		taskResult.Status = model.TaskStatusInProgress
	case "completed":
		taskResult.Status = model.TaskStatusSuccess
		taskResult.Url = resTask.Output.VideoURL
		if taskResult.Url == "" {
			taskResult.Url = resTask.Metadata.URL
		}
		videoURL, err := url.Parse(taskResult.Url)
		if err != nil || videoURL.Host == "" || (videoURL.Scheme != "https" && videoURL.Scheme != "http") {
			return nil, fmt.Errorf("completed newapi task has no directly downloadable HTTP(S) video URL")
		}
	case "failed", "cancelled":
		taskResult.Status = model.TaskStatusFailure
		if resTask.Error != nil {
			taskResult.Reason = resTask.Error.Message
		} else {
			taskResult.Reason = "task failed"
		}
	default:
	}
	if resTask.Progress > 0 && resTask.Progress < 100 {
		taskResult.Progress = fmt.Sprintf("%d%%", resTask.Progress)
	}

	return &taskResult, nil
}

func (a *TaskAdaptor) ConvertToOpenAIVideo(task *model.Task) ([]byte, error) {
	data := task.Data
	fields := map[string]any{"id": task.TaskID, "task_id": task.TaskID}
	if task.Properties.OriginModelName != "" {
		fields["model"] = task.Properties.OriginModelName
	}
	if task.Progress != "" {
		if progress, err := strconv.Atoi(strings.TrimSuffix(task.Progress, "%")); err == nil {
			fields["progress"] = min(max(progress, 0), 100)
		}
	}
	switch task.Status {
	case model.TaskStatusNotStart, model.TaskStatusSubmitted, model.TaskStatusQueued:
		fields["status"] = "queued"
	case model.TaskStatusInProgress:
		fields["status"] = "in_progress"
	case model.TaskStatusSuccess:
		fields["status"] = "completed"
		fields["progress"] = 100
		videoURL := task.PrivateData.ResultURL
		if videoURL == "" {
			result, err := a.ParseTaskResult(task.Data)
			if err != nil {
				return nil, err
			}
			videoURL = result.Url
		}
		if videoURL == "" {
			return nil, fmt.Errorf("completed newapi task has no video URL")
		}
		fields["output.video_url"] = videoURL
		fields["metadata.url"] = videoURL
	case model.TaskStatusFailure:
		fields["status"] = "failed"
		fields["progress"] = 100
		if task.FailReason != "" {
			fields["error.message"] = task.FailReason
		}
	}
	for path, value := range fields {
		var err error
		data, err = sjson.SetBytes(data, path, value)
		if err != nil {
			return nil, errors.Wrapf(err, "set %s failed", path)
		}
	}
	return data, nil
}

func (a *TaskAdaptor) ConvertToAliVideo(task *model.Task) ([]byte, error) {
	data, err := a.ConvertToOpenAIVideo(task)
	if err != nil {
		return nil, err
	}
	var video responseTask
	if err := common.Unmarshal(data, &video); err != nil {
		return nil, errors.Wrap(err, "unmarshal video response failed")
	}
	response := ali.AliVideoResponse{Output: ali.AliVideoOutput{TaskID: task.TaskID}}
	switch video.Status {
	case "queued", "pending", "submitted":
		response.Output.TaskStatus = "PENDING"
	case "processing", "in_progress":
		response.Output.TaskStatus = "RUNNING"
	case "completed":
		response.Output.TaskStatus = "SUCCEEDED"
		response.Output.VideoURL = video.Output.VideoURL
	case "failed", "cancelled":
		response.Output.TaskStatus = "FAILED"
	default:
		response.Output.TaskStatus = "UNKNOWN"
	}
	if video.Error != nil {
		response.Output.Code = video.Error.Code
		response.Output.Message = video.Error.Message
	}
	return common.Marshal(response)
}
