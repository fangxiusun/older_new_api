package newapi_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay"
	"github.com/QuantumNous/new-api/relay/channel/task/newapi"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"gorm.io/gorm"
)

const tokeaseBaseURL = "https://tokease.cn"

type taskTestTransport func(*http.Request) (*http.Response, error)

func (f taskTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// 保留渠道 URL 参与真实分派，只在 HTTP 传输层将网络流量送到本地模拟上游。
func useTaskTestUpstream(t *testing.T, upstream *httptest.Server) {
	t.Helper()
	if service.GetHttpClient() == nil {
		service.InitHttpClient()
	}
	target, err := url.Parse(upstream.URL)
	require.NoError(t, err)
	client := service.GetHttpClient()
	previous := client.Transport
	client.Transport = taskTestTransport(func(req *http.Request) (*http.Response, error) {
		forwarded := req.Clone(req.Context())
		forwarded.URL.Scheme = target.Scheme
		forwarded.URL.Host = target.Host
		return upstream.Client().Transport.RoundTrip(forwarded)
	})
	t.Cleanup(func() { client.Transport = previous })
}

func useTaskTestDatabase(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	previousDB, previousCache := model.DB, common.MemoryCacheEnabled
	previousDatabaseType := common.MainDatabaseType()
	previousFactory := service.GetTaskAdaptorFunc
	model.DB = db
	common.MemoryCacheEnabled = false
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	service.GetTaskAdaptorFunc = func(platform constant.TaskPlatform) service.TaskPollingAdaptor {
		return relay.GetTaskAdaptor(platform)
	}
	t.Cleanup(func() {
		model.DB, common.MemoryCacheEnabled = previousDB, previousCache
		common.SetMainDatabaseType(previousDatabaseType)
		service.GetTaskAdaptorFunc = previousFactory
		require.NoError(t, sqlDB.Close())
	})
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Task{}))
}

