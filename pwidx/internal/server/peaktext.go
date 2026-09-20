package server

import (
	"bufio"
	"fmt"
	"strconv"
	"strings"

	"pwidx/internal/diffract"
)

// parsePeakText 解析宽松的峰位文本：
// 每行 "2theta[,sigma[,intensity]]" 或 "d[,sigma[,intensity]]"，
// # 开头为注释；空行允许（报告 zero-peak 状态由校验层处理）。
func parsePeakText(text string, unit diffract.PeakUnit) ([]diffract.Peak, error) {
	out := []diffract.Peak{}
	sc := bufio.NewScanner(strings.NewReader(text))
	lineNo := 0
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		lineNo++
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.FieldsFunc(line, func(r rune) bool { return r == ',' || r == ';' || r == '\t' || r == ' ' })
		if len(parts) == 0 {
			continue
		}
		pos, err := strconv.ParseFloat(parts[0], 64)
		if err != nil {
			return nil, fmt.Errorf("第 %d 行峰位无法解析: %q", lineNo, parts[0])
		}
		p := diffract.Peak{Position: pos}
		if len(parts) > 1 && parts[1] != "" {
			if p.Sigma, err = strconv.ParseFloat(parts[1], 64); err != nil {
				return nil, fmt.Errorf("第 %d 行不确定度无法解析: %q", lineNo, parts[1])
			}
		}
		if len(parts) > 2 && parts[2] != "" {
			if p.Intensity, err = strconv.ParseFloat(parts[2], 64); err != nil {
				return nil, fmt.Errorf("第 %d 行强度无法解析: %q", lineNo, parts[2])
			}
		}
		out = append(out, p)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Index = i
	}
	return out, nil
}
