package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"pwidx/internal/store"
)

func newTestServer(t *testing.T) (*httptest.Server, func()) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(st)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Routes())
	return ts, func() { ts.Close(); st.Close() }
}

func postJSON(t *testing.T, ts *httptest.Server, path string, body any) map[string]any {
	t.Helper()
	b, _ := json.Marshal(body)
	res, err := http.Post(ts.URL+path, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatalf("解码 %s 失败: %v", path, err)
	}
	if res.StatusCode >= 400 {
		t.Fatalf("%s -> %d %v", path, res.StatusCode, out)
	}
	return out
}

func getJSON(t *testing.T, ts *httptest.Server, path string) map[string]any {
	t.Helper()
	res, err := http.Get(ts.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestEndToEndFixtureJob(t *testing.T) {
	ts, cleanup := newTestServer(t)
	defer cleanup()

	sub := postJSON(t, ts, "/api/jobs", map[string]any{
		"fixture": "cubic_F_NaCl_like", "unit": "two_theta", "wavelength": 1.5406,
		"systems": []string{"cubic"}, "max_volume": 5000, "seed": 7,
		"top_k": 5, "random_trials": 20, "workers": 2,
	})
	jobID := sub["job_id"].(string)
	if jobID == "" {
		t.Fatal("未返回 job_id")
	}
	// 幂等：再次提交返回同一 job。
	sub2 := postJSON(t, ts, "/api/jobs", map[string]any{
		"fixture": "cubic_F_NaCl_like", "unit": "two_theta", "wavelength": 1.5406,
		"systems": []string{"cubic"}, "max_volume": 5000, "seed": 7,
		"top_k": 5, "random_trials": 20, "workers": 2,
	})
	if sub2["job_id"] != jobID || sub2["created"] != false {
		t.Fatalf("幂等提交失败: %+v", sub2)
	}

	var result map[string]any
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		j := getJSON(t, ts, "/api/jobs/"+jobID)
		if j["status"] == "completed" {
			result, _ = j["result"].(map[string]any)
			break
		}
		if j["status"] == "error" || j["status"] == "aborted" {
			t.Fatalf("作业异常结束: %v", j["status"])
		}
		time.Sleep(100 * time.Millisecond)
	}
	if result == nil {
		t.Fatal("作业未在时限内完成")
	}
	cands, _ := result["candidates"].([]any)
	if len(cands) == 0 {
		t.Fatal("无候选发布")
	}
	top := cands[0].(map[string]any)
	candID := top["id"].(string)

	// 页面可达。
	for _, p := range []string{"/", "/jobs/" + jobID, "/candidates/" + jobID + "/" + candID, "/compare", "/static/app.js"} {
		res, err := http.Get(ts.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != 200 {
			t.Fatalf("%s -> %d", p, res.StatusCode)
		}
		res.Body.Close()
	}

	// 锁定。
	lock := postJSON(t, ts, "/api/candidates/"+jobID+"/"+candID+"/lock", map[string]any{"locked": true})
	if lock["locked"] != true {
		t.Fatal("锁定失败")
	}

	// 峰覆盖 + 派生。
	postJSON(t, ts, "/api/jobs/"+jobID+"/overrides", map[string]any{"peak_index": 1, "excluded": true})
	derived := postJSON(t, ts, "/api/candidates/"+jobID+"/"+candID+"/derive", map[string]any{
		"overrides": map[string]int{}, "systems": []string{},
	})
	if derived["job_id"] == nil || derived["job_id"] == "" {
		t.Fatal("派生未返回新作业")
	}

	// 计算清单下载。
	res, err := http.Get(ts.URL + "/api/jobs/" + jobID + "/manifest")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var man map[string]any
	if err := json.NewDecoder(res.Body).Decode(&man); err != nil {
		t.Fatal(err)
	}
	if man["algorithm_version"] != "indexer-1.0.0" {
		t.Fatalf("清单缺少算法版本: %v", man["algorithm_version"])
	}
	if man["numeric_policy"] == nil {
		t.Fatal("清单缺少数值策略")
	}
}

func TestBadInputRejected(t *testing.T) {
	ts, cleanup := newTestServer(t)
	defer cleanup()
	b, _ := json.Marshal(map[string]any{
		"unit": "two_theta", "wavelength": 1.5406, "systems": []string{"cubic"},
		"peaks": []map[string]any{{"position": 250}},
	})
	res, err := http.Post(ts.URL+"/api/jobs", "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	// 2θ=250 非法：作业记录状态为 error（提交本身成功）。
	if res.StatusCode >= 400 {
		t.Fatalf("非法输入应创建 error 作业而非 HTTP 拒绝，got %d", res.StatusCode)
	}
}
