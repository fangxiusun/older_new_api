package newapi

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestDoResponsePreservesVideoFields(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
	upstream := `{"id":"upstream-id","task_id":"upstream-id","model":"upstream-model","status":"queued","seconds":"8","size":"1792x1024","metadata":{"seed":9007199254740993}}`
	resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(upstream))}
	info := &relaycommon.RelayInfo{OriginModelName: "client-model", TaskRelayInfo: &relaycommon.TaskRelayInfo{PublicTaskID: "task_local"}}
	id, data, taskErr := (&TaskAdaptor{}).DoResponse(c, resp, info)
	require.Nil(t, taskErr)
	assert.Equal(t, "upstream-id", id)
	assert.Equal(t, upstream, string(data))
	assert.Equal(t, "8", gjson.GetBytes(recorder.Body.Bytes(), "seconds").String())
	assert.Equal(t, "1792x1024", gjson.GetBytes(recorder.Body.Bytes(), "size").String())
	assert.Equal(t, "9007199254740993", gjson.GetBytes(recorder.Body.Bytes(), "metadata.seed").Raw)
	assert.Equal(t, "client-model", gjson.GetBytes(recorder.Body.Bytes(), "model").String())
}

func TestConvertToOpenAIVideoUsesLocalTaskState(t *testing.T) {
	task := &model.Task{TaskID: "task_local", Status: model.TaskStatusSuccess, Data: []byte(`{"id":"upstream","task_id":"upstream","status":"completed","metadata":{"url":"https://cdn.example.com/result.mp4"}}`)}
	data, err := (&TaskAdaptor{}).ConvertToOpenAIVideo(task)
	require.NoError(t, err)
	assert.Equal(t, "task_local", gjson.GetBytes(data, "task_id").String())
	assert.Equal(t, "https://cdn.example.com/result.mp4", gjson.GetBytes(data, "metadata.url").String())
	task.Status = model.TaskStatusFailure
	task.FailReason = "task timed out"
	task.Data = []byte(`{"id":"upstream","status":"queued"}`)
	data, err = (&TaskAdaptor{}).ConvertToOpenAIVideo(task)
	require.NoError(t, err)
	var response responseTask
	require.NoError(t, common.Unmarshal(data, &response))
	assert.Equal(t, "failed", response.Status)
	require.NotNil(t, response.Error)
	assert.Equal(t, "task timed out", response.Error.Message)
}
