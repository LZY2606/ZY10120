package solver

import (
	"math"
	"sort"

	"pwidx/internal/diffract"
	"pwidx/internal/extinction"
)

// Match 为一条观测峰到一组简并 hkl 的分配。
type Match struct {
	PeakIndex   int       `json:"peak_index"`
	ObsD        float64   `json:"obs_d"`
	ObsTwoTheta float64   `json:"obs_two_theta"`
	HKLs        [][3]int  `json:"hkls"`      // 该峰可对应的全部简并/近简并反射
	PredD       []float64 `json:"pred_d"`    // 与 HKLs 对齐
	Residuals   []float64 `json:"residuals"` // 归一化残差 (pred-obs)/σ_eff
	Ambiguous   bool      `json:"ambiguous"` // >1 个 hkl 落在歧义窗内
	Primary     [3]int    `json:"primary_hkl"`
	PrimaryRes  float64   `json:"primary_residual"`
}

// UnexplainedPeak 为未被任何允许反射解释的非排除观测峰。
type UnexplainedPeak struct {
	PeakIndex   int     `json:"peak_index"`
	ObsD        float64 `json:"obs_d"`
	ObsTwoTheta float64 `json:"obs_two_theta"`
	Reason      string  `json:"reason"`
}

// MissingReflection 为观测窗内允许存在、却无观测峰对应的预测反射组。
type MissingReflection struct {
	HKL          [3]int   `json:"hkl"`
	AllHKLs      [][3]int `json:"all_hkls,omitempty"`
	PredD        float64  `json:"pred_d"`
	PredTwoTheta float64  `json:"pred_two_theta"`
	NearPeak     int      `json:"near_peak_index"` // -1 表示附近连峰都没有
	NearRes      float64  `json:"near_residual"`
	NearExcluded bool     `json:"near_peak_excluded"`
}

// Score 为候选的完整评分与证据。
type Score struct {
	Score              float64             `json:"score"`
	Explained          int                 `json:"explained"`
	ObservedUsed       int                 `json:"observed_used"`
	AmbiguousCount     int                 `json:"ambiguous_count"`
	Unexplained        int                 `json:"unexplained"`
	Missing            int                 `json:"missing"`
	RMS                float64             `json:"rms"`
	MaxRes             float64             `json:"max_abs_residual"`
	FitTerm            float64             `json:"fit_term"`
	CoverageTerm       float64             `json:"coverage_term"`
	MissingTerm        float64             `json:"missing_term"`
	ParsimonyTerm      float64             `json:"parsimony_term"`
	Matches            []Match             `json:"matches"`
	UnexplainedPeaks   []UnexplainedPeak   `json:"unexplained_peaks"`
	MissingReflections []MissingReflection `json:"missing_reflections"`
}

// WeightConfig 为评分权重与容差配置，取值写入计算清单。
type WeightConfig struct {
	MatchZ       float64
	AmbiguityZ   float64
	RelSigmaPred float64
	WFit         float64
	WCover       float64
	WMissing     float64
	WParsimony   float64
	PenaltyUnex  float64
	HKLMax       int
}

// DefaultWeights 返回默认权重；这些常数是排名组成的一部分。
func DefaultWeights() WeightConfig {
	return WeightConfig{
		MatchZ:       3.0,
		AmbiguityZ:   3.0,
		RelSigmaPred: 0.0005,
		WFit:         3.0,
		WCover:       2.0,
		WMissing:     0.5,
		WParsimony:   0.05,
		PenaltyUnex:  1.0,
		HKLMax:       6,
	}
}

type predGroup struct {
	hkls [][3]int
	ds   []float64
	d    float64
	tt   float64
}

