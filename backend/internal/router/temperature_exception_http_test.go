package router_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/redis/go-redis/v9"

	"biosample-cold-custody-tracking/backend/internal/config"
	"biosample-cold-custody-tracking/backend/internal/constants"
	"biosample-cold-custody-tracking/backend/internal/model"
	"biosample-cold-custody-tracking/backend/internal/router"
	"biosample-cold-custody-tracking/backend/internal/testsupport"
)

type apiClient struct {
	t     *testing.T
	base  string
	token string
	reqID string
}

func (c *apiClient) do(method, path string, body any, expectStatus int) map[string]any {
	c.t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, _ := json.Marshal(body)
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(method, c.base+path, reader)
	if err != nil {
		c.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	payload, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != expectStatus {
		c.t.Fatalf("%s %s expected %d, got %d: %s", method, path, expectStatus, resp.StatusCode, string(payload))
	}
	var envelope struct {
		Data      map[string]any `json:"data"`
		RequestID string         `json:"requestId"`
		Error     *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if len(payload) > 0 {
		decoder := json.NewDecoder(bytes.NewReader(payload))
		if err := decoder.Decode(&envelope); err != nil {
			c.t.Fatalf("decode %s: %v: %s", path, err, string(payload))
		}
	}
	c.reqID = envelope.RequestID
	if expectStatus >= 400 {
		if envelope.Error == nil {
			c.t.Fatalf("%s %s expected error body, got: %s", method, path, string(payload))
		}
		c.t.Logf("expected error: %s", envelope.Error.Message)
	}
	return envelope.Data
}

// fakeS3 模拟 MinIO 的 BucketExists，使健康检查与启动流程通过。
type fakeS3 struct {
	mu             sync.Mutex
	existingBucket string
}

func (f *fakeS3) handler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/"+f.existingBucket) {
		w.Header().Set("X-Amz-Bucket-Region", "us-east-1")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<ListBucketResult><Name>` + f.existingBucket + `</Name></ListBucketResult>`))
		return
	}
	if r.Method == http.MethodPut {
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Method == http.MethodGet {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusBadRequest)
}

func TestTemperatureExceptionHTTPWorkflow(t *testing.T) {
	if os.Getenv("IT_DATABASE_URL") == "" {
		t.Skip("IT_DATABASE_URL not set")
	}
	gin.SetMode(gin.TestMode)

	db, dsn := testsupport.NewIntegrationDB(t)

	// 内嵌 Redis
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer mr.Close()

	// 内嵌 S3：让 bucket 视为不存在，启动时会自动创建
	s3 := &fakeS3{existingBucket: "protocol-documents"}
	s3Server := httptest.NewServer(http.HandlerFunc(s3.handler))
	defer s3Server.Close()
	u, _ := url.Parse(s3Server.URL)

	minioClient, err := minio.New(u.Host, &minio.Options{
		Creds:  credentials.NewStaticV4("test", "testsecret", ""),
		Secure: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	redisClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer redisClient.Close()

	cfg := config.Config{
		Port:           "19505",
		DatabaseURL:    dsn,
		JWTSecret:      "integration-test-secret-32bytes",
		TokenTTL:       time.Hour,
		RateLimit:      100000,
		RateWindow:     time.Minute,
		MinIOEndpoint:  u.Host,
		MinIOAccessKey: "test",
		MinIOSecretKey: "testsecret",
		MinIOBucket:    "protocol-documents",
		RedisAddr:      mr.Addr(),
	}
	engine, err := router.Build(db, redisClient, minioClient, cfg)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(engine)
	defer server.Close()

	// 登录保管员
	login := func(username, password string) string {
		body, _ := json.Marshal(map[string]string{"username": username, "password": password})
		resp, err := http.Post(server.URL+"/api/auth/login", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			payload, _ := io.ReadAll(resp.Body)
			t.Fatalf("login %s: %d %s", username, resp.StatusCode, string(payload))
		}
		var envelope struct {
			Data struct {
				Token string `json:"token"`
			} `json:"data"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
			t.Fatal(err)
		}
		return envelope.Data.Token
	}
	custodian := &apiClient{t: t, base: server.URL, token: login("custodian", "custody123")}
	reviewer := &apiClient{t: t, base: server.URL, token: login("reviewer", "review123")}
	receiver := &apiClient{t: t, base: server.URL, token: login("receiver", "receive123")}
	auditor := &apiClient{t: t, base: server.URL, token: login("auditor", "audit123")}

	// 接收员无权登记温度异常
	receiver.do(http.MethodPost, "/api/temperature-exceptions", map[string]any{}, http.StatusForbidden)

	// 保管员创建两个同温区容器
	createContainer := func(code string, capacity int, occupied int) float64 {
		data := custodian.do(http.MethodPost, "/api/storage-containers", map[string]any{
			"code": code, "name": "容器 " + code, "containerType": "freezer",
			"temperatureZone": "minus80", "location": "测试区", "capacity": capacity,
			"status": "available",
		}, http.StatusCreated)
		id, _ := data["id"].(float64)
		if occupied > 0 {
			db.Exec("UPDATE storage_containers SET occupied = ? WHERE id = ?", occupied, int(id))
		}
		return id
	}
	sourceID := createContainer("HTTP-SRC-1", 10, 1)
	targetID := createContainer("HTTP-DST-1", 10, 0)

	// 放一支已冻存样本进源柜
	specimen := &model.Specimen{
		AccessionNo: "HTTP-SP-001", SampleType: "血浆", SubjectCode: "HTTP-SUB-1", ProtocolCode: "HTTP-PROTO-1",
		State: constants.SpecimenStateStored, StorageContainerID: idPtr(uint(sourceID)), Position: "A-1",
		VolumeML: 2, CurrentCustodian: "冻存保管员", ReceivedAt: time.Now().Add(-time.Hour),
	}
	if err := db.Create(specimen).Error; err != nil {
		t.Fatal(err)
	}

	// 起始温度在温区内 -> 拒绝
	custodian.do(http.MethodPost, "/api/temperature-exceptions", map[string]any{
		"exceptionNo": "HTTP-EX-BAD", "containerId": sourceID, "startTemperatureC": -78,
		"alarmReason": "温度其实正常", "action": "onsite",
		"items": []map[string]any{{"specimenId": specimen.ID}},
	}, http.StatusBadRequest)

	// 转柜时目标格位不合适（目标柜容量不足模拟）: 先把目标柜塞满
	db.Exec("UPDATE storage_containers SET occupied = capacity WHERE id = ?", int(targetID))
	custodian.do(http.MethodPost, "/api/temperature-exceptions", map[string]any{
		"exceptionNo": "HTTP-EX-FULL", "containerId": sourceID, "startTemperatureC": -55,
		"alarmReason": "目标柜已满的整单回滚验证", "action": "relocation",
		"items": []map[string]any{{"specimenId": specimen.ID, "targetContainerId": targetID, "targetPosition": "Z-9"}},
	}, http.StatusConflict)
	db.Exec("UPDATE storage_containers SET occupied = 0 WHERE id = ?", int(targetID))

	// 源柜状态未被失败单改变
	var source model.StorageContainer
	db.First(&source, uint(sourceID))
	if source.Status != "available" {
		t.Fatalf("source status = %s, want available after failed orders", source.Status)
	}

	// 正常登记 onsite 处置单
	opened := custodian.do(http.MethodPost, "/api/temperature-exceptions", map[string]any{
		"exceptionNo": "HTTP-EX-001", "containerId": sourceID, "startTemperatureC": -54.5,
		"alarmReason": "柜门密封老化导致温度回升", "action": "onsite",
		"items": []map[string]any{{"specimenId": specimen.ID}},
	}, http.StatusCreated)
	exceptionID := uint(opened["id"].(float64))
	if opened["state"] != "open" {
		t.Fatalf("state = %v", opened["state"])
	}
	db.First(&source, uint(sourceID))
	if source.Status != "alarm" {
		t.Fatalf("source status = %s, want alarm", source.Status)
	}

	// 未结案: 发起交接被拒（样本在 alarm 容器）
	custodian.do(http.MethodPost, "/api/custody-transfers", map[string]any{
		"specimenId": specimen.ID, "transferNo": "HTTP-CT-001",
		"fromCustodian": "冻存保管员", "toCustodian": "另一名保管员",
		"fromLocation": "测试区 / HTTP-SRC-1 / A-1", "toLocation": "测试区",
	}, http.StatusConflict)

	// 未结案: 协议批准放行被拒（前端复核员身份）
	reviewer.do(http.MethodPost, "/api/protocol-reviews", map[string]any{
		"specimenId": specimen.ID, "protocolCode": "HTTP-PROTO-1", "decision": "approved",
		"consentVerified": true, "scopeVerified": true, "notes": "尝试放行应被拦截",
	}, http.StatusConflict)

	// 但 hold 复核仍允许（不触发放行）
	reviewer.do(http.MethodPost, "/api/protocol-reviews", map[string]any{
		"specimenId": specimen.ID, "protocolCode": "HTTP-PROTO-1", "decision": "hold",
		"consentVerified": true, "scopeVerified": true, "notes": "异常期间先暂缓复核结论",
	}, http.StatusCreated)

	// 容器不能手动恢复为 available
	custodian.do(http.MethodPatch, fmt.Sprintf("/api/storage-containers/%d", int(sourceID)), map[string]any{
		"status": "available",
	}, http.StatusConflict)

	// 结束温度仍超温区 -> 结案被拒
	custodian.do(http.MethodPost, fmt.Sprintf("/api/temperature-exceptions/%d/close", exceptionID), map[string]any{
		"endTemperatureC": -50, "outcome": "recovered", "conclusionNotes": "还没恢复不能结案",
	}, http.StatusInternalServerError)

	// 正常结案
	closed := custodian.do(http.MethodPost, fmt.Sprintf("/api/temperature-exceptions/%d/close", exceptionID), map[string]any{
		"endTemperatureC": -79.2, "outcome": "recovered", "conclusionNotes": "温度回到 -80°C 区间，样本评估可继续使用",
	}, http.StatusOK)
	if closed["state"] != "closed" {
		t.Fatalf("closed state = %v", closed["state"])
	}

	// 结案后样本页仍保留异常时段
	listData := custodian.do(http.MethodGet, fmt.Sprintf("/api/specimens?storageContainerId=%d", int(sourceID)), nil, http.StatusOK)
	items := listData["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("expected 1 specimen, got %d", len(items))
	}
	first := items[0].(map[string]any)
	tempItems := first["temperatureItems"].([]any)
	if len(tempItems) != 1 {
		t.Fatalf("specimen must retain exception period, got %d items", len(tempItems))
	}
	entry := tempItems[0].(map[string]any)
	if exceptionObj, ok := entry["exception"].(map[string]any); !ok || exceptionObj["state"] != "closed" {
		t.Fatalf("exception period not retained correctly: %#v", entry)
	}

	// 审计链包含处置单事件且哈希链完整
	auditData := auditor.do(http.MethodGet, "/api/audit-logs?pageSize=50", nil, http.StatusOK)
	auditItems := auditData["items"].([]any)
	var sawOpen, sawClose bool
	for _, raw := range auditItems {
		row := raw.(map[string]any)
		if row["entityType"] == "TemperatureException" {
			if row["action"] == "temperature_exception.opened" {
				sawOpen = true
			}
			if row["action"] == "temperature_exception.closed" {
				sawClose = true
			}
		}
	}
	if !sawOpen || !sawClose {
		t.Fatalf("audit must contain open/close events: open=%v close=%v", sawOpen, sawClose)
	}
}

func idPtr(id uint) *uint { return &id }
