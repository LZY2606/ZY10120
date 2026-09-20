package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestStoreLifecycle(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	cfg := map[string]any{"wavelength": 1.5406}
	rec, created, err := st.CreateRunningJob(ctx, "job-abc", "fp123", cfg, "")
	if err != nil || !created {
		t.Fatalf("create: %v created=%v", err, created)
	}
	// 幂等：同指纹再提交返回旧记录。
	_, created2, err := st.CreateRunningJob(ctx, "job-other", "fp123", cfg, "")
	if err != nil || created2 {
		t.Fatalf("幂等失败: %v created=%v", err, created2)
	}
	result := map[string]any{"candidates": []map[string]any{
		{"id": "cand-1", "rank": 1, "locked": false, "score": map[string]any{"score": 1.2}},
	}}
	if err := st.CompleteJob(ctx, rec.ID, result, map[string]any{"seed": 1}, []string{}); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetJob(ctx, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusCompleted {
		t.Fatalf("status=%s", got.Status)
	}
	raw, err := st.CandidatePayload(ctx, rec.ID, "cand-1")
	if err != nil {
		t.Fatal(err)
	}
	var cand map[string]any
	if err := json.Unmarshal(raw, &cand); err != nil {
		t.Fatal(err)
	}
	// 锁定。
	if err := st.SetLocked(ctx, rec.ID, "cand-1", true, "看好"); err != nil {
		t.Fatal(err)
	}
	// 排除覆盖独立保存。
	if err := st.UpsertOverride(ctx, rec.ID, 3, true); err != nil {
		t.Fatal(err)
	}
	ov, _ := st.Overrides(ctx, rec.ID)
	if !ov[3] {
		t.Fatal("覆盖未保存")
	}
	var rc map[string]any
	if err := json.Unmarshal(got.Config, &rc); err != nil {
		t.Fatal(err)
	}
	if rc["wavelength"] != 1.5406 {
		t.Fatal("原始配置丢失")
	}
}

func TestRecoverInterrupted(t *testing.T) {
	dir := t.TempDir()
	st, _ := Open(filepath.Join(dir, "state.db"))
	defer st.Close()
	ctx := context.Background()
	if _, _, err := st.CreateRunningJob(ctx, "j1", "f1", map[string]any{}, ""); err != nil {
		t.Fatal(err)
	}
	n, err := st.RecoverInterrupted(ctx)
	if err != nil || n != 1 {
		t.Fatalf("recover n=%d err=%v", n, err)
	}
	rec, _ := st.GetJob(ctx, "j1")
	if rec.Status != StatusAborted {
		t.Fatalf("中断作业应为 aborted, got %s", rec.Status)
	}
}
