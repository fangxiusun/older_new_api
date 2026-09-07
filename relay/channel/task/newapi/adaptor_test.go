package newapi

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

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
		{name: "completed", body: `{"id":"up-1","status":"completed","progress":100,"output":{"video_url":"https://cdn.example.com/video.mp4"}}`, wantStatus: "SUCCESS"},
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
	assert.JSONEq(t, `{"id":"task_public_abc","task_id":"task_public_abc","object":"video","model":"sora-2","status":"queued","progress":0}`, recorder.Body.String())
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

func TestConvertToOpenAIVideoReplacesID(t *testing.T) {
	task := &model.Task{
		TaskID: "task_public_abc",
		Data:   []byte(`{"id":"video_upstream_123","status":"completed"}`),
	}
	data, err := (&TaskAdaptor{}).ConvertToOpenAIVideo(task)
	require.NoError(t, err)
	assert.JSONEq(t, `{"id":"task_public_abc","task_id":"task_public_abc","status":"completed"}`, string(data))
}
