// Package diffract 提供粉末衍射索引所需的全部物理量计算：
// 晶胞几何、d-spacing、2theta/d 互转、反射枚举与输入校验。
// 长度单位统一为埃 (angstrom, Å)，角度统一为度 (degree)。
package diffract

import (
	"fmt"
	"math"
	"sort"
)

// AlgorithmVersion 冻结到候选与计算清单中的规则/算法版本。
// 任何会改变打分或候选几何的修改都必须提升此版本号。
const AlgorithmVersion = "indexer-1.0.0"

// System 为七大晶系枚举。
type System string

const (
	Cubic              System = "cubic"
	Tetragonal         System = "tetragonal"
	Hexagonal          System = "hexagonal"
	OrthorhombicSystem System = "orthorhombic"
	Rhombohedral       System = "rhombohedral"
	Monoclinic         System = "monoclinic"
	Triclinic          System = "triclinic"
)

// AllSystems 按自由度从少到多排列（同分排序偏好高对称）。
var AllSystems = []System{Cubic, Tetragonal, Hexagonal, OrthorhombicSystem, Rhombohedral, Monoclinic, Triclinic}

// Valid 报告晶系统是否受支持。
func (s System) Valid() bool {
	for _, q := range AllSystems {
		if q == s {
			return true
		}
	}
	return false
}

// Cell 为一个晶格方案。角度为度；rhombohedral 使用 a=b=c, alpha=beta=gamma；
// hexagonal 使用 a=b, gamma=120；monoclinic 使用 alpha=gamma=90, beta 为非直角。
type Cell struct {
	System System  `json:"system"`
	A      float64 `json:"a"`
	B      float64 `json:"b"`
	C      float64 `json:"c"`
	Alpha  float64 `json:"alpha"`
	Beta   float64 `json:"beta"`
	Gamma  float64 `json:"gamma"`
}

// Params 返回该晶系统下完整的 a,b,c,alpha,beta,gamma（补齐隐式角度/轴）。
func (c Cell) Params() (a, b, cc, alpha, beta, gamma float64) {
	a, b, cc = c.A, c.B, c.C
	alpha, beta, gamma = c.Alpha, c.Beta, c.Gamma
	switch c.System {
	case Cubic:
		b, cc = a, a
		alpha, beta, gamma = 90, 90, 90
	case Tetragonal:
		b = a
		alpha, beta, gamma = 90, 90, 90
	case Hexagonal:
		b = a
		alpha, beta, gamma = 90, 90, 120
	case OrthorhombicSystem:
		alpha, beta, gamma = 90, 90, 90
	case Rhombohedral:
		b, cc = a, a
		alpha, beta, gamma = c.Alpha, c.Alpha, c.Alpha
	case Monoclinic:
		alpha, gamma = 90, 90
	}
	return
}

func deg(x float64) float64 { return x * math.Pi / 180 }

// Volume 返回晶胞体积 (Å^3)。
func (c Cell) Volume() float64 {
	a, b, cc, al, be, ga := c.Params()
	ca, cb, cg := math.Cos(deg(al)), math.Cos(deg(be)), math.Cos(deg(ga))
	return a * b * cc * math.Sqrt(math.Max(0, 1-ca*ca-cb*cb-cg*cg+2*ca*cb*cg))
}

// directMetric 返回正度量张量 G（a_i·a_j）。
func (c Cell) directMetric() [3][3]float64 {
	a, b, cc, al, be, ga := c.Params()
	return [3][3]float64{
		{a * a, a * b * math.Cos(deg(ga)), a * cc * math.Cos(deg(be))},
		{a * b * math.Cos(deg(ga)), b * b, b * cc * math.Cos(deg(al))},
		{a * cc * math.Cos(deg(be)), b * cc * math.Cos(deg(al)), cc * cc},
	}
}

// reciprocalMetric 返回倒易度量张量 G*=(G)^{-1}，满足 1/d²=vᵀG*v。
func (c Cell) reciprocalMetric() [3][3]float64 {
	return invSym3(c.directMetric())
}

func invSym3(m [3][3]float64) [3][3]float64 {
	det := m[0][0]*(m[1][1]*m[2][2]-m[1][2]*m[2][1]) -
		m[0][1]*(m[1][0]*m[2][2]-m[1][2]*m[2][0]) +
		m[0][2]*(m[1][0]*m[2][1]-m[1][1]*m[2][0])
	var g [3][3]float64
	cof := func(i, j int) float64 {
		r1, r2 := (i+1)%3, (i+2)%3
		c1, c2 := (j+1)%3, (j+2)%3
		sign := 1.0
		if (i+j)%2 == 1 {
			sign = -1
		}
		return sign * (m[r1][c1]*m[r2][c2] - m[r1][c2]*m[r2][c1])
	}
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			g[j][i] = cof(i, j) / det // 逆矩阵元素 = 余子式转置/det
		}
	}
	return g
}

