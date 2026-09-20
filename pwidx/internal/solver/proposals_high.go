package solver

import (
	"math"

	"pwidx/internal/diffract"
)

// 高对称晶系的解析候选建议：直接由“峰 = 某允许 hkl”反算晶格参数。
// 全部建议在单线程中确定性枚举，之后才并行打分，保证排序与并行度无关。

// cubicSums 返回 N=h²+k²+l² 升序、去重后的有限列表（N<=maxN）。
func cubicSums(maxN int) []int {
	seen := map[int]bool{}
	out := []int{}
	for h := 0; h*h <= maxN; h++ {
		for k := 0; k*k+h*h <= maxN; k++ {
			for l := 1; h*h+k*k+l*l <= maxN; l++ {
				n := h*h + k*k + l*l
				if n > 0 && !seen[n] {
					seen[n] = true
					out = append(out, n)
				}
			}
		}
	}
	sortInts(out)
	return out
}

func sortInts(x []int) {
	for i := 1; i < len(x); i++ {
		for j := i; j > 0 && x[j-1] > x[j]; j-- {
			x[j-1], x[j] = x[j], x[j-1]
		}
	}
}

func cubicProposals(ps []diffract.NormalizedPeak, rules ruleFilter) []diffract.Cell {
	out := []diffract.Cell{}
	seen := map[string]bool{}
	sums := cubicSums(35)
	lim := len(ps)
	if lim > 14 {
		lim = 14
	}
	ps = ps[:lim]
	for _, p := range ps {
		for _, n := range sums {
			hkl := anyAllowedHKL(n, rules)
			if hkl == nil {
				continue
			}
			a := p.D * math.Sqrt(float64(n))
			c := diffract.Cell{System: diffract.Cubic, A: a, B: a, C: a, Alpha: 90, Beta: 90, Gamma: 90}
			if !validCell(c) {
				continue
			}
			key := qkey(c)
			if !seen[key] {
				seen[key] = true
				out = append(out, c)
			}
		}
	}
	return out
}

// anyAllowedHKL 找到满足 h²+k²+l²=n 且通过消光规则的一组指数。
func anyAllowedHKL(n int, rules ruleFilter) *[3]int {
	for h := 0; h*h <= n; h++ {
		for k := 0; h*h+k*k <= n; k++ {
			rem := n - h*h - k*k
			l := int(math.Round(math.Sqrt(float64(rem))))
			if l*l != rem {
				continue
			}
			if h == 0 && k == 0 && l == 0 {
				continue
			}
			if rules.allow(h, k, l) {
				return &[3]int{h, k, l}
			}
			if rules.allow(l, k, h) {
				return &[3]int{l, k, h}
			}
		}
	}
	return nil
}

// tetragonal/hexagonal：用峰对 (h00),(00l) 反算 a 与 c，
// 再用混合峰 (hk0)/(h0l) 做补充；参数不一致的峰自然在打分中被淘汰。
func tetragonalProposals(ps []diffract.NormalizedPeak, rules ruleFilter, hex bool) []diffract.Cell {
	out := []diffract.Cell{}
	seen := map[string]bool{}
	add := func(a, c float64) {
		sys := diffract.Tetragonal
		if hex {
			sys = diffract.Hexagonal
		}
		g := 90.0
		ga := 90.0
		if hex {
			ga = 120
		}
		cell := diffract.Cell{System: sys, A: a, B: a, C: c, Alpha: g, Beta: g, Gamma: ga}
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
	// 候选 a：(100)/(200)/(300) 系；候选 c：(001)/(002)/(003)
	lim := len(ps)
	if lim > 14 {
		lim = 14
	}
	ps = ps[:lim]
	aCands := []float64{}
	cCands := []float64{}
	for _, p := range ps {
		for h := 1; h <= 3; h++ {
			if rules.allow(h, 0, 0) {
				aCands = append(aCands, p.D*float64(h))
			}
		}
		for l := 1; l <= 4; l++ {
			if rules.allow(0, 0, l) {
				cCands = append(cCands, p.D*float64(l))
			}
		}
	}
	for _, a := range aCands {
		for _, c := range cCands {
			add(a, c)
		}
	}
	// 混合峰补充：由 (110) 定 a（hex: (100) 与 (110) 因子不同，单独处理）
	for _, p := range ps {
		if rules.allow(1, 1, 0) {
			if !hex {
				a := p.D * math.Sqrt2
				for _, c := range cCands {
					add(a, c)
				}
			}
		}
		if hex && rules.allow(1, 0, 0) {
			// 已在 aCands；(110): 1/d² = 4/(3a²) → a = 2d/√3
			if rules.allow(1, 1, 0) {
				a := 2 * p.D / math.Sqrt(3)
				for _, c := range cCands {
					add(a, c)
				}
			}
		}
	}
	return out
}

// orthorhombicProposals 枚举“三条轴峰”组合：从峰中选 (h00),(0k0),(00l)。
// 组合数为 O(n³)，对前 8 条低角峰、h/k/l∈{1,2} 封顶。
func orthorhombicProposals(ps []diffract.NormalizedPeak, rules ruleFilter) []diffract.Cell {
	out := []diffract.Cell{}
	seen := map[string]bool{}
	ps2 := ps
	if len(ps2) > 6 {
		ps2 = ps2[:6]
	}
	type ax struct{ d, L float64 }
	as, bs, cs := []ax{}, []ax{}, []ax{}
	for _, p := range ps2 {
		for h := 1; h <= 2; h++ {
			if rules.allow(h, 0, 0) {
				as = append(as, ax{p.D * float64(h), float64(h)})
			}
			if rules.allow(0, h, 0) {
				bs = append(bs, ax{p.D * float64(h), float64(h)})
			}
			if rules.allow(0, 0, h) {
				cs = append(cs, ax{p.D * float64(h), float64(h)})
			}
		}
	}
	for _, x := range as {
		for _, y := range bs {
			for _, z := range cs {
				cell := diffract.Cell{System: diffract.OrthorhombicSystem, A: x.d, B: y.d, C: z.d, Alpha: 90, Beta: 90, Gamma: 90}
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
	return out
}
