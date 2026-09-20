package solver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"runtime"
	"sort"
	"sync"

	"pwidx/internal/canonical"
	"pwidx/internal/diffract"
	"pwidx/internal/extinction"
)

// Config 为一次求解作业的全部输入参数（也是输入指纹的来源）。
type Config struct {
	Peaks            []diffract.Peak   `json:"peaks"`
	Unit             diffract.PeakUnit `json:"unit"`
	Wavelength       float64           `json:"wavelength"`
	Systems          []diffract.System `json:"systems"`
	MaxVolume        float64           `json:"max_volume"`
	Rules            []extinction.Rule `json:"rules"`
	Seed             int64             `json:"seed"`
	TopK             int               `json:"top_k"`
	RandomTrials     int               `json:"random_trials"`
	RefineEvals      int               `json:"refine_evals"`
	Workers          int               `json:"workers"` // 0 => GOMAXPROCS
	Weights          WeightConfig      `json:"weights"`
	DefaultSigmaT    float64           `json:"default_sigma_two_theta"`
	RelSigmaD        float64           `json:"rel_sigma_d"`
	PeakOverrides    map[int]bool      `json:"peak_overrides,omitempty"` // 排除覆盖，不写回原始峰表
	AlgorithmVersion string            `json:"algorithm_version"`
}

// Candidate 为一个冻结候选（几何 + 证据 + 排名信息）。
type Candidate struct {
	ID               string            `json:"id"`
	Cell             diffract.Cell     `json:"cell"`
	ReducedCell      diffract.Cell     `json:"reduced_cell"`
	Volume           float64           `json:"volume"`
	Score            Score             `json:"score"`
	CanonicalKey     string            `json:"canonical_key"`
	Rules            []extinction.Rule `json:"rules"`
	AlgorithmVersion string            `json:"algorithm_version"`
	Rank             int               `json:"rank"`
	Locked           bool              `json:"locked,omitempty"`
	ParentID         string            `json:"parent_id,omitempty"`
	Source           string            `json:"source"` // analytic | random | derived
}

// Result 为一次完整求解输出。
type Result struct {
	Status      string                     `json:"status"`
	Issues      []diffract.ValidationIssue `json:"issues,omitempty"`
	Candidates  []Candidate                `json:"candidates"`
	Manifest    Manifest                   `json:"manifest"`
	Fingerprint string                     `json:"fingerprint"`
	NormPeaks   []diffract.NormalizedPeak  `json:"normalized_peaks"`
	Aborted     bool                       `json:"aborted,omitempty"`
}

