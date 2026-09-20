package diffract

import (
	"fmt"
	"math"
	"sort"
)

// PeakUnit 表示峰位列表的输入单位。
type PeakUnit string

const (
	UnitTwoTheta PeakUnit = "two_theta" // 度
	UnitDSpacing PeakUnit = "d"         // Å
)

// Peak 为一条原始观测峰。Excluded 为用户在候选页面上修改的“排除”覆盖标记；
// 它不会写回原始峰表（见 solver 输入的 override 层）。
type Peak struct {
	Index     int     `json:"index"`
	Position  float64 `json:"position"`            // 原始位置（2θ 度 或 d Å）
	Sigma     float64 `json:"sigma"`               // 峰位不确定度（同单位）；<=0 时用默认
	Intensity float64 `json:"intensity,omitempty"` // 可选，任意线性标度
	Excluded  bool    `json:"excluded,omitempty"`  // 排除标记（覆盖层，非原始数据）
}

// NormalizedPeak 为换算到 d 空间后的峰。
type NormalizedPeak struct {
	Index     int
	D         float64
	SigmaD    float64 // d 空间 1σ
	SigmaT    float64 // 2θ 空间 1σ（输入就是 2θ 时为原始 sigma）
	Intensity float64
	Excluded  bool
	Position  float64 // 保留原始位置用于展示
	Unit      PeakUnit
}

// ValidationIssue 为单条输入问题。
type ValidationIssue struct {
	Code    string `json:"code"`
	Index   int    `json:"index"`
	Message string `json:"message"`
}

// Normalize 把峰位换算为 d，返回归一化峰（保持输入顺序）与问题列表。
// status: "ok" 表示无错误（可能有 warning），"error" 表示无法求解。
func Normalize(peaks []Peak, unit PeakUnit, wavelength float64, defaultSigmaT, relSigmaD float64) (norm []NormalizedPeak, issues []ValidationIssue, status string) {
	status = "ok"
	if !math.IsNaN(0) {
	}
	if wavelength <= 0 {
		issues = append(issues, ValidationIssue{Code: "bad_wavelength", Message: fmt.Sprintf("波长必须为正，得到 %.6g", wavelength)})
		return nil, issues, "error"
	}
	if unit != UnitTwoTheta && unit != UnitDSpacing {
		issues = append(issues, ValidationIssue{Code: "bad_unit", Message: fmt.Sprintf("未知单位 %q，应为 two_theta 或 d", unit)})
		return nil, issues, "error"
	}
	if len(peaks) == 0 {
		issues = append(issues, ValidationIssue{Code: "empty_peaks", Message: "峰位列表为空"})
		return nil, issues, "error"
	}
	seen := map[[2]float64]int{}
	norm = make([]NormalizedPeak, 0, len(peaks))
	for _, p := range peaks {
		np := NormalizedPeak{Index: p.Index, Intensity: p.Intensity, Excluded: p.Excluded, Position: p.Position, Unit: unit}
		switch unit {
		case UnitTwoTheta:
			if math.IsNaN(p.Position) || math.IsInf(p.Position, 0) {
				issues = append(issues, ValidationIssue{Code: "non_finite", Index: p.Index, Message: "峰位不是有限数"})
				status = "error"
				continue
			}
			if p.Position <= 0 || p.Position >= 180 {
				issues = append(issues, ValidationIssue{Code: "out_of_range", Index: p.Index, Message: fmt.Sprintf("2θ=%.4f 超出 (0,180)°", p.Position)})
				status = "error"
				continue
			}
			d, err := DFromTwoTheta(p.Position, wavelength)
			if err != nil {
				issues = append(issues, ValidationIssue{Code: "arcsin_domain", Index: p.Index, Message: err.Error()})
				status = "error"
				continue
			}
			sig := p.Sigma
			if sig <= 0 {
				sig = defaultSigmaT
			}
			if sig <= 0 {
				sig = 0.05
			}
			np.D, np.SigmaT, np.SigmaD = d, sig, dSigmaFromTheta(d, p.Position, sig, wavelength)
		case UnitDSpacing:
			if math.IsNaN(p.Position) || math.IsInf(p.Position, 0) || p.Position <= 0 {
				issues = append(issues, ValidationIssue{Code: "bad_d", Index: p.Index, Message: fmt.Sprintf("d=%.6g 必须为正有限值", p.Position)})
				status = "error"
				continue
			}
			sig := p.Sigma
			if sig <= 0 {
				sig = relSigmaD * p.Position
			}
			if sig <= 0 {
				sig = 0.001 * p.Position
			}
			np.D, np.SigmaD = p.Position, sig
			tt, err := TwoTheta(p.Position, wavelength)
			if err != nil {
				issues = append(issues, ValidationIssue{Code: "arcsin_domain", Index: p.Index, Message: err.Error() + "（该峰不会出现在给定波长的观测窗内，仍保留用于 d 空间索引）"})
				np.SigmaT = math.NaN()
			} else {
				np.SigmaT = thetaSigmaFromD(p.Position, sig, wavelength)
				_ = tt
			}
		}
		key := [2]float64{round6(p.Position), 0}
		if prev, ok := seen[key]; ok {
			issues = append(issues, ValidationIssue{Code: "duplicate_peak", Index: p.Index, Message: fmt.Sprintf("与峰 #%d 位置重复 (%.6g)，两条都会保留但请确认是否为重复导入", prev, p.Position)})
		}
		seen[key] = p.Index
		norm = append(norm, np)
	}
	if len(norm) == 0 {
		status = "error"
	}
	return norm, issues, status
}

func round6(x float64) float64 { return math.Round(x*1e6) / 1e6 }

// dSigmaFromTheta 由 σ(2θ)（度）经误差传播得到 σ(d)：d = λ/(2 sinθ)，|dd/dθt| = d·cotθ
func dSigmaFromTheta(d, twoTheta, sigmaTdeg, wavelength float64) float64 {
	theta := deg(twoTheta) / 2
	ct := math.Abs(math.Cos(theta) / math.Sin(theta))
	if math.IsInf(ct, 0) || math.IsNaN(ct) {
		return sigmaTdeg * math.Pi / 180 * d
	}
	return d * ct * sigmaTdeg * math.Pi / 180
}

// thetaSigmaFromD 为反向误差传播：σ(2θ) = 2 tanθ · σ(d)/d
func thetaSigmaFromD(d, sigmaD, wavelength float64) float64 {
	x := wavelength / (2 * d)
	if x >= 1 {
		return math.NaN()
	}
	theta := math.Asin(x)
	return 2 * math.Tan(theta) * sigmaD / d * 180 / math.Pi
}

// SortedByD 返回按 d 降序的峰副本（不改动原顺序）。
func SortedByD(ps []NormalizedPeak) []NormalizedPeak {
	out := append([]NormalizedPeak(nil), ps...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].D != out[j].D {
			return out[i].D > out[j].D
		}
		return out[i].Index < out[j].Index
	})
	return out
}
