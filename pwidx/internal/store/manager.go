package store

import (
	"context"

	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"pwidx/internal/diffract"
	"sync"

	"pwidx/internal/solver"
)

// Manager 负责异步执行求解作业并跟踪取消。
type Manager struct {
	store *Store
	mu    sync.Mutex
	jobs  map[string]context.CancelFunc
}

// NewManager 创建管理器。
func NewManager(s *Store) *Manager {
	return &Manager{store: s, jobs: map[string]context.CancelFunc{}}
}

// Submit 以输入指纹幂等提交作业。返回 (jobID, created)。
// 根作业：覆盖层不参与指纹（空覆盖时与原输入等价，命中原作业）；
// 派生作业（parentCandidate 非空）：覆盖层参与指纹，不同排除集合得到不同作业。
func (m *Manager) Submit(parent context.Context, cfg solver.Config, parentCandidate string) (string, bool, error) {
	cfg = solver.WithDefaultsExported(cfg)
	fp := solver.Fingerprint(cfg)
	if parentCandidate != "" && len(cfg.PeakOverrides) > 0 {
		fp = solver.FingerprintWithOverrides(cfg)
	}
	id := "job-" + fp[:16]
	if parentCandidate != "" {
		h := sha256.Sum256([]byte(fp + "|" + parentCandidate))
		id = "job-" + hex.EncodeToString(h[:16])
	}
	rec, created, err := m.store.CreateRunningJob(parent, id, fp, cfg, parentCandidate)
	if err != nil {
		return "", false, err
	}
	if !created {
		return rec.ID, false, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	m.jobs[id] = cancel
	m.mu.Unlock()
	go m.run(ctx, id, cfg)
	return id, true, nil
}

func (m *Manager) run(ctx context.Context, id string, cfg solver.Config) {
	defer func() {
		m.mu.Lock()
		delete(m.jobs, id)
		m.mu.Unlock()
	}()
	res := solver.Solve(ctx, cfg)
	switch res.Status {
	case "ok":
		if err := m.store.CompleteJob(ctx, id, res, res.Manifest, res.Issues); err != nil {
			_ = m.store.MarkTerminal(context.Background(), id, StatusError, map[string]string{"error": err.Error()})
		}
	case "aborted":
		_ = m.store.MarkTerminal(context.Background(), id, StatusAborted, res.Issues)
	default:
		_ = m.store.MarkTerminal(context.Background(), id, StatusError, res.Issues)
	}
}

// Cancel 请求中止作业；中止后不发布 Top-K。
func (m *Manager) Cancel(id string) bool {
	m.mu.Lock()
	cancel, ok := m.jobs[id]
	m.mu.Unlock()
	if ok {
		cancel()
	}
	return ok
}

// Active 报告作业是否仍在本机运行。
func (m *Manager) Active(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.jobs[id]
	return ok
}

// Derive 从冻结候选派生新作业：沿用其几何与规则，叠加峰排除覆盖/系统限定。
func (m *Manager) SubmitDerived(parent context.Context, baseJobID, candID string, overrides map[int]bool, systems []string) (string, bool, error) {
	raw, err := m.store.CandidatePayload(parent, baseJobID, candID)
	if err != nil {
		return "", false, err
	}
	var cand solver.Candidate
	if err := json.Unmarshal(raw, &cand); err != nil {
		return "", false, err
	}
	rec, err := m.store.GetJob(parent, baseJobID)
	if err != nil {
		return "", false, err
	}
	var base solver.Config
	if err := json.Unmarshal(rec.Config, &base); err != nil {
		return "", false, err
	}
	cfg := base
	cfg.PeakOverrides = map[int]bool{}
	// 先继承该作业已有覆盖。
	existing, _ := m.store.Overrides(parent, baseJobID)
	for k, v := range existing {
		cfg.PeakOverrides[k] = v
	}
	for k, v := range overrides {
		cfg.PeakOverrides[k] = v
		_ = m.store.UpsertOverride(context.Background(), baseJobID, k, v)
	}
	if len(systems) > 0 {
		cfg.Systems = nil
		for _, s := range systems {
			cfg.Systems = append(cfg.Systems, parseSystem(s))
		}
	}
	// 派生以候选几何为强先验：提高随机尝试围绕该几何——这里通过复用规则/范围实现，
	// 候选本身锁定旧版本不变；新作业产生新的候选集合。
	cfg.Seed = cfg.Seed + 1
	return m.Submit(parent, cfg, cand.ID)
}

func parseSystem(s string) diffract.System {
	switch s {
	case "cubic":
		return diffract.Cubic
	case "tetragonal":
		return diffract.Tetragonal
	case "hexagonal":
		return diffract.Hexagonal
	case "orthorhombic":
		return diffract.OrthorhombicSystem
	case "rhombohedral":
		return diffract.Rhombohedral
	case "monoclinic":
		return diffract.Monoclinic
	default:
		return diffract.Triclinic
	}
}
