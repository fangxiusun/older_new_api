package ali

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/require"
)

func TestBuildNewFormatRequestUsesMediaAndDefaults(t *testing.T) {
	req := relaycommon.TaskSubmitReq{
		Prompt: "animate the first frame",
		Media: []relaycommon.Media{
			{Type: "first_frame", URL: "https://example.com/first.png"},
			{Type: "last_frame", URL: "https://example.com/last.png"},
			{Type: "", URL: ""},
		},
	}

	aliReq, err := buildNewFormatRequest("wan2.7-i2v", req)

	require.NoError(t, err)
	require.Equal(t, "wan2.7-i2v", aliReq.Model)
	require.Equal(t, "animate the first frame", aliReq.Input.Prompt)
	require.Equal(t, []AliVideoMedia{
		{Type: "first_frame", URL: "https://example.com/first.png"},
		{Type: "last_frame", URL: "https://example.com/last.png"},
	}, aliReq.Input.Media)
	require.Equal(t, "1080P", aliReq.Parameters.Resolution)
	require.Equal(t, 5, aliReq.Parameters.Duration)
	require.NotNil(t, aliReq.Parameters.PromptExtend)
	require.True(t, *aliReq.Parameters.PromptExtend)
	require.NotNil(t, aliReq.Parameters.Watermark)
	require.False(t, *aliReq.Parameters.Watermark)

	body, err := common.Marshal(aliReq)
	require.NoError(t, err)
	require.Contains(t, string(body), `"media"`)
	require.NotContains(t, string(body), `"img_url"`)
}

func TestBuildNewFormatRequestReadsMetadataInputMediaAndParameters(t *testing.T) {
	req := relaycommon.TaskSubmitReq{
		Prompt: "fallback prompt",
		Metadata: map[string]interface{}{
			"input": map[string]interface{}{
				"prompt": "metadata prompt",
				"media": []interface{}{
					map[string]interface{}{
						"type": "first_clip",
						"url":  "https://example.com/input.mp4",
					},
				},
			},
			"resolution": " 720p ",
			"duration":   "10",
		},
	}

	aliReq, err := buildNewFormatRequest("wan2.7-i2v", req)

	require.NoError(t, err)
	require.Equal(t, "metadata prompt", aliReq.Input.Prompt)
	require.Equal(t, []AliVideoMedia{{Type: "first_clip", URL: "https://example.com/input.mp4"}}, aliReq.Input.Media)
	require.Equal(t, "720P", aliReq.Parameters.Resolution)
	require.Equal(t, 10, aliReq.Parameters.Duration)
}

func TestBuildNewFormatRequestRejectsLegacyMediaFields(t *testing.T) {
	tests := []struct {
		name string
		req  relaycommon.TaskSubmitReq
		want string
	}{
		{name: "image", req: relaycommon.TaskSubmitReq{Image: "https://example.com/image.png"}, want: "image"},
		{name: "images", req: relaycommon.TaskSubmitReq{Images: []string{"https://example.com/image.png"}}, want: "images"},
		{name: "size", req: relaycommon.TaskSubmitReq{Size: "1280*720"}, want: "size"},
		{name: "duration", req: relaycommon.TaskSubmitReq{Duration: 10}, want: "duration"},
		{name: "seconds", req: relaycommon.TaskSubmitReq{Seconds: "10"}, want: "seconds"},
		{name: "input reference", req: relaycommon.TaskSubmitReq{InputReference: "https://example.com/ref.png"}, want: "input_reference"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := buildNewFormatRequest("wan2.7-i2v", tt.req)
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestBuildOldFormatRequestMapsMediaAndMetadata(t *testing.T) {
	req := relaycommon.TaskSubmitReq{
		Prompt: "make a video",
		Media: []relaycommon.Media{
			{Type: "img_url", URL: "https://example.com/image.png"},
			{Type: "audio_url", URL: "https://example.com/audio.mp3"},
			{Type: "reference_urls", URL: "https://example.com/reference.png"},
			{Type: "first_frame_url", URL: "https://example.com/first.png"},
			{Type: "last_frame_url", URL: "https://example.com/last.png"},
		},
		Metadata: map[string]interface{}{
			"resolution":    "720p",
			"duration":      8,
			"prompt_extend": false,
			"watermark":     true,
			"seed":          42,
		},
	}

	aliReq, err := buildOldFormatRequest("wan2.5-i2v-preview", req)

	require.NoError(t, err)
	require.Equal(t, "make a video", aliReq.Input.Prompt)
	require.Equal(t, "https://example.com/image.png", aliReq.Input.ImgURL)
	require.Equal(t, "https://example.com/audio.mp3", aliReq.Input.AudioURL)
	require.Equal(t, []string{"https://example.com/reference.png"}, aliReq.Input.ReferenceURLs)
	require.Equal(t, "https://example.com/first.png", aliReq.Input.FirstFrameURL)
	require.Equal(t, "https://example.com/last.png", aliReq.Input.LastFrameURL)
	require.Equal(t, "720P", aliReq.Parameters.Resolution)
	require.Equal(t, 8, aliReq.Parameters.Duration)
	require.NotNil(t, aliReq.Parameters.PromptExtend)
	require.False(t, *aliReq.Parameters.PromptExtend)
	require.NotNil(t, aliReq.Parameters.Watermark)
	require.True(t, *aliReq.Parameters.Watermark)
	require.NotNil(t, aliReq.Parameters.Seed)
	require.Equal(t, 42, *aliReq.Parameters.Seed)

	body, err := common.Marshal(aliReq)
	require.NoError(t, err)
	require.Contains(t, string(body), `"img_url"`)
	require.NotContains(t, string(body), `"media"`)
}

func TestBuildOldFormatRequestUsesModelDefaultResolution(t *testing.T) {
	aliReq, err := buildOldFormatRequest("wan2.2-i2v-flash", relaycommon.TaskSubmitReq{})

	require.NoError(t, err)
	require.Equal(t, "720P", aliReq.Parameters.Resolution)
	require.Equal(t, 5, aliReq.Parameters.Duration)
}

func TestBuildOldFormatRequestRejectsLegacyMediaFields(t *testing.T) {
	_, err := buildOldFormatRequest("wan2.5-i2v-preview", relaycommon.TaskSubmitReq{
		Image: "https://example.com/image.png",
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "image")
}
