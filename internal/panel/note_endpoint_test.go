package panel

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/linguo2625469/workbuddy2api-panel/internal/auth"
	"github.com/linguo2625469/workbuddy2api-panel/internal/pool"
)

// TestAccountNoteEndpoint 备注端点（面板「详情」编辑）端到端：写入 → overview 透出 →
// 清空。备注是服务端持久化的展示层信息（state.json note 字段），跨设备不丢。
func TestAccountNoteEndpoint(t *testing.T) {
	pl := pool.New("")
	pl.Add(&auth.Auth{UID: "u1", Nickname: "a@b.com"})
	p := New(Config{Version: "test", APIKey: "test-key", Pool: pl})
	authz := map[string]string{"Authorization": "Bearer test-key"}

	// 写入备注
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/panel/api/accounts/u1/note", strings.NewReader(`{"note":"主力号"}`))
	for k, v := range authz {
		req.Header.Set(k, v)
	}
	p.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("POST note: code=%d body=%s", rec.Code, rec.Body)
	}
	st, _ := pl.Status("u1")
	if st.Note != "主力号" {
		t.Fatalf("note 未写入: %q", st.Note)
	}

	// overview 必须透出 note（前端据此显示，无需额外请求）
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/panel/api/overview", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	p.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), `"note":"主力号"`) {
		t.Fatalf("overview 未透出 note: %s", rec.Body)
	}

	// 不存在账号 → 404
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/panel/api/accounts/ghost/note", strings.NewReader(`{"note":"x"}`))
	req.Header.Set("Authorization", "Bearer test-key")
	p.ServeHTTP(rec, req)
	if rec.Code != 404 {
		t.Fatalf("ghost note: code=%d want 404", rec.Code)
	}

	// 清空备注
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/panel/api/accounts/u1/note", strings.NewReader(`{"note":""}`))
	req.Header.Set("Authorization", "Bearer test-key")
	p.ServeHTTP(rec, req)
	st, _ = pl.Status("u1")
	if st.Note != "" {
		t.Fatalf("清空后 note=%q", st.Note)
	}

	// 超长截断（>200 字符）
	long := strings.Repeat("甲", 250)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/panel/api/accounts/u1/note", strings.NewReader(`{"note":"`+long+`"}`))
	req.Header.Set("Authorization", "Bearer test-key")
	p.ServeHTTP(rec, req)
	st, _ = pl.Status("u1")
	if n := len([]rune(st.Note)); n != 200 {
		t.Fatalf("超长备注未截断到 200: got %d", n)
	}
}