// Solve 执行求解；ctx 取消时不发布任何候选（Aborted=true，Candidates 为空）。
func Solve(ctx context.Context, cfg Config) Result {
	cfg = withDefaults(cfg)
	norm, issues, status := diffract.Normalize(cfg.Peaks, cfg.Unit, cfg.Wavelength, cfg.DefaultSigmaT, cfg.RelSigmaD)
	man := Manifest{
		AlgorithmVersion: diffract.AlgorithmVersion,
		Units:            "positions: degree 2theta or angstrom d; wavelength: angstrom; volume: angstrom^3",
		Weights:          cfg.Weights,
		Seed:             cfg.Seed,
		RandomTrials:     cfg.RandomTrials,
		RefineEvals:      cfg.RefineEvals,
		Workers:          cfg.Workers,
		MaxHKL:           cfg.Weights.HKLMax,
		Rules:            cfg.Rules,
		Systems:          cfg.Systems,
		MaxVolume:        cfg.MaxVolume,
		Quantization:     "canonical key relative 1e-5 per parameter; tie keys unrounded",
		NumericPolicy: []string{
			"匹配残差 z=(d_pred-d_obs)/sqrt(sigma_d^2+(0.0005*d_pred)^2)",
			"排序: score 降序; 同分依次比 coverage↑, rms↓, explained↑, ambiguous↓, missing↓, 系统自由度, 标准形参数词典序",
			"随机建议在单线程内按种子生成，之后并行打分；打分是纯函数，排名与 workers 无关",
		},
	}
	fp := Fingerprint(cfg)
	man.Fingerprint = fp
	if status == "error" {
		return Result{Status: "error", Issues: issues, Manifest: man, Fingerprint: fp, NormPeaks: norm}
	}
	// 应用排除覆盖（只影响本次/派生计算，不改原始峰表）。
	for i := range norm {
		if ex, ok := cfg.PeakOverrides[norm[i].Index]; ok {
			norm[i].Excluded = ex
		}
	}
	rf := ruleFilter{rules: cfg.Rules}
	props := []proposal{}
	addProps := func(cs []diffract.Cell, src string) {
		for _, c := range cs {
			props = append(props, proposal{c, src})
		}
	}
	sysSet := map[diffract.System]bool{}
	for _, s := range cfg.Systems {
		sysSet[s] = true
	}
	sortedPeaks := diffract.SortedByD(norm)
	if sysSet[diffract.Cubic] {
		addProps(cubicProposals(sortedPeaks, rf), "analytic")
	}
	if sysSet[diffract.Tetragonal] {
		addProps(tetragonalProposals(sortedPeaks, rf, false), "analytic")
	}
	if sysSet[diffract.Hexagonal] {
		addProps(tetragonalProposals(sortedPeaks, rf, true), "analytic")
	}
	if sysSet[diffract.OrthorhombicSystem] {
		addProps(orthorhombicProposals(sortedPeaks, rf), "analytic")
	}
	if sysSet[diffract.Rhombohedral] {
		addProps(rhombohedralProposals(sortedPeaks, rf), "analytic")
	}
	if sysSet[diffract.Monoclinic] {
		addProps(monoclinicProposals(sortedPeaks, rf), "analytic")
	}
	if sysSet[diffract.Triclinic] {
		addProps(triclinicAnalyticProposals(sortedPeaks, rf), "analytic")
	}
	addProps(randomProposals(sortedPeaks, cfg.Systems, cfg.RandomTrials, cfg.Seed), "random")
	man.Proposals = len(props)

	// 预筛：对全部原始建议做一次粗评分（单线程、确定性），仅把每晶系统前 N 个送入精修，
	// 避免把昂贵的单纯形花在明显劣化的建议上。
	type evaled struct {
		cell   diffract.Cell
		source string
		score  Score
		red    diffract.Cell
		key    string
		ok     bool
		order  int
	}
	type scored struct {
		p     proposal
		score Score
	}
	pre := make([]scored, 0, len(props))
	for _, p := range props {
		if ctx.Err() != nil {
			man.Aborted = true
			return Result{Status: "aborted", Issues: issues, Manifest: man, Fingerprint: fp, NormPeaks: norm, Aborted: true}
		}
		rc := canonical.Reduce(p.cell)
		if !validCell(rc) || !volumeOK(rc, cfg.MaxVolume) {
			continue
		}
		pre = append(pre, scored{p, Evaluate(rc, norm, cfg.Wavelength, cfg.Rules, cfg.Weights)})
	}
	sort.SliceStable(pre, func(i, j int) bool {
		if pre[i].score.Score != pre[j].score.Score {
			return pre[i].score.Score > pre[j].score.Score
		}
		return pre[i].p.cell.A < pre[j].p.cell.A
	})
	const refineQuotaPerSystem = 12
	quota := map[diffract.System]int{}
	shortlist := make([]proposal, 0)
	for _, sc := range pre {
		sys := sc.p.cell.System
		if quota[sys] < refineQuotaPerSystem {
			shortlist = append(shortlist, sc.p)
			quota[sys]++
		}
	}
	// 随机建议取固定数量作为多样性补充。
	for _, p := range props {
		if p.source == "random" && len(shortlist) < len(pre) {
			shortlist = append(shortlist, p)
		}
		if len(shortlist) >= len(pre) {
			break
		}
	}
	man.Proposals = len(props)
	man.RefineCandidates = len(shortlist)

	// 单线程确定顺序后并行精修+打分；任何 worker 看到取消都不产出。
	results := make([]evaled, len(shortlist))
	workers := cfg.Workers
	if workers <= 0 {
		workers = runtime.GOMAXPROCS(0)
	}
	if workers > len(shortlist) {
		workers = len(shortlist)
	}
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	aborted := false
	for i, p := range shortlist {
		select {
		case <-ctx.Done():
			aborted = true
		case sem <- struct{}{}:
		}
		if aborted {
			break
		}
		wg.Add(1)
		go func(i int, p proposal) {
			defer wg.Done()
			defer func() { <-sem }()
			refined := refine(p.cell, norm, cfg.Wavelength, cfg.Rules, cfg.Weights, cfg.MaxVolume, cfg.RefineEvals)
			if ctx.Err() != nil {
				return
			}
			red := canonical.Reduce(refined)
			if !validCell(red) || !volumeOK(red, cfg.MaxVolume) {
				return
			}
			sc := Evaluate(red, norm, cfg.Wavelength, cfg.Rules, cfg.Weights)
			results[i] = evaled{cell: refined, source: p.source, score: sc, red: red, key: canonical.Key(red), ok: true, order: i}
		}(i, p)
	}
	wg.Wait()
	if aborted || ctx.Err() != nil {
		man.Aborted = true
		return Result{Status: "aborted", Issues: issues, Manifest: man, Fingerprint: fp, NormPeaks: norm, Aborted: true}
	}

	// 标准形去重：同 key 保留分数最高（再比建议顺序，确保确定性）。
	best := map[string]evaled{}
	for _, r := range results {
		if !r.ok {
			continue
		}
		if b, ok := best[r.key]; !ok || rankLess(r.score, r.red, b.score, b.red) {
			best[r.key] = r
		}
	}
	all := make([]evaled, 0, len(best))
	for _, r := range best {
		all = append(all, r)
	}
	sort.SliceStable(all, func(i, j int) bool { return rankLess(all[i].score, all[i].red, all[j].score, all[j].red) })

	cands := make([]Candidate, 0, cfg.TopK)
	for rank, r := range all {
		if rank >= cfg.TopK {
			break
		}
		c := Candidate{
			ID:   candidateID(fp, r.key, rank),
			Cell: r.cell, ReducedCell: r.red, Volume: r.red.Volume(),
			Score: r.score, CanonicalKey: r.key, Rules: append([]extinction.Rule(nil), cfg.Rules...),
			AlgorithmVersion: diffract.AlgorithmVersion, Rank: rank + 1, Source: r.source,
		}
		cands = append(cands, c)
	}
	man.TopK = len(cands)
	return Result{Status: "ok", Issues: issues, Candidates: cands, Manifest: man, Fingerprint: fp, NormPeaks: norm}
}

