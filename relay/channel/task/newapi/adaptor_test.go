package newapi

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseTaskResultStatusMapping(t *testing.T) {
	tests := []struct {
		name         string
		body         string
		wantStatus   string
		wantReason   string
		wantProgress string
	}{
		{name: "queued", body: `{"id":"up-1","status":"queued","progress":0}`, wantStatus: "QUEUED"},
		{name: "pending", body: `{"id":"up-1","status":"pending"}`, wantStatus: "QUEUED"},
		{name: "submitted", body: `{"id":"up-1","status":"submitted"}`, wantStatus: "SUBMITTED"},
		{name: "processing", body: `{"id":"up-1","status":"processing","progress":50}`, wantStatus: "IN_PROGRESS", wantProgress: "50%"},
		{name: "in_progress", body: `{"id":"up-1","status":"in_progress"}`, wantStatus: "IN_PROGRESS"},
		{name: "completed", body: `{"id":"up-1","status":"completed","progress":100}`, wantStatus: "SUCCESS"},
		{
			name:       "failed with message",
			body:       `{"id":"up-1","status":"failed","error":{"message":"content policy violation","code":"moderation"}}`,
			wantStatus: "FAILURE",
			wantReason: "content policy violation",
		},
		{name: "failed without message", body: `{"id":"up-1","status":"failed"}`, wantStatus: "FAILURE", wantReason: "task failed"},
		{name: "cancelled", body: `{"id":"up-1","status":"cancelled"}`, wantStatus: "FAILURE", wantReason: "task failed"},
		{name: "unknown status", body: `{"id":"up-1","status":"weird"}`, wantStatus: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := (&TaskAdaptor{}).ParseTaskResult([]byte(tt.body))
			require.NoError(t, err)
			assert.Equal(t, tt.wantStatus, result.Status)
			assert.Equal(t, tt.wantReason, result.Reason)
			assert.Equal(t, tt.wantProgress, result.Progress)
			assert.Equal(t, 0, result.Code)
		})
	}
}

func TestParseTaskResultInvalidBody(t *testing.T) {
	_, err := (&TaskAdaptor{}).ParseTaskResult([]byte("not-json"))
	require.Error(t, err)
}

func newBillingContext(t *testing.T, body string) *gin.Context {
	t.Helper()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/video/generations", bytes.NewReader([]byte(body)))
	c.Request.Header.Set("Content-Type", "application/json")
	var req relaycommon.TaskSubmitReq
	require.NoError(t, common.UnmarshalBodyReusable(c, &req))
	c.Set("task_request", req)
	return c
}

func TestEstimateBillingSecondsAndSize(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		action     string
		wantRatios map[string]float64
	}{
		{name: "default seconds and size", body: `{}`, wantRatios: map[string]float64{"seconds": 4, "size": 1}},
		{name: "explicit seconds", body: `{"seconds":"8"}`, wantRatios: map[string]float64{"seconds": 8, "size": 1}},
		{name: "duration fallback", body: `{"duration":10}`, wantRatios: map[string]float64{"seconds": 10, "size": 1}},
		{name: "wide size ratio", body: `{"seconds":"4","size":"1792x1024"}`, wantRatios: map[string]float64{"seconds": 4, "size": 1.666667}},
		{name: "tall size ratio", body: `{"seconds":"4","size":"1024x1792"}`, wantRatios: map[string]float64{"seconds": 4, "size": 1.666667}},
		{name: "other size no ratio", body: `{"seconds":"4","size":"1280x720"}`, wantRatios: map[string]float64{"seconds": 4, "size": 1}},
		{name: "remix returns nil", body: `{}`, action: constant.TaskActionRemix, wantRatios: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{
				ChannelMeta:   &relaycommon.ChannelMeta{UpstreamModelName: "sora-2"},
				TaskRelayInfo: &relaycommon.TaskRelayInfo{Action: tt.action},
			}
			if tt.action == constant.TaskActionRemix {
				ratios := (&TaskAdaptor{}).EstimateBilling(nil, info)
				assert.Equal(t, tt.wantRatios, ratios)
				return
			}
			c := newBillingContext(t, tt.body)
			ratios := (&TaskAdaptor{}).EstimateBilling(c, info)
			assert.Equal(t, tt.wantRatios, ratios)
		})
	}
}