// Evaluate 是纯函数：相同输入在任何并行度下给出相同结果。
func Evaluate(cell diffract.Cell, peaks []diffract.NormalizedPeak, wavelength float64, rules []extinction.Rule, w WeightConfig) Score {
	if w.HKLMax == 0 {
		w = DefaultWeights()
	}
	sc := Score{Matches: []Match{}, UnexplainedPeaks: []UnexplainedPeak{}, MissingReflections: []MissingReflection{}}

	used := make([]diffract.NormalizedPeak, 0)
	dMin, dMax := math.Inf(1), 0.0
	for _, p := range peaks {
		if p.Excluded {
			continue
		}
		used = append(used, p)
		if p.D < dMin {
			dMin = p.D
		}
		if p.D > dMax {
			dMax = p.D
		}
	}
	if len(used) == 0 {
		sc.Score = math.Inf(-1)
		return sc
	}
	windowLo := dMin*(1-4*w.RelSigmaPred) - 4*maxSigmaD(used)
	windowHi := dMax + 0.25*dMax

	refs := diffract.EnumerateReflections(cell, windowLo, wavelength, w.HKLMax)
	groups := groupReflections(refs, rules, windowHi, windowLo)

	type cand struct {
		pi, gi int
		z      float64
	}
	order := make([]int, len(used))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool { return used[order[a]].D > used[order[b]].D })
	cands := make([]cand, 0, len(used)*4)
	head, tail := 0, 0
	for _, pi := range order {
		p := used[pi]
		upper := p.D + matchHalfWidth(p, dMax, w, w.MatchZ)
		lower := p.D - matchHalfWidth(p, dMax, w, w.MatchZ)
		for head < len(groups) && groups[head].d > upper {
			head++
		}
		if tail < head {
			tail = head
		}
		for tail < len(groups) && groups[tail].d >= lower {
			tail++
		}
		for gi := head; gi < tail; gi++ {
			z := bestZ(p, groups[gi], w)
			if math.Abs(z) <= w.MatchZ {
				cands = append(cands, cand{pi, gi, z})
			}
		}
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if math.Abs(cands[i].z) != math.Abs(cands[j].z) {
			return math.Abs(cands[i].z) < math.Abs(cands[j].z)
		}
		if used[cands[i].pi].Index != used[cands[j].pi].Index {
			return used[cands[i].pi].Index < used[cands[j].pi].Index
		}
		return cands[i].gi < cands[j].gi
	})
	peakTaken := map[int]bool{}
	groupCovered := map[int]bool{} // 主匹配或被歧义引用，均不算缺失
	peakGroups := map[int][]int{}
	for _, cd := range cands {
		if peakTaken[cd.pi] || groupCovered[cd.gi] {
			continue
		}
		peakTaken[cd.pi] = true
		groupCovered[cd.gi] = true
		peakGroups[cd.pi] = []int{cd.gi}
	}
	// 跨组歧义：滑动窗口追加歧义窗内其他组（不改变主匹配）。
	head2, tail2 := 0, 0
	for _, pi := range order {
		p := used[pi]
		if !peakTaken[pi] {
			continue
		}
		upper := p.D + matchHalfWidth(p, dMax, w, w.AmbiguityZ)
		lower := p.D - matchHalfWidth(p, dMax, w, w.AmbiguityZ)
		for head2 < len(groups) && groups[head2].d > upper {
			head2++
		}
		if tail2 < head2 {
			tail2 = head2
		}
		for tail2 < len(groups) && groups[tail2].d >= lower {
			tail2++
		}
		for gi := head2; gi < tail2; gi++ {
			already := false
			for _, x := range peakGroups[pi] {
				if x == gi {
					already = true
				}
			}
			if already {
				continue
			}
			if math.Abs(bestZ(p, groups[gi], w)) <= w.AmbiguityZ {
				peakGroups[pi] = append(peakGroups[pi], gi)
				groupCovered[gi] = true
			}
		}
	}

	sumSq, nRes, maxRes := 0.0, 0, 0.0
	ambPeaks := 0
	for pi, p := range used {
		gis := peakGroups[pi]
		if len(gis) == 0 {
			sc.UnexplainedPeaks = append(sc.UnexplainedPeaks, UnexplainedPeak{
				PeakIndex: p.Index, ObsD: p.D, ObsTwoTheta: ttOf(p),
				Reason: "观测窗内无允许反射落入匹配带",
			})
			continue
		}
		sc.Explained++
		m := Match{PeakIndex: p.Index, ObsD: p.D, ObsTwoTheta: ttOf(p)}
		bestAbs := math.Inf(1)
		for _, gi := range gis {
			g := groups[gi]
			for j, hkl := range g.hkls {
				z := (g.ds[j] - p.D) / sigmaEff(p, g.ds[j], w)
				m.HKLs = append(m.HKLs, hkl)
				m.PredD = append(m.PredD, g.ds[j])
				m.Residuals = append(m.Residuals, z)
				if math.Abs(z) < bestAbs {
					bestAbs, m.Primary, m.PrimaryRes = math.Abs(z), hkl, z
				}
				sumSq += z * z
				nRes++
				if math.Abs(z) > maxRes {
					maxRes = math.Abs(z)
				}
			}
		}
		m.Ambiguous = len(m.HKLs) > 1
		if m.Ambiguous {
			ambPeaks++
		}
		sc.Matches = append(sc.Matches, m)
	}
	for gi, g := range groups {
		if groupCovered[gi] {
			continue
		}
		near, nz, excluded := -1, math.Inf(1), false
		for _, p := range peaks {
			z := math.Abs((g.d - p.D) / sigmaEff(p, g.d, w))
			if z < nz {
				nz, near, excluded = z, p.Index, p.Excluded
			}
		}
		sc.MissingReflections = append(sc.MissingReflections, MissingReflection{
			HKL: g.hkls[0], AllHKLs: g.hkls, PredD: g.d, PredTwoTheta: g.tt,
			NearPeak: near, NearRes: nz, NearExcluded: excluded,
		})
	}

	sort.Slice(sc.Matches, func(i, j int) bool { return sc.Matches[i].PeakIndex < sc.Matches[j].PeakIndex })
	sort.Slice(sc.UnexplainedPeaks, func(i, j int) bool { return sc.UnexplainedPeaks[i].PeakIndex < sc.UnexplainedPeaks[j].PeakIndex })
	sort.Slice(sc.MissingReflections, func(i, j int) bool { return sc.MissingReflections[i].PredD > sc.MissingReflections[j].PredD })

	sc.ObservedUsed = len(used)
	sc.AmbiguousCount = ambPeaks
	sc.Unexplained = len(sc.UnexplainedPeaks)
	sc.Missing = len(sc.MissingReflections)
	if nRes > 0 {
		sc.RMS = math.Sqrt(sumSq / float64(nRes))
	}
	sc.MaxRes = maxRes
	coverage := float64(sc.Explained) / float64(len(used))
	sc.FitTerm = sc.RMS
	sc.CoverageTerm = coverage
	sc.MissingTerm = float64(sc.Missing)
	sc.ParsimonyTerm = parsimony(cell)
	sc.Score = w.WCover*coverage -
		w.WFit*sc.RMS -
		w.WMissing*float64(sc.Missing)/math.Max(1, float64(len(groups))) -
		w.PenaltyUnex*float64(float64(sc.Unexplained)/float64(len(used))) -
		w.WParsimony*sc.ParsimonyTerm
	// 歧义不直接减分，但作为并列时的次级排序键（见 Rank 与清单）。
	return sc
}