// rankLess 定义全局确定性排名（见 Manifest.NumericPolicy）。
func rankLess(a Score, ca diffract.Cell, b Score, cb diffract.Cell) bool {
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	if a.CoverageTerm != b.CoverageTerm {
		return a.CoverageTerm > b.CoverageTerm
	}
	if a.RMS != b.RMS {
		return a.RMS < b.RMS
	}
	if a.Explained != b.Explained {
		return a.Explained > b.Explained
	}
	if a.AmbiguousCount != b.AmbiguousCount {
		return a.AmbiguousCount < b.AmbiguousCount
	}
	if a.Missing != b.Missing {
		return a.Missing < b.Missing
	}
	pa, pb := parsimony(ca), parsimony(cb)
	if pa != pb {
		return pa < pb
	}
	return tupleKey(ca) < tupleKey(cb)
}

func tupleKey(c diffract.Cell) string {
	a, b, cc, al, be, ga := c.Params()
	return fmt.Sprintf("%.9f|%.9f|%.9f|%.9f|%.9f|%.9f", a, b, cc, al, be, ga)
}

func withDefaults(cfg Config) Config {
	if cfg.TopK <= 0 {
		cfg.TopK = 10
	}
	if cfg.RandomTrials == 0 {
		cfg.RandomTrials = 200
	}
	if cfg.RefineEvals == 0 {
		cfg.RefineEvals = 50
	}
	if cfg.Workers <= 0 {
		cfg.Workers = runtime.GOMAXPROCS(0)
	}
	if len(cfg.Systems) == 0 {
		cfg.Systems = append([]diffract.System(nil), diffract.AllSystems...)
	}
	if cfg.MaxVolume <= 0 {
		cfg.MaxVolume = 1e5
	}
	if cfg.DefaultSigmaT <= 0 {
		cfg.DefaultSigmaT = 0.05
	}
	if cfg.RelSigmaD <= 0 {
		cfg.RelSigmaD = 0.002
	}
	if cfg.Weights.HKLMax == 0 {
		cfg.Weights = DefaultWeights()
	}
	if cfg.PeakOverrides == nil {
		cfg.PeakOverrides = map[int]bool{}
	}
	cfg.AlgorithmVersion = diffract.AlgorithmVersion
	return cfg
}

// Fingerprint 为输入指纹（JSON 规范化后 SHA-256），用于根作业幂等提交；
// 不含峰排除覆盖层（覆盖重算走派生指纹）与并行度。
func Fingerprint(cfg Config) string {
	c := withDefaults(cfg)
	c.PeakOverrides = nil
	c.Workers = 0
	return fingerprintJSON(c)
}

func fingerprintJSON(c Config) string {
	b, _ := json.Marshal(c)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:16])
}

func candidateID(jobFP, key string, rank int) string {
	h := sha256.Sum256([]byte(jobFP + "|" + key))
	return fmt.Sprintf("cand-%s-%d", hex.EncodeToString(h[:6]), rank)
}

// Manifest 为可导出的计算清单（排名组成与量化策略）。
type Manifest struct {
	AlgorithmVersion string            `json:"algorithm_version"`
	Fingerprint      string            `json:"fingerprint"`
	Units            string            `json:"units"`
	Weights          WeightConfig      `json:"weights"`
	Seed             int64             `json:"seed"`
	RandomTrials     int               `json:"random_trials"`
	RefineEvals      int               `json:"refine_evals"`
	Workers          int               `json:"workers"`
	MaxHKL           int               `json:"max_hkl"`
	Rules            []extinction.Rule `json:"rules"`
	Systems          []diffract.System `json:"systems"`
	MaxVolume        float64           `json:"max_volume"`
	Proposals        int               `json:"proposals_generated"`
	RefineCandidates int               `json:"proposals_refined"`
	TopK             int               `json:"topk_published"`
	Quantization     string            `json:"quantization"`
	NumericPolicy    []string          `json:"numeric_policy"`
	Aborted          bool              `json:"aborted,omitempty"`
}

// finiteScore 仅用于测试/调试。
func finiteScore(s Score) bool { return !math.IsNaN(s.Score) }

// WithDefaultsExported 暴露默认值补全给作业管理层。
func WithDefaultsExported(cfg Config) Config { return withDefaults(cfg) }

// FingerprintWithOverrides 与 Fingerprint 相同，但纳入峰排除覆盖层，
// 供派生作业（排除重算）获得独立于根作业的幂等指纹。
func FingerprintWithOverrides(cfg Config) string {
	c := withDefaults(cfg)
	c.Workers = 0
	return fingerprintJSON(c)
}
