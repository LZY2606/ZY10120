package solver

import (
	"math"
	"math/rand"

	"pwidx/internal/diffract"
)

// rhombohedralProposals 用 (111) 与 (1-10) 类峰对反算 a, alpha。
//
//	对 (h,h,h)：1/d1² = 3(1+2ca)/a²
//	对 (h,-h,0)：1/d0² = 6(1-ca)/a²
//	令 R = d1²/d0² = 2(1-ca)/(1+2ca)  → ca=(2-R)/(2R+1)
//	再由 (1,-1,0)：a = d0·sqrt(6(1-ca))。
func rhombohedralProposals(ps []diffract.NormalizedPeak, rules ruleFilter) []diffract.Cell {
	out := []diffract.Cell{}
	seen := map[string]bool{}
	if len(ps) < 2 {
		return out
	}
	lim := len(ps)
	if lim > 6 {
		lim = 6
	}
	add := func(d1, d0 float64) {
		R := d1 * d1 / (d0 * d0)
		den := 2*R + 1
		if den == 0 {
			return
		}
		ca := (2 - R) / den
		if ca <= -1 || ca >= 1 {
			return
		}
		a := d0 * math.Sqrt(math.Max(0, 6*(1-ca)))
		alpha := math.Acos(ca) * 180 / math.Pi
		if alpha > 90 {
			alpha = 180 - alpha
		}
		cell := diffract.Cell{System: diffract.Rhombohedral, A: a, B: a, C: a, Alpha: alpha, Beta: alpha, Gamma: alpha}
		if !validCell(cell) {
			return
		}
		key := qkey(cell)
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, cell)
	}
	for i := 0; i < lim; i++ {
		for j := 0; j < lim; j++ {
			if i == j {
				continue
			}
			d1, d0 := ps[i].D, ps[j].D
			ok1 := rules.allow(1, 1, 1) || rules.allow(2, 2, 2)
			ok0 := rules.allow(1, -1, 0)
			if ok1 && ok0 {
				add(d1, d0)
			}
		}
	}
	return out
}

// monoclinicProposals 用 (h00),(00l) 定 a,c，再用 (h0l) 解 cos(beta)：
//
//	1/d² = h²/a² + l²/c² - 2hl cosβ/(ac)
//	→ cosβ = ac/(2hl)·(h²/a² + l²/c² - 1/d²)
func monoclinicProposals(ps []diffract.NormalizedPeak, rules ruleFilter) []diffract.Cell {
	out := []diffract.Cell{}
	seen := map[string]bool{}
	lim := len(ps)
	if lim > 5 {
		lim = 5
	}
	type ac struct{ a, c float64 }
	pairs := []ac{}
	for _, pa := range ps[:lim] {
		for _, pc := range ps[:lim] {
			if pa.Index == pc.Index {
				continue
			}
			if rules.allow(1, 0, 0) && rules.allow(0, 0, 1) {
				pairs = append(pairs, ac{pa.D, pc.D})
			}
		}
	}
	for _, pr := range pairs {
		for _, pm := range ps[:lim] {
			for _, h := range []int{1, 2} {
				for _, l := range []int{1, 2} {
					if !rules.allow(h, 0, l) {
						continue
					}
					ia := float64(h) * float64(h) / (pr.a * pr.a)
					ic := float64(l) * float64(l) / (pr.c * pr.c)
					id := 1 / (pm.D * pm.D)
					cb := pr.a * pr.c / (2 * float64(h*l)) * (ia + ic - id)
					if cb <= -1 || cb >= 1 {
						continue
					}
					beta := math.Acos(cb) * 180 / math.Pi
					if beta > 90 {
						beta = 180 - beta
					}
					// b 从 (0k0) 峰或混合峰估；先放 1..2 倍 a 的占位，后续随机精修补全。
					b := pr.a
					if rules.allow(0, 1, 0) {
						for _, pb := range ps[:lim] {
							b = pb.D
						}
					}
					cell := diffract.Cell{System: diffract.Monoclinic, A: pr.a, B: b, C: pr.c, Alpha: 90, Beta: beta, Gamma: 90}
					if !validCell(cell) {
						continue
					}
					key := qkey(cell)
					if seen[key] {
						continue
					}
					seen[key] = true
					out = append(out, cell)
				}
			}
		}
	}
	return out
}

