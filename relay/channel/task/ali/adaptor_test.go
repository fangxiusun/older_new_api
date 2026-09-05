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

func TestBuildNewFormatRequestRejectsLegacyFields(t *testing.T) {
	// image/images/size/duration/seconds 已改为前置迁移，仅 mode 与 input_reference 仍拒绝
	_, err := buildNewFormatRequest("wan2.7-i2v", relaycommon.TaskSubmitReq{InputReference: "https://example.com/ref.png"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "input_reference")

	_, err = buildNewFormatRequest("wan2.7-i2v", relaycommon.TaskSubmitReq{Mode: "std"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "mode")
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

func TestBuildOldFormatRequestRejectsLegacyFields(t *testing.T) {
	_, err := buildOldFormatRequest("wan2.5-i2v-preview", relaycommon.TaskSubmitReq{
		InputReference: "https://example.com/ref.png",
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "input_reference")
}

func TestFirstTaskImageAndSecondTaskImage(t *testing.T) {
	// Image + Images 顺序拼接，非空第一个/第二个
	req := relaycommon.TaskSubmitReq{Images: []string{"", "b.png", "c.png"}}
	require.Equal(t, "b.png", firstTaskImage(req))
	require.Equal(t, "c.png", secondTaskImage(req))

	// Image 优先于 Images
	req = relaycommon.TaskSubmitReq{Image: "a.png", Images: []string{"b.png"}}
	require.Equal(t, "a.png", firstTaskImage(req))
	require.Equal(t, "b.png", secondTaskImage(req))

	// 全空
	require.Equal(t, "", firstTaskImage(relaycommon.TaskSubmitReq{}))
}

func TestMigrateTaskImagesToMedia(t *testing.T) {
	tests := []struct {
		name      string
		model     string
		image     string
		images    []string
		existing  []relaycommon.Media
		wantMedia []relaycommon.Media
	}{
		{
			name:      "i2v image reference",
			model:     "wan2.6-i2v-flash",
			image:     "https://example.com/a.png",
			wantMedia: []relaycommon.Media{{Type: "img_url", URL: "https://example.com/a.png"}},
		},
		{
			name:      "kf2v first and last frame",
			model:     "wan2.2-kf2v-flash",
			images:    []string{"first.png", "last.png"},
			wantMedia: []relaycommon.Media{{Type: "first_frame_url", URL: "first.png"}, {Type: "last_frame_url", URL: "last.png"}},
		},
		{
			name:      "wan2.7 first frame image",
			model:     "wan2.7-i2v",
			image:     "https://example.com/a.png",
			wantMedia: []relaycommon.Media{{Type: "first_frame", URL: "https://example.com/a.png"}},
		},
		{
			name:      "wan2.7 first clip video by suffix",
			model:     "wan2.7-i2v",
			image:     "https://example.com/clip.MP4",
			wantMedia: []relaycommon.Media{{Type: "first_clip", URL: "https://example.com/clip.MP4"}},
		},
		{
			name:      "wan2.7 first clip video by data uri",
			model:     "wan2.7-i2v-2026-04-25",
			image:     "data:video/mp4;base64,AAAA",
			wantMedia: []relaycommon.Media{{Type: "first_clip", URL: "data:video/mp4;base64,AAAA"}},
		},
		{
			name:      "wan2.7 image data uri stays first_frame",
			model:     "wan2.7-i2v",
			image:     "data:image/png;base64,AAAA",
			wantMedia: []relaycommon.Media{{Type: "first_frame", URL: "data:image/png;base64,AAAA"}},
		},
		{
			name:      "wan2.7 first frame plus second last frame",
			model:     "wan2.7-i2v",
			images:    []string{"a.png", "b.png"},
			wantMedia: []relaycommon.Media{{Type: "first_frame", URL: "a.png"}, {Type: "last_frame", URL: "b.png"}},
		},
		{
			name:      "happyhorse first frame only",
			model:     "happyhorse-1.1-i2v",
			image:     "a.png",
			wantMedia: []relaycommon.Media{{Type: "first_frame", URL: "a.png"}},
		},
		{
			name:      "wan3.0 first and last frame",
			model:     "wan3.0-video-prime",
			images:    []string{"a.png", "b.png"},
			wantMedia: []relaycommon.Media{{Type: "first_frame", URL: "a.png"}, {Type: "last_frame", URL: "b.png"}},
		},
		{
			name:  "non-media model untouched",
			model: "wan2.6-t2v",
			image: "a.png",
		},
		{
			name:      "existing media skips migration",
			model:     "wan2.6-i2v-flash",
			image:     "a.png",
			existing:  []relaycommon.Media{{Type: "img_url", URL: "already.png"}},
			wantMedia: []relaycommon.Media{{Type: "img_url", URL: "already.png"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := relaycommon.TaskSubmitReq{Image: tt.image, Images: tt.images, Media: tt.existing}
			migrateTaskImagesToMedia(tt.model, &req)
			require.Equal(t, tt.wantMedia, req.Media)
		})
	}
}

func TestMigrateTaskImagesToMediaClearsLegacyFields(t *testing.T) {
	req := relaycommon.TaskSubmitReq{
		Model:  "wan2.2-kf2v-flash",
		Image:  "first.png",
		Images: []string{"last.png"},
	}
	migrateTaskImagesToMedia(req.Model, &req)
	migrateTaskParamsToMetadata(&req)
	require.Equal(t, []relaycommon.Media{{Type: "first_frame_url", URL: "first.png"}, {Type: "last_frame_url", URL: "last.png"}}, req.Media)
	require.Equal(t, "", req.Image)
	require.Nil(t, req.Images)

	// 迁移后旧格式构建不再因遗留 image 字段报错
	aliReq, err := buildOldFormatRequest("wan2.2-kf2v-flash", req)
	require.NoError(t, err)
	require.Equal(t, "first.png", aliReq.Input.FirstFrameURL)
}

func TestMigrateTaskParamsToMetadata(t *testing.T) {
	// Size/Duration/Seconds 转移到 Metadata，对应字段清空
	req := relaycommon.TaskSubmitReq{Size: "1280*720", Duration: 10, Seconds: "8"}
	migrateTaskParamsToMetadata(&req)
	require.Equal(t, map[string]interface{}{"size": "1280*720", "duration": 10}, req.Metadata)
	require.Equal(t, "", req.Size)
	require.Equal(t, 0, req.Duration)
	require.Equal(t, "", req.Seconds)

	// Duration 为 0 时 Seconds 兜底 duration（字符串，toInt 可解析）
	req = relaycommon.TaskSubmitReq{Seconds: "8"}
	migrateTaskParamsToMetadata(&req)
	require.Equal(t, map[string]interface{}{"duration": "8"}, req.Metadata)

	// Metadata 已有键时不覆盖
	req = relaycommon.TaskSubmitReq{
		Size:     "1280*720",
		Duration: 10,
		Metadata: map[string]interface{}{"size": "480*480", "duration": 5},
	}
	migrateTaskParamsToMetadata(&req)
	require.Equal(t, map[string]interface{}{"size": "480*480", "duration": 5}, req.Metadata)

	// 迁移后构建不再因 size/duration 报错，且旧格式读取到迁移值
	req = relaycommon.TaskSubmitReq{Prompt: "p", Model: "wan2.5-i2v-preview", Size: "1280*720", Duration: 10}
	migrateTaskImagesToMedia(req.Model, &req)
	migrateTaskParamsToMetadata(&req)
	aliReq, err := buildOldFormatRequest("wan2.5-i2v-preview", req)
	require.NoError(t, err)
	require.Equal(t, "1280*720", aliReq.Parameters.Size)
	require.Equal(t, 10, aliReq.Parameters.Duration)
}
