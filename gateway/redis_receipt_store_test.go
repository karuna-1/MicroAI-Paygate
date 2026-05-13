package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestRedisReceiptStore_StoreGetAndTTL(t *testing.T) {
	ctx := context.Background()
	redisServer := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() {
		_ = rdb.Close()
	})

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

	redisServer.FastForward(200 * time.Millisecond)
	_, exists, err = store.Get(ctx, receipt.Receipt.ID)
	if err != nil {
		t.Fatalf("get expired receipt: %v", err)
	}
	if exists {
		t.Fatal("expired receipt should not exist")
	}
}

func TestNewRedisReceiptStore_NilClient(t *testing.T) {
	store, err := NewRedisReceiptStore(nil)
	if err == nil {
		t.Fatal("expected error for nil redis client")
	}
	if store != nil {
		t.Fatal("expected nil store when redis client is nil")
	}
}

func TestRedisReceiptStore_RejectsNonPositiveTTL(t *testing.T) {
	ctx := context.Background()
	redisServer := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() {
		_ = rdb.Close()
	})

	store, err := NewRedisReceiptStore(rdb)
	if err != nil {
		t.Fatalf("new redis receipt store: %v", err)
	}

	tests := []struct {
		name string
		ttl  time.Duration
		id   string
	}{
		{name: "zero", ttl: 0, id: "rcpt_redis_ttl_zero"},
		{name: "negative", ttl: -1 * time.Second, id: "rcpt_redis_ttl_negative"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			receipt := validTestReceipt(tt.id)
			if err := store.Store(ctx, receipt, tt.ttl); err == nil {
				t.Fatalf("expected non-positive TTL %v to be rejected", tt.ttl)
			}

			exists, err := rdb.Exists(ctx, redisReceiptKey(receipt.Receipt.ID)).Result()
			if err != nil {
				t.Fatalf("check redis key existence: %v", err)
			}
			if exists != 0 {
				t.Fatalf("receipt with non-positive TTL %v should not be stored", tt.ttl)
			}
		})
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
