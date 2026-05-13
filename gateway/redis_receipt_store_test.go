package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestRedisReceiptStore_StoreGetAndTTL(t *testing.T) {
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:6379"})
	t.Cleanup(func() {
		_ = rdb.Close()
	})
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Skipf("Redis unavailable, skipping integration test: %v", err)
	}

	store, err := NewRedisReceiptStore(rdb)
	if err != nil {
		t.Fatalf("new redis receipt store: %v", err)
	}

	receipt := validTestReceipt("rcpt_redis123456")
	key := "receipt:" + receipt.Receipt.ID
	t.Cleanup(func() {
		_ = rdb.Del(ctx, key).Err()
	})

	if err := store.Store(ctx, receipt, 150*time.Millisecond); err != nil {
		t.Fatalf("store receipt: %v", err)
	}

	raw, err := rdb.Get(ctx, key).Result()
	if err != nil {
		t.Fatalf("expected receipt at exact key %q: %v", key, err)
	}
	var stored SignedReceipt
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		t.Fatalf("stored receipt is not valid JSON: %v", err)
	}
	if stored.Receipt.ID != receipt.Receipt.ID {
		t.Fatalf("stored receipt ID mismatch: got %q, want %q", stored.Receipt.ID, receipt.Receipt.ID)
	}

	ttl, err := rdb.PTTL(ctx, key).Result()
	if err != nil {
		t.Fatalf("read receipt TTL: %v", err)
	}
	if ttl <= 0 || ttl > 150*time.Millisecond {
		t.Fatalf("unexpected receipt TTL: got %v, want <=150ms and >0", ttl)
	}

	got, exists, err := store.Get(ctx, receipt.Receipt.ID)
	if err != nil {
		t.Fatalf("get receipt: %v", err)
	}
	if !exists {
		t.Fatal("receipt not found after storing")
	}
	if got.Signature != receipt.Signature {
		t.Fatalf("signature mismatch: got %q, want %q", got.Signature, receipt.Signature)
	}

	time.Sleep(200 * time.Millisecond)
	_, exists, err = store.Get(ctx, receipt.Receipt.ID)
	if err != nil {
		t.Fatalf("get expired receipt: %v", err)
	}
	if exists {
		t.Fatal("expired receipt should not exist")
	}
}

func validTestReceipt(id string) *SignedReceipt {
	return &SignedReceipt{
		Receipt: Receipt{
			ID:        id,
			Version:   "1.0",
			Timestamp: time.Now().UTC(),
			Payment: PaymentDetails{
				Payer:     "0x742d35Cc6634C0532925a3b844Bc9e7595f8fE21",
				Recipient: "0x2cAF48b4BA1C58721a85dFADa5aC01C2DFa62219",
				Amount:    "0.001",
				Token:     "USDC",
				ChainID:   8453,
				Nonce:     "test-nonce",
			},
			Service: ServiceDetails{
				Endpoint:     "/api/ai/summarize",
				RequestHash:  "sha256:test",
				ResponseHash: "sha256:response",
			},
		},
		Signature:       "0x1234567890abcdef",
		ServerPublicKey: "0xabcdef1234567890",
	}
}
