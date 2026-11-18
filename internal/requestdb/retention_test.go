package requestdb

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mag1cFall/Gemini-Web2API/internal/logging"
)

// TestLedgerRetention 验证账本保留期：请求记录 90 天，Responses 30 天（OpenAI 保存 Response 对象的期限），生成文件 48 小时（Gemini API 生成视频的保存期），上传文件按到期时间删除，文件总量超过 4 GiB 时从最早的文件起删除
func TestLedgerRetention(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "requests.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	now := time.Now()
	day := 24 * time.Hour
	for id, started := range map[string]time.Time{"request-old": now.Add(-92 * day), "request-kept": now.Add(-88 * day)} {
		store.Record(logging.RequestInfo{ID: id, StartedAt: started, State: logging.StateCompleted, Status: 200, Model: "gemini-4-hydrogen"}, nil)
	}
	store.flush()
	for id, created := range map[string]time.Time{"resp_old": now.Add(-31 * day), "resp_kept": now.Add(-29 * day)} {
		if err := store.SaveResponse(ctx, StoredResponse{ID: id, CreatedAt: created, Status: "completed", Response: json.RawMessage(`{}`)}); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []StoredFile{
		{ID: "generated-old", CreatedAt: now.Add(-49 * time.Hour), Source: "generated", Purpose: "video"},
		{ID: "generated-kept", CreatedAt: now.Add(-47 * time.Hour), Source: "generated", Purpose: "video"},
		{ID: "upload-expired", CreatedAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(-time.Hour), Source: "uploaded", Purpose: "user_data"},
		{ID: "upload-oldest", CreatedAt: now.Add(-30 * day), Source: "uploaded", Purpose: "user_data"},
		{ID: "upload-middle", CreatedAt: now.Add(-20 * day), Source: "uploaded", Purpose: "user_data"},
		{ID: "upload-newest", CreatedAt: now.Add(-10 * day), Source: "uploaded", Purpose: "user_data"},
	} {
		if _, err := store.CreateFile(ctx, file, strings.NewReader(file.ID)); err != nil {
			t.Fatal(err)
		}
	}
	for id, size := range map[string]int64{"upload-oldest": 2 << 30, "upload-middle": 2 << 30, "upload-newest": 1 << 30} {
		if _, err := store.db.Exec(`UPDATE files SET bytes=? WHERE id=?`, size, id); err != nil {
			t.Fatal(err)
		}
	}
	store.prune()
	for id, kept := range map[string]bool{"request-old": false, "request-kept": true} {
		var count int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM requests WHERE id=?`, id).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if (count == 1) != kept {
			t.Errorf("请求记录 %s 存在 %d 条，应保留 %v", id, count, kept)
		}
	}
	for id, kept := range map[string]bool{"resp_old": false, "resp_kept": true} {
		if _, err := store.Response(ctx, id); errors.Is(err, ErrNotFound) == kept {
			t.Errorf("Response %s: err=%v，应保留 %v", id, err, kept)
		}
	}
	for id, kept := range map[string]bool{"generated-old": false, "generated-kept": true, "upload-expired": false, "upload-oldest": false, "upload-middle": true, "upload-newest": true} {
		if _, err := store.File(ctx, id); errors.Is(err, ErrNotFound) == kept {
			t.Errorf("文件 %s: err=%v，应保留 %v", id, err, kept)
		}
	}
}