func parsimony(c diffract.Cell) float64 {
	switch c.System {
	case diffract.Cubic:
		return 0
	case diffract.Tetragonal, diffract.Hexagonal:
		return 1
	case diffract.OrthorhombicSystem, diffract.Rhombohedral:
		return 2
	case diffract.Monoclinic:
		return 3
	default:
		return 4
	}
}

func groupReflections(refs []diffract.Reflection, rules []extinction.Rule, dHi, dLo float64) []predGroup {
	groups := make([]predGroup, 0)
	for _, r := range refs {
		if r.D > dHi || r.D < dLo {
			continue
		}
		ok, _ := extinction.Allowed(r.H, r.K, r.L, rules)
		if !ok {
			continue
		}
		if len(groups) > 0 && math.Abs(groups[len(groups)-1].d-r.D) < 1e-7*math.Max(1, r.D) {
			g := &groups[len(groups)-1]
			g.hkls = append(g.hkls, [3]int{r.H, r.K, r.L})
			g.ds = append(g.ds, r.D)
		} else {
			groups = append(groups, predGroup{
				hkls: [][3]int{{r.H, r.K, r.L}},
				ds:   []float64{r.D},
				d:    r.D,
				tt:   r.TwoTheta,
			})
		}
	}
	return groups
}

func sigmaEff(p diffract.NormalizedPeak, predD float64, w WeightConfig) float64 {
	s2 := p.SigmaD*p.SigmaD + (w.RelSigmaPred*predD)*(w.RelSigmaPred*predD)
	if s2 < 1e-12 {
		s2 = 1e-12
	}
	return math.Sqrt(s2)
}

func bestZ(p diffract.NormalizedPeak, g predGroup, w WeightConfig) float64 {
	best := math.Inf(1)
	for _, d := range g.ds {
		z := (d - p.D) / sigmaEff(p, d, w)
		if math.Abs(z) < math.Abs(best) {
			best = z
		}
	}
	return best
}

func maxSigmaD(ps []diffract.NormalizedPeak) float64 {
	m := 0.0
	for _, p := range ps {
		if p.SigmaD > m {
			m = p.SigmaD
		}
	}
	return m
}

func ttOf(p diffract.NormalizedPeak) float64 {
	if p.Unit == diffract.UnitTwoTheta {
		return p.Position
	}
	return math.NaN()
}

// matchHalfWidth 返回 z 界对应的 d 空间半宽（保守使用观测窗最大 d 估计预测相对项）。
func matchHalfWidth(p diffract.NormalizedPeak, dMax float64, w WeightConfig, z float64) float64 {
	s2 := p.SigmaD*p.SigmaD + (w.RelSigmaPred*dMax)*(w.RelSigmaPred*dMax)
	return z * math.Sqrt(s2)
}