func setTaskPricing(t *testing.T, prices map[string]float64) {
	t.Helper()
	previousPrices := ratio_setting.ModelPrice2JSONString()
	previousGroups := ratio_setting.GroupRatio2JSONString()
	previousSpecialGroups := ratio_setting.GroupGroupRatio2JSONString()
	data, err := common.Marshal(prices)
	require.NoError(t, err)
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(string(data)))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1}`))
	require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(`{}`))
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(previousPrices))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(previousGroups))
		require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(previousSpecialGroups))
	})
}

func newTaskRelayContext(body, modelName, baseURL string) (*gin.Context, *httptest.ResponseRecorder, *relaycommon.RelayInfo) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/video/generations", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeNewAPI)
	common.SetContextKey(c, constant.ContextKeyChannelId, 60)
	common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, baseURL)
	common.SetContextKey(c, constant.ContextKeyChannelKey, "sk-selected")
	common.SetContextKey(c, constant.ContextKeyChannelIsMultiKey, true)
	common.SetContextKey(c, constant.ContextKeyChannelMultiKeyIndex, 1)
	common.SetContextKey(c, constant.ContextKeyOriginalModel, modelName)
	info := &relaycommon.RelayInfo{
		OriginModelName: modelName, UserGroup: "default", UsingGroup: "default", TokenGroup: "default",
		ChannelMeta:   &relaycommon.ChannelMeta{ChannelBaseUrl: baseURL},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{PublicTaskID: "task_local"},
	}
	return c, recorder, info
}

func TestH3DecoratedModelNamesKeepFullUpstreamName(t *testing.T) {
	for _, name := range []string{
		"MiniMax-H3", "provider/MiniMax-H3", "MiniMax-H3/provider",
		"provider/team/MiniMax-H3", "MiniMax-H3/provider/team",
		"provider/team/MiniMax-H3/vendor/tier", "MiniMax-H3-2026-09",
		"MiniMax-H3_custom_tier", "provider/team/MiniMax-H3-custom_v2/vendor/tier",
	} {
		t.Run(name, func(t *testing.T) {
			setTaskPricing(t, map[string]float64{name: 0.5})
			data, err := common.Marshal(map[string]any{
				"model": name, "prompt": "黄昏海岸线", "duration": 5,
				"metadata": map[string]any{"resolution": "2k", "ratio": "16:9"},
			})
			require.NoError(t, err)
			c, _, info := newTaskRelayContext(string(data), name, tokeaseBaseURL)
			a := &newapi.TaskAdaptor{}
			require.Nil(t, a.ValidateRequestAndSetAction(c, info))
			assert.Equal(t, map[string]float64{"seconds": 5, "resolution": 1.6}, a.EstimateBilling(c, info))
			body, err := a.BuildRequestBody(c, info)
			require.NoError(t, err)
			forwarded, err := io.ReadAll(body)
			require.NoError(t, err)
			assert.Equal(t, name, gjson.GetBytes(forwarded, "model").String())
			assert.Equal(t, "2K", gjson.GetBytes(forwarded, "metadata.resolution").String())
		})
	}
}

func TestH3BillingMatchesAllReferenceModes(t *testing.T) {
	setTaskPricing(t, map[string]float64{"MiniMax-H3": 0.5})
	previousQuotaUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 500000
	t.Cleanup(func() { common.QuotaPerUnit = previousQuotaUnit })
	for _, input := range []struct {
		name           string
		images, videos []string
	}{
		{name: "text"},
		{name: "image", images: []string{"https://cdn.example.com/角色.png"}},
		{name: "multiple images", images: []string{"https://cdn.example.com/face.png", "https://cdn.example.com/clothes.png"}},
		{name: "video", videos: []string{"https://cdn.example.com/input.mp4"}},
		{name: "multiple videos", videos: []string{"https://cdn.example.com/input.mp4", "https://cdn.example.com/style.mp4"}},
	} {
		for _, price := range []struct {
			resolution string
			duration   int
			wantQuota  int
		}{
			{"768P", 5, 1250000}, // 5 × 0.5 = 2.5
			{"2K", 5, 2000000},   // 5 × 0.8 = 4
			{"2K", 8, 3200000},   // 8 × 0.8 = 6.4
		} {
			t.Run(input.name+"/"+price.resolution+"/"+strconv.Itoa(price.duration), func(t *testing.T) {
				metadata := map[string]any{"resolution": price.resolution, "ratio": "16:9"}
				if len(input.videos) > 0 {
					metadata["video_urls"] = input.videos
				}
				request := map[string]any{"model": "MiniMax-H3", "prompt": "转为电影风格", "duration": price.duration, "metadata": metadata}
				if len(input.images) > 0 {
					request["images"] = input.images
				}
				data, err := common.Marshal(request)
				require.NoError(t, err)
				c, _, info := newTaskRelayContext(string(data), "MiniMax-H3", tokeaseBaseURL)
				a := &newapi.TaskAdaptor{}
				require.Nil(t, a.ValidateRequestAndSetAction(c, info))
				priceData, err := helper.ModelPriceHelperPerCall(c, info)
				require.NoError(t, err)
				assert.True(t, priceData.UsePrice)
				for key, value := range a.EstimateBilling(c, info) {
					priceData.AddOtherRatio(key, value)
				}
				quota, clamp := common.QuotaFromFloatChecked(priceData.ApplyOtherRatiosToFloat(float64(priceData.Quota)))
				assert.Nil(t, clamp)
				assert.Equal(t, price.wantQuota, quota)
				body, err := a.BuildRequestBody(c, info)
				require.NoError(t, err)
				var forwarded relaycommon.TaskSubmitReq
				require.NoError(t, common.DecodeJson(body, &forwarded))
				assert.Equal(t, price.duration, forwarded.Duration)
				assert.Equal(t, price.resolution, forwarded.Metadata["resolution"])
				assert.Equal(t, input.images, forwarded.Images)
				if len(input.videos) > 0 {
					videos, err := common.Marshal(forwarded.Metadata["video_urls"])
					require.NoError(t, err)
					var forwardedVideos []string
					require.NoError(t, common.Unmarshal(videos, &forwardedVideos))
					assert.Equal(t, input.videos, forwardedVideos)
				}
			})
		}
	}
}

func TestH3MetadataExtensionsAreForwardedUnchanged(t *testing.T) {
	setTaskPricing(t, map[string]float64{"MiniMax-H3": 0.5})
	const request = `{"model":"MiniMax-H3","prompt":"animate","duration":5,"metadata":{
		"resolution":"2k","ratio":"16:9","duration":1,"generate_audio":false,
		"seed":18446744073709551615,"strength":0.1234567890123456789,
		"offset":0,"optional":null,"custom":{"enabled":true,"values":[0,false,null,9007199254740993]}
	}}`
	c, _, info := newTaskRelayContext(request, "MiniMax-H3", tokeaseBaseURL)
	a := &newapi.TaskAdaptor{}
	require.Nil(t, a.ValidateRequestAndSetAction(c, info))
	assert.Equal(t, map[string]float64{"seconds": 5, "resolution": 1.6}, a.EstimateBilling(c, info))
	body, err := a.BuildRequestBody(c, info)
	require.NoError(t, err)
	forwarded, err := io.ReadAll(body)
	require.NoError(t, err)
	assert.Equal(t, int64(5), gjson.GetBytes(forwarded, "duration").Int())
	wantMetadata := gjson.Get(request, "metadata").Map()
	gotMetadata := gjson.GetBytes(forwarded, "metadata").Map()
	assert.Len(t, gotMetadata, len(wantMetadata))
	for key, want := range wantMetadata {
		if key == "resolution" {
			assert.Equal(t, "2K", gotMetadata[key].String())
			continue
		}
		assert.Equal(t, want.Raw, gotMetadata[key].Raw, "metadata.%s", key)
	}
}

func TestH3MediaReferencesAreMergedForForwarding(t *testing.T) {
	setTaskPricing(t, map[string]float64{"MiniMax-H3": 0.5})
	for _, test := range []struct {
		name, fields, images, videos string
	}{
		{
			name: "multiple media references",
			fields: `"metadata":{"resolution":"768P"},"media":[
				{"type":"images","url":"https://cdn.example.com/face.png"},
				{"type":"video_urls","url":"https://cdn.example.com/input.mp4"},
				{"type":"images","url":"https://cdn.example.com/style.png"},
				{"type":"video_urls","url":"https://cdn.example.com/motion.mp4"}]`,
			images: `["https://cdn.example.com/face.png","https://cdn.example.com/style.png"]`,
			videos: `["https://cdn.example.com/input.mp4","https://cdn.example.com/motion.mp4"]`,
		},
		{
			name: "existing references precede Media",
			fields: `"images":["https://cdn.example.com/face.png"],
				"metadata":{"resolution":"768P","video_urls":["https://cdn.example.com/input.mp4"],"generate_audio":false},
				"media":[{"type":"Images","url":"https://cdn.example.com/style.png"},
				{"type":"Video_Urls","url":"https://cdn.example.com/motion.mp4"}]`,
			images: `["https://cdn.example.com/face.png","https://cdn.example.com/style.png"]`,
			videos: `["https://cdn.example.com/input.mp4","https://cdn.example.com/motion.mp4"]`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := `{"model":"MiniMax-H3","prompt":"animate","duration":5,` + test.fields + `}`
			c, _, info := newTaskRelayContext(request, "MiniMax-H3", tokeaseBaseURL)
			a := &newapi.TaskAdaptor{}
			require.Nil(t, a.ValidateRequestAndSetAction(c, info))
			cached, err := relaycommon.GetTaskRequest(c)
			require.NoError(t, err)
			before, err := common.Marshal(cached)
			require.NoError(t, err)
			assert.Equal(t, map[string]float64{"seconds": 5, "resolution": 1}, a.EstimateBilling(c, info))
			body, err := a.BuildRequestBody(c, info)
			require.NoError(t, err)
			forwarded, err := io.ReadAll(body)
			require.NoError(t, err)
			assert.JSONEq(t, test.images, gjson.GetBytes(forwarded, "images").Raw)
			assert.JSONEq(t, test.videos, gjson.GetBytes(forwarded, "metadata.video_urls").Raw)
			assert.False(t, gjson.GetBytes(forwarded, "media").Exists())
			cached, err = relaycommon.GetTaskRequest(c)
			require.NoError(t, err)
			after, err := common.Marshal(cached)
			require.NoError(t, err)
			assert.Equal(t, before, after, "billing and forwarding must not mutate the cached request")

			// 重试再次验证原始请求，不能把已迁移的引用再次追加。
			require.Nil(t, a.ValidateRequestAndSetAction(c, info))
			retryBody, err := a.BuildRequestBody(c, info)
			require.NoError(t, err)
			retried, err := io.ReadAll(retryBody)
			require.NoError(t, err)
			assert.Equal(t, forwarded, retried)
			storage, err := common.GetBodyStorage(c)
			require.NoError(t, err)
			original, err := storage.Bytes()
			require.NoError(t, err)
			assert.Equal(t, request, string(original))
		})
	}
}

func TestH3RejectsUnknownContractsBeforeForwarding(t *testing.T) {
	setTaskPricing(t, map[string]float64{"MiniMax-H3": 0.5})
	var requests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer upstream.Close()
	useTaskTestUpstream(t, upstream)
	for _, name := range []string{"sora-2", "kling-v1", "MiniMax-Hailuo-2.3", "jimeng_v30", "viduq1", "doubao-seedance-2-0-260128", "wan3.0-video", "Seedance 2.0/uP", "MiniMax-H30", "not-MiniMax-H3", "mystery-video"} {
		t.Run(name, func(t *testing.T) {
			body, err := common.Marshal(map[string]any{"model": name, "prompt": "animate", "duration": 5, "metadata": map[string]any{"resolution": "768P"}})
			require.NoError(t, err)
			c, _, info := newTaskRelayContext(string(body), name, tokeaseBaseURL)
			_, taskErr := relay.RelayTaskSubmit(c, info)
			require.NotNil(t, taskErr)
			assert.Equal(t, "unsupported_task_model", taskErr.Code)
			assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
			assert.True(t, taskErr.LocalError)
			assert.Nil(t, info.Billing)
		})
	}
	for _, fields := range []string{
		`"duration":0,"metadata":{"resolution":"768P"}`,
		`"duration":-1,"metadata":{"resolution":"768P"}`,
		`"duration":3601,"metadata":{"resolution":"768P"}`,
		`"duration":18446744073709551616,"metadata":{"resolution":"768P"}`,
		`"duration":"18446744073709551616","metadata":{"resolution":"768P"}`,
		`"duration":"5","metadata":{"resolution":"768P"}`,
		`"duration":1.5,"metadata":{"resolution":"768P"}`,
		`"duration":null,"metadata":{"resolution":"768P"}`,
		`"metadata":{"resolution":"768P"}`,
		`"duration":5,"metadata":{"resolution":"720P"}`,
		`"duration":5,"metadata":{}`,
		`"duration":5,"metadata":null`,
		`"duration":5,"metadata":"{\"resolution\":\"768P\"}"`,
		`"duration":5,"metadata":{"resolution":"768P","video_urls":"https://cdn.example.com/video.mp4"}`,
		`"duration":5,"metadata":{"resolution":"768P","video_urls":null}`,
		`"duration":5,"metadata":{"resolution":"768P","video_urls":[""]}`,
		`"duration":5,"images":[" "],"metadata":{"resolution":"768P"}`,
		`"duration":5,"media":{"video_urls":["https://cdn.example.com/input.mp4"]},"metadata":{"resolution":"768P"}`,
		`"duration":5,"media":[{"type":"images","url":" "}],"metadata":{"resolution":"768P"}`,
		`"duration":5,"media":[{"type":"audio","url":"https://cdn.example.com/input.wav"}],"metadata":{"resolution":"768P"}`,
	} {
		t.Run(fields, func(t *testing.T) {
			c, _, info := newTaskRelayContext(`{"model":"MiniMax-H3","prompt":"animate",`+fields+`}`, "MiniMax-H3", tokeaseBaseURL)
			_, taskErr := relay.RelayTaskSubmit(c, info)
			require.NotNil(t, taskErr)
			assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
			assert.Equal(t, "invalid_request", taskErr.Code)
			assert.Nil(t, info.Billing)
		})
	}
	assert.Zero(t, requests.Load())
}

func TestH3RequiresSupportedChannelURL(t *testing.T) {
	setTaskPricing(t, map[string]float64{"MiniMax-H3": 0})
	var requests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer upstream.Close()
	useTaskTestUpstream(t, upstream)
	for _, test := range []struct {
		baseURL   string
		supported bool
	}{
		{tokeaseBaseURL, true},
		{tokeaseBaseURL + "/", true},
		{"https://TOKEASE.CN:443/", true},
		{"https://other.example", false},
		{"https://tokease.cn.other.example", false},
		{"https://tokease.cn@other.example", false},
		{"https://other.example/tokease.cn", false},
		{"https://tokease.cn，", false},
		{"http://tokease.cn", false},
		{"https://tokease.cn:8443", false},
		{"https://tokease.cn/v1", false},
		{"https://tokease.cn?site=other", false},
		{"https://tokease.cn#other", false},
		{"tokease.cn", false},
		{"", false},
	} {
		t.Run(test.baseURL, func(t *testing.T) {
			c, _, info := newTaskRelayContext(`{"model":"MiniMax-H3","prompt":"animate","duration":5,"metadata":{"resolution":"2K"}}`, "MiniMax-H3", test.baseURL)
			if test.supported {
				a := &newapi.TaskAdaptor{}
				a.Init(info)
				require.Nil(t, a.ValidateRequestAndSetAction(c, info))
				assert.Equal(t, map[string]float64{"seconds": 5, "resolution": 1.6}, a.EstimateBilling(c, info))
				requestURL, err := a.BuildRequestURL(info)
				require.NoError(t, err)
				assert.Equal(t, strings.TrimRight(test.baseURL, "/")+"/v1/video/generations", requestURL)
				return
			}
			_, taskErr := relay.RelayTaskSubmit(c, info)
			require.NotNil(t, taskErr)
			assert.Equal(t, "unsupported_task_upstream", taskErr.Code)
			assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
			assert.True(t, taskErr.LocalError)
			assert.Nil(t, info.Billing)
			_, err := (&newapi.TaskAdaptor{}).FetchTask(test.baseURL, "sk-selected", map[string]any{"task_id": "task_upstream"}, "")
			require.ErrorContains(t, err, "unsupported newapi task upstream")
		})
	}
	assert.Zero(t, requests.Load())
}

func TestH3MappedSubmissionAndDirectResult(t *testing.T) {
	useTaskTestDatabase(t)
	setTaskPricing(t, map[string]float64{"video-alias": 0})
	quotaSetting := operation_setting.GetQuotaSetting()
	previousPreConsume := quotaSetting.EnableFreeModelPreConsume
	quotaSetting.EnableFreeModelPreConsume = false
	t.Cleanup(func() { quotaSetting.EnableFreeModelPreConsume = previousPreConsume })
	const upstreamModel = "provider/team/MiniMax-H3_custom/tier/region"
	const videoURL = "https://h3.submodel.ai/output/result.mp4?signature=example"
	const completedBody = `{"completed_at":1788508519,"created_at":1788508343,"error":null,"id":"task_upstream","model":"MiniMax-H3","object":"video.generation","output":{"video_url":"` + videoURL + `","poster_url":"https://cdn.example.com/poster.jpg"},"progress":100,"seconds":"5","size":"768P","status":"completed","metadata":{"seed":9007199254740993},"extension":{"enabled":false,"zero":0,"nullable":null},"response":{"bytesBase64Encoded":"AA=="}}`
	var submits, fetches atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "tokease.cn", r.Host)
		assert.Equal(t, "Bearer sk-selected", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "POST /v1/video/generations":
			submits.Add(1)
			var request relaycommon.TaskSubmitReq
			if !assert.NoError(t, common.DecodeJson(r.Body, &request)) {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			assert.Equal(t, upstreamModel, request.Model)
			assert.Equal(t, 5, request.Duration)
			assert.Equal(t, "768P", request.Metadata["resolution"])
			assert.Equal(t, []string{"https://cdn.example.com/face.png"}, request.Images)
			assert.Equal(t, []any{"https://cdn.example.com/input.mp4"}, request.Metadata["video_urls"])
			assert.Empty(t, request.Media)
			_, err := io.WriteString(w, `{"id":"task_upstream","object":"video.generation","model":"MiniMax-H3","status":"queued","progress":0,"created_at":"2026-09-04T07:52:23Z","error":null}`)
			assert.NoError(t, err)
		case "GET /v1/video/generations/task_upstream":
			if fetches.Add(1) == 1 {
				_, err := io.WriteString(w, `{"created_at":1788508343,"error":null,"id":"task_upstream","model":"MiniMax-H3","object":"video.generation","progress":30,"seconds":"5","size":"768P","status":"in_progress"}`)
				assert.NoError(t, err)
				return
			}
			_, err := io.WriteString(w, completedBody)
			assert.NoError(t, err)
		default:
			assert.Fail(t, "unexpected upstream request", r.Method+" "+r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer upstream.Close()
	useTaskTestUpstream(t, upstream)
	// 重复 model 键也必须收敛为映射后的同一个上游名称。
	c, recorder, info := newTaskRelayContext(`{"model":"ignored","model":"video-alias","prompt":"黄昏海岸线","duration":5,"metadata":{"resolution":"768p","ratio":"16:9"},"media":[{"type":"images","url":"https://cdn.example.com/face.png"},{"type":"video_urls","url":"https://cdn.example.com/input.mp4"}]}`, "video-alias", tokeaseBaseURL+"/")
	info.UserId = 1
	c.Set("model_mapping", `{"video-alias":"intermediate","intermediate":"`+upstreamModel+`"}`)
	result, taskErr := relay.RelayTaskSubmit(c, info)
	require.Nil(t, taskErr)
	require.NotNil(t, result)
	assert.Equal(t, map[string]float64{"seconds": 5, "resolution": 1}, info.PriceData.OtherRatios())
	assert.Equal(t, "task_local", gjson.GetBytes(recorder.Body.Bytes(), "id").String())
	assert.Equal(t, "video-alias", gjson.GetBytes(recorder.Body.Bytes(), "model").String())
	assert.Equal(t, "2026-09-04T07:52:23Z", gjson.GetBytes(recorder.Body.Bytes(), "created_at").String())
	task := model.InitTask(result.Platform, info)
	task.PrivateData.UpstreamTaskID = result.UpstreamTaskID
	assert.Equal(t, "sk-selected", task.PrivateData.Key)
	task.Action, task.Data = info.Action, result.TaskData
	baseURL := tokeaseBaseURL
	require.NoError(t, model.DB.Create(&model.Channel{
		Id: task.ChannelId, Type: constant.ChannelTypeNewAPI, Name: "tokease", Key: "sk-selected", BaseURL: &baseURL,
	}).Error)
	require.NoError(t, task.Insert())
	for _, wantStatus := range []string{model.TaskStatusInProgress, model.TaskStatusSuccess} {
		require.NoError(t, service.UpdateVideoTasks(context.Background(), task.Platform,
			map[int][]string{task.ChannelId: {task.GetUpstreamTaskID()}},
			map[string]*model.Task{task.GetUpstreamTaskID(): task}))
		stored, exists, err := model.GetByTaskId(info.UserId, task.TaskID)
		require.NoError(t, err)
		require.True(t, exists)
		assert.Equal(t, model.TaskStatus(wantStatus), stored.Status)
		task = stored
	}
	assert.Equal(t, videoURL, task.GetResultURL())
	assert.JSONEq(t, completedBody, string(task.Data))
	assert.Equal(t, "9007199254740993", gjson.GetBytes(task.Data, "metadata.seed").Raw)
	for _, path := range []string{"/v1/videos/task_local", "/v1/video/generations/task_local"} {
		t.Run(path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodGet, path, nil)
			c.Params = gin.Params{{Key: "task_id", Value: task.TaskID}}
			c.Set("id", info.UserId)
			require.Nil(t, relay.RelayTaskFetch(c, relayconstant.RelayModeVideoFetchByID))
			assert.Equal(t, http.StatusOK, recorder.Code)
			response := gjson.ParseBytes(recorder.Body.Bytes())
			if strings.HasPrefix(path, "/v1/video/generations/") {
				assert.Equal(t, "task_local", response.Get("data.task_id").String())
				response = response.Get("data.data")
				assert.JSONEq(t, completedBody, response.Raw)
			} else {
				assert.Equal(t, "task_local", response.Get("id").String())
				assert.Equal(t, "task_local", response.Get("task_id").String())
				assert.Equal(t, "video-alias", response.Get("model").String())
				assert.Equal(t, videoURL, response.Get("metadata.url").String())
			}
			for field, expected := range map[string]string{
				"completed_at": "1788508519", "created_at": "1788508343", "error": "null",
				"object": `"video.generation"`, "status": `"completed"`, "progress": "100",
				"seconds": `"5"`, "size": `"768P"`, "output.video_url": `"` + videoURL + `"`,
				"output.poster_url": `"https://cdn.example.com/poster.jpg"`,
				"metadata.seed":     "9007199254740993", "response.bytesBase64Encoded": `"AA=="`,
				"extension.enabled": "false", "extension.zero": "0", "extension.nullable": "null",
			} {
				assert.Equal(t, expected, response.Get(field).Raw, field)
			}
		})
	}
	assert.EqualValues(t, 1, submits.Load())
	assert.EqualValues(t, 2, fetches.Load())
}

func TestH3RejectsMissingPriceAndUnsupportedMapping(t *testing.T) {
	setTaskPricing(t, map[string]float64{})
	for _, test := range []struct {
		modelName, mapping, code string
	}{
		{"MiniMax-H3/no-price", "", "invalid_billing_config"},
		{"MiniMax-H3", `{"MiniMax-H3":"sora-2"}`, "unsupported_task_model"},
		{"MiniMax-H3", `{"MiniMax-H3":"other","other":"MiniMax-H3"}`, "model_mapping_failed"},
	} {
		t.Run(test.code, func(t *testing.T) {
			body, err := common.Marshal(map[string]any{"model": test.modelName, "prompt": "animate", "duration": 5, "metadata": map[string]any{"resolution": "768P"}})
			require.NoError(t, err)
			c, _, info := newTaskRelayContext(string(body), test.modelName, tokeaseBaseURL)
			c.Set("model_mapping", test.mapping)
			_, taskErr := relay.RelayTaskSubmit(c, info)
			require.NotNil(t, taskErr)
			assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
			assert.Equal(t, test.code, taskErr.Code)
			assert.Nil(t, info.Billing)
		})
	}
}

func TestH3CompletionRequiresDirectVideoURL(t *testing.T) {
	for _, body := range []string{
		`{"status":"completed"}`,
		`{"status":"completed","output":{"video_url":"/v1/videos/upstream/content"}}`,
		`{"status":"completed","output":{"video_url":"data:video/mp4;base64,AAAA"}}`,
		`{"status":"completed","output":{"video_url":"file:///video.mp4"}}`,
	} {
		_, err := (&newapi.TaskAdaptor{}).ParseTaskResult([]byte(body))
		require.ErrorContains(t, err, "directly downloadable")
	}
}
