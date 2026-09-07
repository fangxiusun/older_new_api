package ali

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestBuildRequestBodyPreservesStandardImages(t *testing.T) {
	tests := []struct {
		name  string
		body  string
		model string
		want  map[string]string
	}{
		{
			name: "wan3 uses media protocol",
			body: `{"model":"wan3.0-video","prompt":"animate","images":["first.png","last.png"]}`,
			want: map[string]string{"input.media.0.type": "first_frame", "input.media.0.url": "first.png", "input.media.1.type": "last_frame", "input.media.1.url": "last.png"},
		},
		{
			name:  "model mapping selects wan3 protocol",
			body:  `{"model":"video-alias","prompt":"animate","image":"first.png"}`,
			model: "wan3.0-video-prime",
			want:  map[string]string{"model": "wan3.0-video-prime", "input.media.0.type": "first_frame", "input.media.0.url": "first.png", "input.media.#": "1"},
		},
		{
			name: "wanx image reference",
			body: `{"model":"wanx2.1-i2v-turbo","prompt":"animate","image":"first.png"}`,
			want: map[string]string{"input.img_url": "first.png"},
		},
		{
			name: "wanx first and last frames",
			body: `{"model":"wanx2.1-kf2v-plus","prompt":"animate","images":["first.png","last.png"]}`,
			want: map[string]string{"input.first_frame_url": "first.png", "input.last_frame_url": "last.png"},
		},
		{
			name: "single image does not become a last frame",
			body: `{"model":"wan2.2-kf2v-flash","prompt":"animate","image":"first.png"}`,
			want: map[string]string{"input.first_frame_url": "first.png", "input.last_frame_url": ""},
		},
		{
			name: "dated model keeps image reference",
			body: `{"model":"wan2.6-i2v-2026-04-25","prompt":"animate","image":"first.png"}`,
			want: map[string]string{"input.img_url": "first.png"},
		},
		{
			name: "signed video URL remains a first clip",
			body: `{"model":"wan2.7-i2v","prompt":"animate","image":"https://example.com/clip.MP4?signature=test#video"}`,
			want: map[string]string{"input.media.0.type": "first_clip"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(tt.body))
			c.Request.Header.Set("Content-Type", "application/json")
			info := &relaycommon.RelayInfo{
				TaskRelayInfo: &relaycommon.TaskRelayInfo{},
				ChannelMeta:   &relaycommon.ChannelMeta{IsModelMapped: tt.model != "", UpstreamModelName: tt.model},
			}
			a := &TaskAdaptor{}
			require.Nil(t, a.ValidateRequestAndSetAction(c, info))
			body, err := a.BuildRequestBody(c, info)
			require.NoError(t, err)
			data, err := io.ReadAll(body)
			require.NoError(t, err)
			for path, want := range tt.want {
				assert.Equal(t, want, gjson.GetBytes(data, path).String(), path)
			}
		})
	}
}

func TestAliMigrationDoesNotMutateCachedMetadata(t *testing.T) {
	req := relaycommon.TaskSubmitReq{
		Size: "1280*720", Duration: 10,
		Metadata: map[string]interface{}{"seed": 42},
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("task_request", req)
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{IsModelMapped: true, UpstreamModelName: "wan2.5-t2v-preview"}}
	(&TaskAdaptor{}).EstimateBilling(c, info)
	stored, err := relaycommon.GetTaskRequest(c)
	require.NoError(t, err)
	assert.Equal(t, map[string]interface{}{"seed": 42}, stored.Metadata)
}

func TestAliRejectsUnmappedImages(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("task_request", relaycommon.TaskSubmitReq{Model: "wan2.6-t2v", Prompt: "animate", Image: "first.png"})
	_, err := (&TaskAdaptor{}).BuildRequestBody(c, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}})
	require.ErrorContains(t, err, "image")
}

func TestAliStandardSizeMatchesBillingAndRequest(t *testing.T) {
	for _, modelName := range []string{"wan2.5-t2v-preview", "wan3.0-video"} {
		for _, size := range []string{"1280x720", "1280*720", "720P"} {
			t.Run(modelName+"/"+size, func(t *testing.T) {
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Set("task_request", relaycommon.TaskSubmitReq{Model: modelName, Prompt: "animate", Size: size, Duration: 10})
				info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
				a := &TaskAdaptor{}
				ratios := a.EstimateBilling(c, info)
				assert.Equal(t, 10.0, ratios["seconds"])
				assert.Equal(t, 2.0, ratios["resolution-720P"])
				body, err := a.BuildRequestBody(c, info)
				require.NoError(t, err)
				data, err := io.ReadAll(body)
				require.NoError(t, err)
				var request struct {
					Parameters struct {
						Size       string
						Resolution string
						Duration   int
					}
				}
				require.NoError(t, common.Unmarshal(data, &request))
				assert.Equal(t, 10, request.Parameters.Duration)
				if request.Parameters.Size != "" {
					assert.Equal(t, "1280*720", request.Parameters.Size)
				} else {
					assert.Equal(t, "720P", request.Parameters.Resolution)
				}
			})
		}
	}
}
