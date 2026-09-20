package solver

import (
	"fmt"
	"math"
	"strconv"

	"pwidx/internal/diffract"
	"pwidx/internal/extinction"
)

// ruleFilter 把消光规则列表包成判定函数（生成建议时预过滤不可能的 hkl）。
type ruleFilter struct{ rules []extinction.Rule }

func (f ruleFilter) allow(h, k, l int) bool {
	ok, _ := extinction.Allowed(h, k, l, f.rules)
	return ok
}

func validCell(c diffract.Cell) bool {
	a, b, cc, al, be, ga := c.Params()
	if !(a > 0.5 && b > 0.5 && cc > 0.5) {
		return false
	}
	if !(a < 1000 && b < 1000 && cc < 1000) {
		return false
	}
	for _, x := range []float64{al, be, ga} {
		if x < 20 || x > 160 || math.IsNaN(x) {
			return false
		}
	}
	v := c.Volume()
	if math.IsNaN(v) || v <= 0 || math.IsInf(v, 0) {
		return false
	}
	return true
}

func volumeOK(c diffract.Cell, maxVol float64) bool {
	v := c.Volume()
	if math.IsNaN(v) || v <= 0 {
		return false
	}
	return v <= maxVol*(1+1e-9)
}

// qkey 为建议级去重键（约化前的粗量化，最终去重以 canonical.Key 为准）。
func qkey(c diffract.Cell) string {
	a, b, cc, al, be, ga := c.Params()
	q := func(x float64) string { return strconv.Itoa(int(math.Round(x * 1000))) }
	s := string(c.System) + "|" + q(a) + "|" + q(b) + "|" + q(cc)
	if c.System == diffract.Monoclinic || c.System == diffract.Triclinic || c.System == diffract.Rhombohedral {
		s += "|" + q(al) + "|" + q(be) + "|" + q(ga)
	}
	return s
}

// cellFromReciprocal 由倒易度量张量分量反解胞参数。
// s = (s11,s22,s33,s12,s13,s23)，1/d²(hkl)=vᵀSv。
func cellFromReciprocal(sys diffract.System, s [6]float64) (diffract.Cell, bool) {
	S := [3][3]float64{
		{s[0], s[3], s[4]},
		{s[3], s[1], s[5]},
		{s[4], s[5], s[2]},
	}
	G, ok := invert3(S)
	if !ok {
		return diffract.Cell{}, false
	}
	cell := cellFromDirectMetric(sys, G)
	return cell, true
}

func cellFromDirectMetric(sys diffract.System, G [3][3]float64) diffract.Cell {
	a := math.Sqrt(math.Max(0, G[0][0]))
	b := math.Sqrt(math.Max(0, G[1][1]))
	cc := math.Sqrt(math.Max(0, G[2][2]))
	ang := func(dot, p, q float64) float64 {
		if p*q == 0 {
			return 90
		}
		cos := dot / (p * q)
		cos = math.Max(-1, math.Min(1, cos))
		return math.Acos(cos) * 180 / math.Pi
	}
	cell := diffract.Cell{
		System: sys, A: a, B: b, C: cc,
		Alpha: ang(G[1][2], b, cc),
		Beta:  ang(G[0][2], a, cc),
		Gamma: ang(G[0][1], a, b),
	}
	switch sys {
	case diffract.Cubic:
		cell.B, cell.C, cell.Alpha, cell.Beta, cell.Gamma = a, a, 90, 90, 90
	case diffract.Tetragonal:
		cell.B, cell.Alpha, cell.Beta, cell.Gamma = a, 90, 90, 90
	case diffract.Hexagonal:
		cell.B, cell.Alpha, cell.Beta, cell.Gamma = a, 90, 90, 120
	case diffract.OrthorhombicSystem:
		cell.Alpha, cell.Beta, cell.Gamma = 90, 90, 90
	case diffract.Rhombohedral:
		al := cell.Alpha
		if al > 90 {
			al = 180 - al
		}
		cell.B, cell.C = a, a
		cell.Alpha, cell.Beta, cell.Gamma = al, al, al
	case diffract.Monoclinic:
		be := cell.Beta
		if be > 90 {
			be = 180 - be
		}
		cell.Alpha, cell.Beta, cell.Gamma = 90, be, 90
	}
	return cell
}

func invert3(m [3][3]float64) ([3][3]float64, bool) {
	det := m[0][0]*(m[1][1]*m[2][2]-m[1][2]*m[2][1]) -
		m[0][1]*(m[1][0]*m[2][2]-m[1][2]*m[2][0]) +
		m[0][2]*(m[1][0]*m[2][1]-m[1][1]*m[2][0])
	if math.Abs(det) < 1e-18 {
		return [3][3]float64{}, false
	}
	inv := func(i, j int) float64 {
		r1, r2 := (j+1)%3, (j+2)%3
		c1, c2 := (i+1)%3, (i+2)%3
		sign := 1.0
		if (i+j)%2 == 1 {
			sign = -1
		}
		return sign * (m[r1][c1]*m[r2][c2] - m[r1][c2]*m[r2][c1]) / det
	}
	var out [3][3]float64
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			out[i][j] = inv(j, i)
		}
	}
	// 正定性检查（倒易阵也必须正定）。
	if out[0][0] <= 0 || out[1][1] <= 0 || out[2][2] <= 0 {
		return out, false
	}
	return out, true
}

func ftoa(x float64) string { return fmt.Sprintf("%.6g", x) }
