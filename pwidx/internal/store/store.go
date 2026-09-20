// Package store 用 SQLite 持久化作业、冻结候选、峰排除覆盖与锁定标记。
// 候选 JSON 连同规则与算法版本一起冻结；重算只产生新作业，绝不覆盖旧候选。
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// Store 为 SQLite 状态存储。
type Store struct {
	db *sql.DB
	mu sync.Mutex // SQLite 单连接写串行化
}

// Open 打开/初始化数据库。
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) init() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS jobs (
			id TEXT PRIMARY KEY,
			fingerprint TEXT NOT NULL,
			config_json TEXT NOT NULL,
			status TEXT NOT NULL,
			manifest_json TEXT,
			result_json TEXT,
			issues_json TEXT,
			parent_candidate_id TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_jobs_fingerprint ON jobs(fingerprint) WHERE parent_candidate_id IS NULL`,
		`CREATE TABLE IF NOT EXISTS candidates (
			job_id TEXT NOT NULL,
			candidate_id TEXT NOT NULL,
			rank INTEGER NOT NULL,
			payload_json TEXT NOT NULL,
			locked INTEGER NOT NULL DEFAULT 0,
			note TEXT,
			created_at TEXT NOT NULL,
			PRIMARY KEY(job_id, candidate_id)
		)`,
		`CREATE TABLE IF NOT EXISTS peak_overrides (
			job_id TEXT NOT NULL,
			peak_index INTEGER NOT NULL,
			excluded INTEGER NOT NULL,
			PRIMARY KEY(job_id, peak_index)
		)`,
		`CREATE TABLE IF NOT EXISTS compare_sets (
			id TEXT PRIMARY KEY,
			label TEXT NOT NULL,
			member_candidate_ids TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
	}
	for _, q := range stmts {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("schema: %w", err)
		}
	}
	return nil
}

// JobStatus 为作业状态枚举。
const (
	StatusRunning   = "running"
	StatusCompleted = "completed"
	StatusError     = "error"
	StatusAborted   = "aborted"
)

// JobRecord 为作业持久化记录。
type JobRecord struct {
	ID                string          `json:"id"`
	Fingerprint       string          `json:"fingerprint"`
	Config            json.RawMessage `json:"config"`
	Status            string          `json:"status"`
	Manifest          json.RawMessage `json:"manifest,omitempty"`
	Result            json.RawMessage `json:"result,omitempty"`
	Issues            json.RawMessage `json:"issues,omitempty"`
	ParentCandidateID string          `json:"parent_candidate_id,omitempty"`
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
}

// CreateRunningJob 幂等创建作业；若同指纹已有根作业则返回该记录。
// 返回 (record, created, error)。
func (s *Store) CreateRunningJob(ctx context.Context, id, fp string, config any, parentCandidate string) (JobRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, err := s.jobByFingerprint(ctx, fp, parentCandidate); err == nil {
		return existing, false, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return JobRecord{}, false, err
	}
	cb, err := json.Marshal(config)
	if err != nil {
		return JobRecord{}, false, err
	}
	now := time.Now().UTC()
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO jobs(id,fingerprint,config_json,status,parent_candidate_id,created_at,updated_at)
		 VALUES(?,?,?,?,?,?,?)`,
		id, fp, string(cb), StatusRunning, nullStr(parentCandidate), now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	if err != nil {
		return JobRecord{}, false, err
	}
	return JobRecord{ID: id, Fingerprint: fp, Config: cb, Status: StatusRunning,
		CreatedAt: now, UpdatedAt: now}, true, nil
}

func nullStr(x string) any {
	if x == "" {
		return nil
	}
	return x
}

func (s *Store) jobByFingerprint(ctx context.Context, fp, parent string) (JobRecord, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id,fingerprint,config_json,status,COALESCE(manifest_json,''),COALESCE(result_json,''),COALESCE(issues_json,''),COALESCE(parent_candidate_id,''),created_at,updated_at
		 FROM jobs WHERE fingerprint=? AND COALESCE(parent_candidate_id,'')=?`, fp, parent)
	return scanJob(row)
}