func TestDoResponseReplacesTaskIDWithPublicID(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)

	upstreamBody := `{"id":"video_upstream_123","object":"video","model":"sora-2","status":"queued","progress":0}`
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader([]byte(upstreamBody))),
	}
	info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{PublicTaskID: "task_public_abc"}}

	taskID, taskData, taskErr := (&TaskAdaptor{}).DoResponse(c, resp, info)
	require.Nil(t, taskErr)
	assert.Equal(t, "video_upstream_123", taskID)
	assert.Equal(t, upstreamBody, string(taskData))

	assert.Equal(t, http.StatusOK, c.Writer.Status())
	assert.JSONEq(t, `{"id":"task_public_abc","task_id":"task_public_abc","object":"video","model":"sora-2","status":"queued","progress":0,"created_at":0}`, recorder.Body.String())
}

func TestDoResponseFallsBackToLegacyTaskID(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/video/generations", nil)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader([]byte(`{"task_id":"legacy_1","status":"queued"}`))),
	}
	info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{PublicTaskID: "task_public_abc"}}

	taskID, _, taskErr := (&TaskAdaptor{}).DoResponse(c, resp, info)
	require.Nil(t, taskErr)
	assert.Equal(t, "legacy_1", taskID)
}

func TestDoResponseEmptyTaskID(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader([]byte(`{"status":"queued"}`))),
	}
	info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{PublicTaskID: "task_public_abc"}}

	_, _, taskErr := (&TaskAdaptor{}).DoResponse(c, resp, info)
	require.NotNil(t, taskErr)
}

func TestProcessNewAPIOtherRatiosUsesBuiltInSizeRatio(t *testing.T) {
	original := videoSizeRatio
	videoSizeRatio = map[string]map[string]float64{
		"*":          {"1792x1024": 1.666667},
		"my-model":   {"720x1280": 2.5, "*": 1.2},
		"my-model-x": {"720x1280": 0.5},
	}
	t.Cleanup(func() { videoSizeRatio = original })

	assert.Equal(t, map[string]float64{"seconds": 6, "size": 2.5}, ProcessNewAPIOtherRatios("my-model", "720x1280", 6))
	// 模型内 "*" 通配
	assert.Equal(t, map[string]float64{"seconds": 6, "size": 1.2}, ProcessNewAPIOtherRatios("my-model", "1280x720", 6))
	// 最长前缀匹配
	assert.Equal(t, map[string]float64{"seconds": 6, "size": 0.5}, ProcessNewAPIOtherRatios("my-model-x", "720x1280", 6))
	// 无模型配置时走全局 "*" 通配
	assert.Equal(t, map[string]float64{"seconds": 6, "size": 1.666667}, ProcessNewAPIOtherRatios("other-model", "1792x1024", 6))
	// 全局 "*" 也未命中时回退 1
	assert.Equal(t, map[string]float64{"seconds": 6, "size": 1}, ProcessNewAPIOtherRatios("other-model", "3840x2160", 6))
}

func TestGetVideoSizeRatioBuiltInTable(t *testing.T) {
	// 内置表：sora 系按宽高比，kling 系按 720p/1080p
	assert.Equal(t, 1.0, getVideoSizeRatio("sora-2", "720x1280"))
	assert.Equal(t, 1.666667, getVideoSizeRatio("sora-2", "1792x1024"))
	assert.Equal(t, 1.6/0.9, getVideoSizeRatio("kling-v1-6", "1080p"))
	// 前缀模型未配置的尺寸回退全局 "*"
	assert.Equal(t, 1.666667, getVideoSizeRatio("kling-v1-6", "1792x1024"))
	// 完全未知模型走全局通配或 1
	assert.Equal(t, 1.666667, getVideoSizeRatio("unknown-model", "1792x1024"))
	assert.Equal(t, 1.0, getVideoSizeRatio("unknown-model", "720x1280"))
}

func TestConvertToOpenAIVideoReplacesID(t *testing.T) {
	task := &model.Task{
		TaskID: "task_public_abc",
		Data:   []byte(`{"id":"video_upstream_123","status":"completed"}`),
	}
	data, err := (&TaskAdaptor{}).ConvertToOpenAIVideo(task)
	require.NoError(t, err)
	assert.JSONEq(t, `{"id":"task_public_abc","status":"completed"}`, string(data))
}
