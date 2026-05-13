package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"gateway/internal/ai"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

func TestRedisReceiptStore_PersistsAcrossGatewayRestart(t *testing.T) {
	ctx := t.Context()
	redisServer := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	defer rdb.Close()

	verifier := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(VerifyResponse{
			IsValid:          true,
			RecoveredAddress: "0x742d35Cc6634C0532925a3b844Bc9e7595f8fE21",
		})
	}))
	defer verifier.Close()

	aiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"Redis receipt summary"}}]}`))
	}))
	defer aiServer.Close()

	t.Setenv("CACHE_ENABLED", "false")
	t.Setenv("RECEIPT_STORE", "redis")
	t.Setenv("REDIS_URL", redisServer.Addr())
	t.Setenv("AI_PROVIDER", "openrouter")
	t.Setenv("OPENROUTER_URL", aiServer.URL)
	t.Setenv("OPENROUTER_API_KEY", "test-key")
	t.Setenv("VERIFIER_URL", verifier.URL)
	t.Setenv("SERVER_WALLET_PRIVATE_KEY", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	t.Setenv("RECIPIENT_ADDRESS", "0x2cAF48b4BA1C58721a85dFADa5aC01C2DFa62219")
	t.Setenv("RECEIPT_TTL", "86400")

	resetServerPrivateKeyForTest(t)
	restoreReceiptGlobals := replaceReceiptGlobalsForTest(t)
	defer restoreReceiptGlobals()

	if err := initRedis(); err != nil {
		t.Fatalf("init redis: %v", err)
	}
	if err := initReceiptStore(); err != nil {
		t.Fatalf("init redis receipt store: %v", err)
	}

	var err error
	aiProvider, err = ai.NewProvider()
	if err != nil {
		t.Fatalf("new AI provider: %v", err)
	}

	firstGateway := newReceiptPersistenceTestRouter()
	createReq := httptest.NewRequest(http.MethodPost, "/api/ai/summarize", bytes.NewBufferString(`{"text":"persist this receipt"}`))
	createReq.Header.Set("Content-Type", "application/json")
	createReq.Header.Set("X-402-Signature", "0xValidSig")
	createReq.Header.Set("X-402-Nonce", "restart-test-nonce")
	createReq.Header.Set("X-402-Timestamp", strconv.FormatInt(time.Now().Unix(), 10))
	createResp := httptest.NewRecorder()
	firstGateway.ServeHTTP(createResp, createReq)

	if createResp.Code != http.StatusOK {
		t.Fatalf("create receipt status=%d body=%s", createResp.Code, createResp.Body.String())
	}

	receiptHeader := createResp.Header().Get("X-402-Receipt")
	if receiptHeader == "" {
		t.Fatal("missing X-402-Receipt header")
	}
	receiptJSON, err := base64.StdEncoding.DecodeString(receiptHeader)
	if err != nil {
		t.Fatalf("decode receipt header: %v", err)
	}
	var created SignedReceipt
	if err := json.Unmarshal(receiptJSON, &created); err != nil {
		t.Fatalf("unmarshal receipt header: %v", err)
	}
	t.Cleanup(func() {
		_ = rdb.Del(ctx, redisReceiptKey(created.Receipt.ID)).Err()
	})

	// Simulate a gateway restart by replacing the active receipt store and
	// routing the lookup through a fresh Gin engine.
	restartedStore, err := NewRedisReceiptStore(redisClient)
	if err != nil {
		t.Fatalf("new restarted receipt store: %v", err)
	}
	setActiveReceiptStore(restartedStore)

	secondGateway := newReceiptPersistenceTestRouter()
	lookupReq := httptest.NewRequest(http.MethodGet, "/api/receipts/"+created.Receipt.ID, nil)
	lookupResp := httptest.NewRecorder()
	secondGateway.ServeHTTP(lookupResp, lookupReq)

	if lookupResp.Code != http.StatusOK {
		t.Fatalf("lookup receipt status=%d body=%s", lookupResp.Code, lookupResp.Body.String())
	}
	var lookup map[string]any
	if err := json.Unmarshal(lookupResp.Body.Bytes(), &lookup); err != nil {
		t.Fatalf("unmarshal lookup response: %v", err)
	}
	receiptBody := lookup["receipt"].(map[string]any)
	if receiptBody["id"] != created.Receipt.ID {
		t.Fatalf("lookup receipt ID mismatch: got %v, want %s", receiptBody["id"], created.Receipt.ID)
	}
}

func newReceiptPersistenceTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/ai/summarize", handleSummarize)
	r.GET("/api/receipts/:id", handleGetReceipt)
	return r
}

func resetServerPrivateKeyForTest(t *testing.T) {
	t.Helper()
	origKey := serverPrivateKey
	origErr := serverPrivateKeyErr
	serverPrivateKey = nil
	serverPrivateKeyErr = nil
	serverPrivateKeyOnce = sync.Once{}
	t.Cleanup(func() {
		serverPrivateKey = origKey
		serverPrivateKeyErr = origErr
		serverPrivateKeyOnce = sync.Once{}
	})
}

func replaceReceiptGlobalsForTest(t *testing.T) func() {
	t.Helper()
	origRedisClient := redisClient
	origStore := getActiveReceiptStore()
	return func() {
		if redisClient != nil && redisClient != origRedisClient {
			_ = redisClient.Close()
		}
		redisClient = origRedisClient
		setActiveReceiptStore(origStore)
	}
}