// GetJob 读取作业。
func (s *Store) GetJob(ctx context.Context, id string) (JobRecord, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id,fingerprint,config_json,status,COALESCE(manifest_json,''),COALESCE(result_json,''),COALESCE(issues_json,''),COALESCE(parent_candidate_id,''),created_at,updated_at
		 FROM jobs WHERE id=?`, id)
	return scanJob(row)
}

// FindByFingerprint 仅查找根作业（派生作业用 GetJob）。
func (s *Store) FindByFingerprint(ctx context.Context, fp string) (JobRecord, error) {
	return s.jobByFingerprint(ctx, fp, "")
}

type scanner interface {
	Scan(dest ...any) error
}

func scanJob(row scanner) (JobRecord, error) {
	var r JobRecord
	var createdAt, updatedAt, parent string
	var cfg, man, res, iss string
	if err := row.Scan(&r.ID, &r.Fingerprint, &cfg, &r.Status, &man, &res, &iss, &parent, &createdAt, &updatedAt); err != nil {
		return JobRecord{}, err
	}
	r.Config = json.RawMessage(cfg)
	r.Manifest = json.RawMessage(man)
	r.Result = json.RawMessage(res)
	r.Issues = json.RawMessage(iss)
	r.ParentCandidateID = parent
	r.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	r.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	return r, nil
}

// CompleteJob 发布成功结果与候选——单一事务，避免半成品 Top-K 可见。
func (s *Store) CompleteJob(ctx context.Context, id string, result, manifest, issues any) error {
	rb, _ := json.Marshal(result)
	mb, _ := json.Marshal(manifest)
	ib, _ := json.Marshal(issues)
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx,
		`UPDATE jobs SET status=?, manifest_json=?, result_json=?, issues_json=?, updated_at=? WHERE id=?`,
		StatusCompleted, string(mb), string(rb), string(ib), now, id); err != nil {
		return err
	}
	// 候选由 result.candidates 解出，整批写入。
	var envelope struct {
		Candidates []json.RawMessage `json:"candidates"`
	}
	if err := json.Unmarshal(rb, &envelope); err != nil {
		return err
	}
	for rank, raw := range envelope.Candidates {
		var cand struct {
			ID     string `json:"id"`
			Rank   int    `json:"rank"`
			Locked bool   `json:"locked"`
		}
		if err := json.Unmarshal(raw, &cand); err != nil {
			return err
		}
		if cand.Rank == 0 {
			cand.Rank = rank + 1
		}
		lock := 0
		if cand.Locked {
			lock = 1
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT OR REPLACE INTO candidates(job_id,candidate_id,rank,payload_json,locked,created_at)
			 VALUES(?,?,?,?,?,?)`, id, cand.ID, cand.Rank, string(raw), lock, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// MarkTerminal 把作业置为 error/aborted；不写候选。
func (s *Store) MarkTerminal(ctx context.Context, id, status string, issues any) error {
	ib, _ := json.Marshal(issues)
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx,
		`UPDATE jobs SET status=?, issues_json=?, updated_at=? WHERE id=?`,
		status, string(ib), time.Now().UTC().Format(time.RFC3339Nano), id)
	return err
}

// RecoverInterrupted 在服务启动时把未完成作业标记为 aborted（恢复方式见 README）。
func (s *Store) RecoverInterrupted(ctx context.Context) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.ExecContext(ctx,
		`UPDATE jobs SET status=?, updated_at=? WHERE status=?`,
		StatusAborted, time.Now().UTC().Format(time.RFC3339Nano), StatusRunning)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// ListJobs 返回最近作业（不含大 JSON）。
func (s *Store) ListJobs(ctx context.Context, limit int) ([]JobRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,fingerprint,config_json,status,COALESCE(manifest_json,''),COALESCE(result_json,''),COALESCE(issues_json,''),COALESCE(parent_candidate_id,''),created_at,updated_at
		 FROM jobs ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []JobRecord{}
	for rows.Next() {
		r, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetLocked 设置候选锁定标记（锁定不改变冻结内容）。
func (s *Store) SetLocked(ctx context.Context, jobID, candID string, locked bool, note string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.ExecContext(ctx,
		`UPDATE candidates SET locked=?, note=? WHERE job_id=? AND candidate_id=?`,
		btoi(locked), note, jobID, candID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("候选 %s/%s 不存在", jobID, candID)
	}
	_, err = s.db.ExecContext(ctx, `UPDATE jobs SET updated_at=? WHERE id=?`,
		time.Now().UTC().Format(time.RFC3339Nano), jobID)
	return err
}

// CandidatePayload 返回冻结候选 JSON。
func (s *Store) CandidatePayload(ctx context.Context, jobID, candID string) (json.RawMessage, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT payload_json FROM candidates WHERE job_id=? AND candidate_id=?`, jobID, candID)
	var raw string
	if err := row.Scan(&raw); err != nil {
		return nil, err
	}
	return json.RawMessage(raw), nil
}

// UpsertOverride 保存峰排除覆盖（覆盖层独立，绝不修改输入峰表/config_json 的 peaks）。
func (s *Store) UpsertOverride(ctx context.Context, jobID string, peakIndex int, excluded bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO peak_overrides(job_id,peak_index,excluded) VALUES(?,?,?)
		 ON CONFLICT(job_id,peak_index) DO UPDATE SET excluded=excluded.excluded`,
		jobID, peakIndex, btoi(excluded))
	return err
}

// Overrides 读取作业的覆盖映射。
func (s *Store) Overrides(ctx context.Context, jobID string) (map[int]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT peak_index,excluded FROM peak_overrides WHERE job_id=?`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int]bool{}
	for rows.Next() {
		var pi, ex int
		if err := rows.Scan(&pi, &ex); err != nil {
			return nil, err
		}
		out[pi] = ex == 1
	}
	return out, rows.Err()
}

// SaveCompareSet 保存候选比较集合。
func (s *Store) SaveCompareSet(ctx context.Context, id, label string, ids []string) error {
	b, _ := json.Marshal(ids)
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO compare_sets(id,label,member_candidate_ids,created_at) VALUES(?,?,?,?)`,
		id, label, string(b), time.Now().UTC().Format(RFC3339))
	return err
}

// ListCompareSets 列出比较集合。
func (s *Store) ListCompareSets(ctx context.Context) ([]CompareSet, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,label,member_candidate_ids,created_at FROM compare_sets ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CompareSet{}
	for rows.Next() {
		var cs CompareSet
		var members, created string
		if err := rows.Scan(&cs.ID, &cs.Label, &members, &created); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(members), &cs.MemberCandidateIDs)
		cs.CreatedAt = created
		out = append(out, cs)
	}
	return out, rows.Err()
}

// CompareSet 为比较集合。
type CompareSet struct {
	ID                 string   `json:"id"`
	Label              string   `json:"label"`
	MemberCandidateIDs []string `json:"member_candidate_ids"`
	CreatedAt          string   `json:"created_at"`
}

// RFC3339 供 SQL 时间戳使用。
const RFC3339 = "2006-01-02T15:04:05Z07:00"

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}
