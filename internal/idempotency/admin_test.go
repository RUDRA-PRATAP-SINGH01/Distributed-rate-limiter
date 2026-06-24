package idempotency

import (
	"context"
	"testing"
)

func TestGetRecordAndDelete(t *testing.T) {
	store, _ := setupTestStore(t)
	ctx := context.Background()

	claim, err := store.Claim(ctx, "scope-adm", "key-adm", "hash-1")
	if err != nil || claim.Result != ResultClaimed {
		t.Fatal(err)
	}
	err = store.Complete(ctx, CompleteRequest{
		Scope: "scope-adm", Key: "key-adm", FenceToken: claim.FenceToken, HTTPStatus: 200,
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    []byte(`{"ok":true}`),
	})
	if err != nil {
		t.Fatal(err)
	}

	rec, err := store.GetRecord(ctx, "scope-adm", "key-adm")
	if err != nil || rec == nil {
		t.Fatalf("get record: %#v %v", rec, err)
	}
	if rec.Status != StatusCompleted || rec.HTTPStatus != 200 {
		t.Fatalf("unexpected record: %#v", rec)
	}
	if rec.BodySize != 11 {
		t.Fatalf("body size %d", rec.BodySize)
	}

	if err := store.DeleteRecord(ctx, "scope-adm", "key-adm"); err != nil {
		t.Fatal(err)
	}
	rec, err = store.GetRecord(ctx, "scope-adm", "key-adm")
	if err != nil || rec != nil {
		t.Fatalf("expected nil after delete, got %#v %v", rec, err)
	}
}
