package solver

import (
	"math"

	"pwidx/internal/diffract"
	"pwidx/internal/extinction"
)

// freeParams 返回某晶系统的自由参数名（轴长 + 角度）。
func freeParams(sys diffract.System) (axes []int, angs []int) {
	switch sys {
	case diffract.Cubic:
		return []int{0}, nil
	case diffract.Tetragonal, diffract.Hexagonal:
		return []int{0, 2}, nil
	case diffract.OrthorhombicSystem:
		return []int{0, 1, 2}, nil
	case diffract.Rhombohedral:
		return []int{0}, []int{3}
	case diffract.Monoclinic:
		return []int{0, 1, 2}, []int{4}
	default:
		return []int{0, 1, 2}, []int{3, 4, 5}
	}
}

func packCell(c diffract.Cell) []float64 {
	a, b, cc, al, be, ga := c.Params()
	return []float64{a, b, cc, al, be, ga}
}

func unpackCell(sys diffract.System, x []float64) diffract.Cell {
	return diffract.Cell{System: sys, A: x[0], B: x[1], C: x[2], Alpha: x[3], Beta: x[4], Gamma: x[5]}
}

// refine 对自由参数做小规模 Nelder–Mead 单纯形优化。
// 目标函数 = -Score（分数越大越好）。确定性、无随机数，便于并行。
func refine(start diffract.Cell, peaks []diffract.NormalizedPeak, wavelength float64, rules []extinction.Rule, w WeightConfig, maxVol float64, maxEval int) diffract.Cell {
	axesIdx, angIdx := freeParams(start.System)
	keep := map[int]bool{}
	for _, i := range axesIdx {
		keep[i] = true
	}
	for _, i := range angIdx {
		keep[i] = true
	}
	base := packCell(start)
	// 固定角度到系统约束。
	switch start.System {
	case diffract.Cubic:
		base[3], base[4], base[5] = 90, 90, 90
	case diffract.Tetragonal, diffract.OrthorhombicSystem:
		base[3], base[4], base[5] = 90, 90, 90
		base[1] = base[0]
		if start.System == diffract.OrthorhombicSystem {
			base[1] = packCell(start)[1]
		}
	case diffract.Hexagonal:
		base[3], base[4], base[5] = 90, 90, 120
		base[1] = base[0]
	case diffract.Rhombohedral:
		base[1], base[2] = base[0], base[0]
		base[4], base[5] = base[3], base[3]
	case diffract.Monoclinic:
		base[3], base[5] = 90, 90
	}

	free := append(append([]int{}, axesIdx...), angIdx...)
	n := len(free)
	if n == 0 {
		return start
	}
	y0 := make([]float64, n)
	for i, fi := range free {
		y0[i] = base[fi]
	}
	obj := func(y []float64) float64 {
		x := append([]float64{}, base...)
		for i, fi := range free {
			x[fi] = y[i]
		}
		// 重新施加约束（对称等价参数联动）。
		switch start.System {
		case diffract.Cubic:
			x[1], x[2] = x[0], x[0]
		case diffract.Tetragonal:
			x[1] = x[0]
		case diffract.Hexagonal:
			x[1] = x[0]
		case diffract.Rhombohedral:
			x[1], x[2] = x[0], x[0]
			x[4], x[5] = x[3], x[3]
		}
		cell := unpackCell(start.System, x)
		if !validCell(cell) || !volumeOK(cell, maxVol) {
			return math.Inf(1)
		}
		return -Evaluate(cell, peaks, wavelength, rules, w).Score
	}
	step := make([]float64, n)
	for i, fi := range free {
		if fi < 3 {
			step[i] = 0.02 * math.Max(1, math.Abs(y0[i]))
		} else {
			step[i] = 1.0
		}
	}
	yBest := nelderMead(obj, y0, step, maxEval)
	x := append([]float64{}, base...)
	for i, fi := range free {
		x[fi] = yBest[i]
	}
	switch start.System {
	case diffract.Cubic:
		x[1], x[2] = x[0], x[0]
	case diffract.Tetragonal, diffract.Hexagonal:
		x[1] = x[0]
	case diffract.Rhombohedral:
		x[1], x[2] = x[0], x[0]
		x[4], x[5] = x[3], x[3]
	}
	c := unpackCell(start.System, x)
	if !validCell(c) || !volumeOK(c, maxVol) {
		return start
	}
	return c
}

// nelderMead 为标准单纯形法（系数 α=1, γ=2, ρ=0.5, σ=0.5）。
func nelderMead(obj func([]float64) float64, x0, step []float64, maxEval int) []float64 {
	n := len(x0)
	type vert struct {
		x []float64
		f float64
	}
	evals := 0
	eval := func(x []float64) vert {
		evals++
		return vert{x, obj(x)}
	}
	simp := make([]vert, 0, n+1)
	simp = append(simp, eval(append([]float64{}, x0...)))
	for i := 0; i < n; i++ {
		x := append([]float64{}, x0...)
		x[i] += step[i]
		simp = append(simp, eval(x))
	}
	center := func(exclude int) []float64 {
		m := make([]float64, n)
		for j, v := range simp {
			if j == exclude {
				continue
			}
			for k := 0; k < n; k++ {
				m[k] += v.x[k] / float64(n)
			}
		}
		return m
	}
	sortV := func() {
		for i := 1; i < len(simp); i++ {
			for j := i; j > 0 && simp[j-1].f > simp[j].f; j-- {
				simp[j-1], simp[j] = simp[j], simp[j-1]
			}
		}
	}
	for evals < maxEval {
		sortV()
		if simp[n].f-simp[0].f < 1e-8 {
			break
		}
		cen := center(n)
		// 反射
		xr := make([]float64, n)
		for k := 0; k < n; k++ {
			xr[k] = cen[k] + (cen[k] - simp[n].x[k])
		}
		vr := eval(xr)
		if vr.f < simp[n-1].f && vr.f >= simp[0].f {
			simp[n] = vr
			continue
		}
		if vr.f < simp[0].f {
			xe := make([]float64, n)
			for k := 0; k < n; k++ {
				xe[k] = cen[k] + 2*(xr[k]-cen[k])
			}
			ve := eval(xe)
			if ve.f < vr.f {
				simp[n] = ve
			} else {
				simp[n] = vr
			}
			continue
		}
		xc := make([]float64, n)
		for k := 0; k < n; k++ {
			xc[k] = cen[k] + 0.5*(simp[n].x[k]-cen[k])
		}
		vc := eval(xc)
		if vc.f < simp[n].f {
			simp[n] = vc
			continue
		}
		// 收缩
		best := simp[0].x
		for j := 1; j <= n; j++ {
			for k := 0; k < n; k++ {
				simp[j].x[k] = best[k] + 0.5*(simp[j].x[k]-best[k])
			}
			simp[j] = eval(simp[j].x)
		}
	}
	sortV()
	return simp[0].x
}