// InvD2 返回 1/d^2。
func (c Cell) InvD2(h, k, l int) float64 {
	g := c.reciprocalMetric()
	v := [3]float64{float64(h), float64(k), float64(l)}
	var q float64
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			q += v[i] * g[i][j] * v[j]
		}
	}
	return q
}

// D 返回反射 (h,k,l) 的面间距 (Å)。零指数返回 +Inf。
func (c Cell) D(h, k, l int) float64 {
	if h == 0 && k == 0 && l == 0 {
		return math.Inf(1)
	}
	q := c.InvD2(h, k, l)
	if q <= 0 || math.IsNaN(q) {
		return math.Inf(1)
	}
	return 1 / math.Sqrt(q)
}

// TwoTheta 返回 d 对应的 2theta（度），波长单位与 d 相同（Å）。
func TwoTheta(d, wavelength float64) (float64, error) {
	if d <= 0 || wavelength <= 0 {
		return 0, fmt.Errorf("d 与波长必须为正数")
	}
	x := wavelength / (2 * d)
	if x > 1 {
		return 0, fmt.Errorf("λ/(2d)=%.6g 超出 arcsin 定义域 (d=%.4f Å, λ=%.4f Å)：该反射在该波长下不可观测", x, d, wavelength)
	}
	return 2 * math.Asin(x) * 180 / math.Pi, nil
}

// DFromTwoTheta 由 2theta(度) 与波长(Å)计算 d (Å)。
func DFromTwoTheta(twoThetaDeg, wavelength float64) (float64, error) {
	if wavelength <= 0 {
		return 0, fmt.Errorf("波长必须为正数")
	}
	if twoThetaDeg <= 0 || twoThetaDeg >= 180 {
		return 0, fmt.Errorf("2θ=%.4f 超出物理开区间 (0,180)°", twoThetaDeg)
	}
	st := math.Sin(deg(twoThetaDeg) / 2)
	if st <= 0 {
		return 0, fmt.Errorf("2θ=%.4f 给出 sinθ<=0", twoThetaDeg)
	}
	return wavelength / (2 * st), nil
}

// Reflection 为一条预测反射。
type Reflection struct {
	H, K, L  int
	D        float64
	TwoTheta float64 // 波长>0 且可观测时填充，否则 NaN
}

// EnumerateReflections 枚举 d >= dMin 且 |h|,|k|,|l| <= hklMax 的全部非零反射，
// 按 d 降序（即 2theta 升序）排列。
func EnumerateReflections(c Cell, dMin float64, wavelength float64, hklMax int) []Reflection {
	if hklMax < 1 {
		hklMax = 1
	}
	a, b, cc, _, _, _ := c.Params()
	// 各指数单独贡献时的上界（非正交系为保守上界，用 1/2 因子放宽角度耦合）。
	hi := func(axis float64) int {
		n := int(math.Floor(axis/dMin + 1))
		if n > hklMax {
			return hklMax
		}
		if n < 1 {
			return 1
		}
		return n
	}
	nh, nk, nl := hi(a), hi(b), hi(cc)
	g := c.reciprocalMetric()
	dAt := func(h, k, l int) float64 {
		vh, vk, vl := float64(h), float64(k), float64(l)
		q := vh*vh*g[0][0] + vk*vk*g[1][1] + vl*vl*g[2][2] +
			2*(vh*vk*g[0][1]+vh*vl*g[0][2]+vk*vl*g[1][2])
		if q <= 0 || math.IsNaN(q) {
			return math.Inf(1)
		}
		return 1 / math.Sqrt(q)
	}
	out := make([]Reflection, 0, 128)
	for h := -nh; h <= nh; h++ {
		for k := -nk; k <= nk; k++ {
			for l := -nl; l <= nl; l++ {
				if h == 0 && k == 0 && l == 0 {
					continue
				}
				d := dAt(h, k, l)
				if math.IsInf(d, 1) || d < dMin {
					continue
				}
				r := Reflection{H: h, K: k, L: l, D: d, TwoTheta: math.NaN()}
				if wavelength > 0 {
					if tt, err := TwoTheta(d, wavelength); err == nil {
						r.TwoTheta = tt
					}
				}
				out = append(out, r)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].D != out[j].D {
			return out[i].D > out[j].D
		}
		if out[i].H != out[j].H {
			return out[i].H < out[j].H
		}
		if out[i].K != out[j].K {
			return out[i].K < out[j].K
		}
		return out[i].L < out[j].L
	})
	return out
}
