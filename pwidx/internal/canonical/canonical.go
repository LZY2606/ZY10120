// Package canonical 把晶格约化为稳定标准形，消除等价轴置换、
// 晶胞选择（三斜 GL(3,Z) 重选约化胞）与符号简并造成的重复 Top-K。
//
// 三斜/单斜采用有界枚举的 Minkowski/Delaunay 风格短向量约化：
// 在 |U v|_∞<=M 的整数变换中选取使确定性代价最小的胞。
// 这不是完整 Niggli 约化，但在求解器 hkl 范围内稳定、确定性且自包含，
// 限制已写入 README 与计算清单。
package canonical

import (
	"math"
	"sort"

	"pwidx/internal/diffract"
)

// M 为整数变换元素的枚举界。
const M = 1

// gl3z 在包初始化时枚举 det=±1、|u_ij|<=M 的全部 3x3 整数矩阵。
var gl3z [][3][3]int

func init() {
	rangeVals := make([]int, 2*M+1)
	for i := range rangeVals {
		rangeVals[i] = i - M
	}
	for _, a := range rangeVals {
		for _, b := range rangeVals {
			for _, c := range rangeVals {
				for _, d := range rangeVals {
					for _, e := range rangeVals {
						for _, f := range rangeVals {
							for _, g := range rangeVals {
								for _, h := range rangeVals {
									for _, i := range rangeVals {
										U := [3][3]int{{a, b, c}, {d, e, f}, {g, h, i}}
										if det3(U) == 1 || det3(U) == -1 {
											gl3z = append(gl3z, U)
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}
	// 固定顺序，保证不同机器/并行度一致。
	sort.Slice(gl3z, func(i, j int) bool {
		for r := 0; r < 3; r++ {
			for c := 0; c < 3; c++ {
				if gl3z[i][r][c] != gl3z[j][r][c] {
					return gl3z[i][r][c] < gl3z[j][r][c]
				}
			}
		}
		return false
	})
}

func det3(u [3][3]int) int {
	return u[0][0]*(u[1][1]*u[2][2]-u[1][2]*u[2][1]) -
		u[0][1]*(u[1][0]*u[2][2]-u[1][2]*u[2][0]) +
		u[0][2]*(u[1][0]*u[2][1]-u[1][1]*u[2][0])
}

// directMetric 由胞参数计算正度量张量 a_i·a_j。
func directMetric(c diffract.Cell) [3][3]float64 {
	a, b, cc, al, be, ga := c.Params()
	rad := func(x float64) float64 { return x * math.Pi / 180 }
	return [3][3]float64{
		{a * a, a * b * math.Cos(rad(ga)), a * cc * math.Cos(rad(be))},
		{a * b * math.Cos(rad(ga)), b * b, b * cc * math.Cos(rad(al))},
		{a * cc * math.Cos(rad(be)), b * cc * math.Cos(rad(al)), cc * cc},
	}
}

func matFromG(g [3][3]float64, system diffract.System) diffract.Cell {
	a := math.Sqrt(g[0][0])
	b := math.Sqrt(g[1][1])
	cc := math.Sqrt(g[2][2])
	clamp := func(x float64) float64 {
		if x > 1 {
			x = 1
		}
		if x < -1 {
			x = -1
		}
		return x
	}
	ang := func(dot, p, q float64) float64 {
		cos := clamp(dot / (p * q))
		return math.Acos(cos) * 180 / math.Pi
	}
	cell := diffract.Cell{System: system, A: a, B: b, C: cc,
		Alpha: ang(g[1][2], b, cc), Beta: ang(g[0][2], a, cc), Gamma: ang(g[0][1], a, b)}
	switch system {
	case diffract.Cubic:
		cell.B, cell.C = a, a
		cell.Alpha, cell.Beta, cell.Gamma = 90, 90, 90
	case diffract.Tetragonal:
		cell.B = a
		cell.Alpha, cell.Beta, cell.Gamma = 90, 90, 90
	case diffract.Hexagonal:
		cell.B = a
		cell.Alpha, cell.Beta, cell.Gamma = 90, 90, 120
	case diffract.OrthorhombicSystem:
		cell.Alpha, cell.Beta, cell.Gamma = 90, 90, 90
	case diffract.Rhombohedral:
		// 取 alpha 与 180-alpha 中较小者（短/长菱面体选择等价）。
		al := cell.Alpha
		if al > 90 {
			al = 180 - al
		}
		cell.B, cell.C = a, a
		cell.Alpha, cell.Beta, cell.Gamma = al, al, al
	case diffract.Monoclinic:
		// 唯一非直角 beta；同时取 beta<=90（轴翻转等价）。
		be := cell.Beta
		if be > 90 {
			be = 180 - be
		}
		cell.Alpha, cell.Beta, cell.Gamma = 90, be, 90
	}
	return cell
}

func transformMetric(g [3][3]float64, u [3][3]int) [3][3]float64 {
	// G' = U G U^T
	var ug [3][3]float64
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			s := 0.0
			for k := 0; k < 3; k++ {
				s += float64(u[i][k]) * g[k][j]
			}
			ug[i][j] = s
		}
	}
	var out [3][3]float64
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			s := 0.0
			for k := 0; k < 3; k++ {
				s += ug[i][k] * float64(u[j][k])
			}
			out[i][j] = s
		}
	}
	return out
}

// cost 为标准形选择的确定性词典代价：轴长升序、非直角接近 90°、
// 对角项升序排列后比较（鼓励短轴在前），再用各角度与非对角项收尾。
func cost(g [3][3]float64) []float64 {
	d := []float64{g[0][0], g[1][1], g[2][2]}
	sort.Float64s(d)
	return []float64{
		d[0], d[1], d[2],
		math.Abs(g[0][1]), math.Abs(g[0][2]), math.Abs(g[1][2]),
		-g[0][1], -g[0][2], -g[1][2],
	}
}

func lessCost(x, y []float64) bool {
	for i := range x {
		if math.Abs(x[i]-y[i]) > 1e-10*math.Max(1, math.Abs(x[i])) {
			return x[i] < y[i]
		}
	}
	return false
}

// Reduce 返回标准形胞。对三斜枚举全部有界 GL(3,Z)；
// 单斜仅允许保持 b 为唯一非直角轴的变换（I 与 a/c 轴的有界剪切）；
// 正交及以上只做等价轴置换。
func Reduce(c diffract.Cell) diffract.Cell {
	g0 := directMetric(c)
	bestG := g0
	bestC := cost(g0)

	consider := func(g [3][3]float64) {
		cs := cost(g)
		if lessCost(cs, bestC) {
			bestG, bestC = g, cs
		}
	}

	switch c.System {
	case diffract.Cubic:
		return c
	case diffract.Tetragonal, diffract.Hexagonal:
		// a=b：仅 a/c 互换会改变晶格（一般不允许，除非 a==c），
		// 因此保持 c 在第三位；符号不影响度量，无需变换。
		return orthoLike(c)
	case diffract.OrthorhombicSystem:
		return orthoLike(c)
	case diffract.Rhombohedral:
		return matFromG(bestG, c.System)
	case diffract.Monoclinic:
		// b 轴保持第二；允许变换
		//   a' = a + m c (m∈{-1,0,1})（保持 b、beta 约定），
		// 以及 a<->c 互换后重新规范 beta。
		for _, U := range monoclinicTransforms() {
			consider(transformMetric(g0, U))
		}
	case diffract.Triclinic:
		for _, U := range gl3z {
			consider(transformMetric(g0, U))
		}
	}
	return matFromG(bestG, c.System)
}

// monoclinicTransforms 保持 y(b) 轴不变的有界幺模变换。
func monoclinicTransforms() [][3][3]int {
	out := make([][3][3]int, 0, 6)
	for m := -M; m <= M; m++ {
		out = append(out, [3][3]int{{1, 0, m}, {0, 1, 0}, {0, 0, 1}})
	}
	out = append(out,
		[3][3]int{{-1, 0, 0}, {0, 1, 0}, {0, 0, -1}},
		[3][3]int{{0, 0, 1}, {0, -1, 0}, {1, 0, 0}},
	)
	return out
}

// orthoLike 对正交/四方/六方做等价轴置换标准形：
// 正交把 a<=b<=c；四方/六方保持 (a,a,c) 但把 c 放到正确的第三位。
func orthoLike(c diffract.Cell) diffract.Cell {
	a, b, cc, al, be, ga := c.Params()
	switch c.System {
	case diffract.OrthorhombicSystem:
		type ax struct {
			l float64
			n string
		}
		axes := []ax{{a, "a"}, {b, "b"}, {cc, "c"}}
		sort.Slice(axes, func(i, j int) bool {
			if math.Abs(axes[i].l-axes[j].l) > 1e-9 {
				return axes[i].l < axes[j].l
			}
			return axes[i].n < axes[j].n
		})
		return diffract.Cell{System: c.System, A: axes[0].l, B: axes[1].l, C: axes[2].l, Alpha: 90, Beta: 90, Gamma: 90}
	case diffract.Tetragonal:
		return diffract.Cell{System: c.System, A: a, B: a, C: cc, Alpha: 90, Beta: 90, Gamma: 90}
	case diffract.Hexagonal:
		return diffract.Cell{System: c.System, A: a, B: a, C: cc, Alpha: 90, Beta: 90, Gamma: 120}
	}
	_ = al
	_ = be
	_ = ga
	return c
}

// quant 把参数量化到固定相对精度，用于同一标准形去重。
func quant(x float64) int64 {
	return int64(math.Round(x / math.Max(1e-6, 1e-5*math.Max(1, x))))
}

// Key 返回标准形的稳定去重键（晶系统 + 量化参数）。
func Key(c diffract.Cell) string {
	r := Reduce(c)
	a, b, cc, al, be, ga := r.Params()
	vals := []float64{a, b, cc}
	if r.System == diffract.Triclinic || r.System == diffract.Monoclinic || r.System == diffract.Rhombohedral {
		vals = append(vals, al, be, ga)
	}
	var sb []byte
	sb = append(sb, string(r.System)...)
	for _, v := range vals {
		sb = append(sb, '|')
		sb = append(sb, []byte(itoa(quant(v)))...)
	}
	return string(sb)
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [32]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
