package sessions_test

import (
	"testing"

	testdb "github.com/scotmcc/cairo2/internal/store/testing"
)

func TestPageForSession(t *testing.T) {
	db := testdb.OpenTestDB(t)

	sess, err := db.Sessions.Create("test", "/tmp", "thinking_partner")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Insert 5 messages in order; track their IDs.
	var ids []int64
	contents := []string{"msg1", "msg2", "msg3", "msg4", "msg5"}
	for _, c := range contents {
		m, err := db.Messages.Add(sess.ID, "user", c, "", "", "")
		if err != nil {
			t.Fatalf("Add: %v", err)
		}
		ids = append(ids, m.ID)
	}

	// before=0: newest page, limit=3 → expect ids[4], ids[3], ids[2].
	page1, err := db.Messages.PageForSession(sess.ID, 3, 0)
	if err != nil {
		t.Fatalf("PageForSession(before=0): %v", err)
	}
	if len(page1) != 3 {
		t.Fatalf("page1: want 3 messages, got %d", len(page1))
	}
	if page1[0].ID != ids[4] || page1[1].ID != ids[3] || page1[2].ID != ids[2] {
		t.Errorf("page1 ids: got %v %v %v, want %v %v %v",
			page1[0].ID, page1[1].ID, page1[2].ID, ids[4], ids[3], ids[2])
	}

	// before=ids[2]: next page → expect ids[1], ids[0] (only 2 remain).
	page2, err := db.Messages.PageForSession(sess.ID, 3, ids[2])
	if err != nil {
		t.Fatalf("PageForSession(before=ids[2]): %v", err)
	}
	if len(page2) != 2 {
		t.Fatalf("page2: want 2 messages, got %d", len(page2))
	}
	if page2[0].ID != ids[1] || page2[1].ID != ids[0] {
		t.Errorf("page2 ids: got %v %v, want %v %v",
			page2[0].ID, page2[1].ID, ids[1], ids[0])
	}

	// limit is honored: before=0, limit=2 → exactly 2 messages.
	limited, err := db.Messages.PageForSession(sess.ID, 2, 0)
	if err != nil {
		t.Fatalf("PageForSession(limit=2): %v", err)
	}
	if len(limited) != 2 {
		t.Fatalf("limited: want 2 messages, got %d", len(limited))
	}
}