// triclinicAnalyticProposals 用前 6 条峰的 720 种排列分配到
// (100),(010),(001),(110),(101),(011)，解出度量张量全部 6 个独立分量。
func triclinicAnalyticProposals(ps []diffract.NormalizedPeak, rules ruleFilter) []diffract.Cell {
	out := []diffract.Cell{}
	seen := map[string]bool{}
	idx := []int{0, 1, 2, 3, 4, 5}
	if len(ps) < 6 {
		return out
	}
	need := [6][3]int{{1, 0, 0}, {0, 1, 0}, {0, 0, 1}, {1, 1, 0}, {1, 0, 1}, {0, 1, 1}}
	for _, hkl := range need {
		if !rules.allow(hkl[0], hkl[1], hkl[2]) {
			return out
		}
	}
	var perms func(int)
	swap := func(i, j int) { idx[i], idx[j] = idx[j], idx[i] }
	perms = func(k int) {
		if len(out) >= 720 {
			return
		}
		if k == 6 {
			q := [6]float64{}
			for i, pi := range idx {
				q[i] = 1 / (ps[pi].D * ps[pi].D)
			}
			g11, g22, g33, g12, g13, g23 := q[0], q[1], q[2], (q[3]-q[0]-q[1])/2, (q[4]-q[0]-q[2])/2, (q[5]-q[1]-q[2])/2
			cell, ok := cellFromReciprocal(diffract.Triclinic, [6]float64{g11, g22, g33, g12, g13, g23})
			if !ok || !validCell(cell) {
				return
			}
			key := qkey(cell)
			if seen[key] {
				return
			}
			seen[key] = true
			out = append(out, cell)
			return
		}
		for i := k; i < 6; i++ {
			swap(k, i)
			perms(k + 1)
			swap(k, i)
		}
	}
	perms(0)
	return out
}

// randomProposals 用受控种子为低对称晶系生成确定性随机起点。
// 参数范围由观测 d 范围推导：轴长 ∈ [0.3·dmax, 3·dmax]。
func randomProposals(ps []diffract.NormalizedPeak, systems []diffract.System, n int, seed int64) []diffract.Cell {
	rng := rand.New(rand.NewSource(seed))
	dMin, dMax := math.Inf(1), 0.0
	for _, p := range ps {
		if p.D < dMin {
			dMin = p.D
		}
		if p.D > dMax {
			dMax = p.D
		}
	}
	out := make([]diffract.Cell, 0, n)
	seen := map[string]bool{}
	makeCell := func(sys diffract.System) diffract.Cell {
		axis := func() float64 { return dMax * (0.3 + rng.Float64()*2.7) }
		ang := func(lo, hi float64) float64 { return lo + rng.Float64()*(hi-lo) }
		switch sys {
		case diffract.Rhombohedral:
			a := axis()
			al := ang(40, 120)
			return diffract.Cell{System: sys, A: a, B: a, C: a, Alpha: al, Beta: al, Gamma: al}
		case diffract.Monoclinic:
			return diffract.Cell{System: sys, A: axis(), B: axis(), C: axis(), Alpha: 90, Beta: ang(50, 130), Gamma: 90}
		case diffract.Triclinic:
			return diffract.Cell{System: sys, A: axis(), B: axis(), C: axis(),
				Alpha: ang(45, 135), Beta: ang(45, 135), Gamma: ang(45, 135)}
		}
		return diffract.Cell{}
	}
	low := []diffract.System{}
	for _, s := range systems {
		if s == diffract.Rhombohedral || s == diffract.Monoclinic || s == diffract.Triclinic {
			low = append(low, s)
		}
	}
	if len(low) == 0 {
		return nil
	}
	for len(out) < n {
		sys := low[rng.Intn(len(low))]
		c := makeCell(sys)
		if !validCell(c) {
			continue
		}
		key := qkey(c)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, c)
	}
	_ = dMin
	return out
}
