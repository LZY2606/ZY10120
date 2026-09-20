// Package fixtures 提供确定性合成粉末图样 fixture：由已知晶格生成
// 理论 2theta 峰并加入按种子控制的噪声，供演示与测试使用。
package fixtures

import (
	"math"
	"math/rand"

	"pwidx/internal/diffract"
	"pwidx/internal/extinction"
)

// Spec 描述一个合成图样。
type Spec struct {
	Name        string
	Cell        diffract.Cell
	Wavelength  float64
	Rules       []extinction.Rule
	TwoThetaMax float64
	HKLMax      int
	// 噪声
	Seed          int64
	JitterSigma2T float64 // 度
	MissingProb   float64
	ImpurityCount int // 额外杂峰数量
}

// Catalog 返回内置 fixture 目录。
func Catalog() []Spec {
	return []Spec{
		{
			Name:       "cubic_F_NaCl_like",
			Cell:       diffract.Cell{System: diffract.Cubic, A: 5.64, B: 5.64, C: 5.64, Alpha: 90, Beta: 90, Gamma: 90},
			Wavelength: 1.5406, Rules: []extinction.Rule{extinction.FBaseCentered},
			TwoThetaMax: 90, HKLMax: 6, Seed: 101, JitterSigma2T: 0.03, MissingProb: 0.05, ImpurityCount: 1,
		},
		{
			Name:       "tetragonal_P_rutile_like",
			Cell:       diffract.Cell{System: diffract.Tetragonal, A: 4.594, B: 4.594, C: 2.959, Alpha: 90, Beta: 90, Gamma: 90},
			Wavelength: 1.5406, Rules: nil,
			TwoThetaMax: 90, HKLMax: 6, Seed: 202, JitterSigma2T: 0.03, MissingProb: 0.05, ImpurityCount: 0,
		},
		{
			Name:       "hexagonal_P_graphite_like",
			Cell:       diffract.Cell{System: diffract.Hexagonal, A: 2.464, B: 2.464, C: 6.712, Alpha: 90, Beta: 90, Gamma: 120},
			Wavelength: 1.5406, Rules: nil,
			TwoThetaMax: 90, HKLMax: 6, Seed: 303, JitterSigma2T: 0.03, MissingProb: 0.08, ImpurityCount: 1,
		},
		{
			Name:       "orthorhombic_P_forsterite_like",
			Cell:       diffract.Cell{System: diffract.OrthorhombicSystem, A: 4.75, B: 10.20, C: 5.98, Alpha: 90, Beta: 90, Gamma: 90},
			Wavelength: 1.5406, Rules: nil,
			TwoThetaMax: 70, HKLMax: 5, Seed: 404, JitterSigma2T: 0.04, MissingProb: 0.1, ImpurityCount: 1,
		},
		{
			Name:       "rhombohedral_R_calcite_like",
			Cell:       diffract.Cell{System: diffract.Rhombohedral, A: 6.36, B: 6.36, C: 6.36, Alpha: 46.1, Beta: 46.1, Gamma: 46.1},
			Wavelength: 1.5406, Rules: nil,
			TwoThetaMax: 80, HKLMax: 5, Seed: 505, JitterSigma2T: 0.04, MissingProb: 0.1, ImpurityCount: 0,
		},
		{
			Name:       "monoclinic_P_generic",
			Cell:       diffract.Cell{System: diffract.Monoclinic, A: 5.20, B: 8.10, C: 6.30, Alpha: 90, Beta: 104.2, Gamma: 90},
			Wavelength: 1.5406, Rules: nil,
			TwoThetaMax: 60, HKLMax: 4, Seed: 606, JitterSigma2T: 0.05, MissingProb: 0.15, ImpurityCount: 2,
		},
	}
}

// PeakObs 为生成的观测峰。
type PeakObs struct {
	TwoTheta  float64
	Sigma     float64
	Intensity float64
	HKL       [3]int
	Impurity  bool
}

// Generate 依据 spec 生成带噪合成峰列表（2theta 升序）。
func Generate(s Spec) []PeakObs {
	rng := rand.New(rand.NewSource(s.Seed))
	refs := diffract.EnumerateReflections(s.Cell, 1.0, s.Wavelength, s.HKLMax)
	out := make([]PeakObs, 0, 32)
	lastTT := -1.0
	for _, r := range refs {
		if math.IsNaN(r.TwoTheta) || r.TwoTheta < 3 || r.TwoTheta > s.TwoThetaMax {
			continue
		}
		ok, _ := extinction.Allowed(r.H, r.K, r.L, s.Rules)
		if !ok {
			continue
		}
		// 同一 2theta 简并组只保留一条“观测峰”（强度相加在真实仪器中发生，
		// 歧义仍由打分器的简并组展示）。
		if r.TwoTheta-lastTT < 0.02 {
			continue
		}
		lastTT = r.TwoTheta
		if s.MissingProb > 0 && rng.Float64() < s.MissingProb {
			continue
		}
		tt := r.TwoTheta + rng.NormFloat64()*s.JitterSigma2T
		intensity := math.Max(5, 100*math.Exp(-math.Pow((r.TwoTheta-20)/35, 2))+rng.Float64()*10)
		out = append(out, PeakObs{TwoTheta: round4(tt), Sigma: s.JitterSigma2T, Intensity: round2(intensity), HKL: [3]int{r.H, r.K, r.L}})
	}
	// 杂峰：均匀落在观测范围。
	used := map[float64]bool{}
	for i := 0; i < s.ImpurityCount; i++ {
		tt := 8 + rng.Float64()*(s.TwoThetaMax-16)
		key := math.Round(tt*10) / 10
		if used[key] {
			continue
		}
		used[key] = true
		out = append(out, PeakObs{TwoTheta: round4(tt), Sigma: s.JitterSigma2T, Intensity: round2(8 + rng.Float64()*15), Impurity: true})
	}
	// 插入式排序。
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1].TwoTheta > out[j].TwoTheta; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

func round2(x float64) float64 { return math.Round(x*100) / 100 }
func round4(x float64) float64 { return math.Round(x*10000) / 10000 }
