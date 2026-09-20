package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"pwidx/internal/diffract"
	"pwidx/internal/extinction"
	"pwidx/internal/fixtures"
	"pwidx/internal/solver"
	"pwidx/internal/store"
)

type submitReq struct {
	Peaks         []diffract.Peak   `json:"peaks"`
	Unit          diffract.PeakUnit `json:"unit"`
	Wavelength    float64           `json:"wavelength"`
	Systems       []string          `json:"systems"`
	MaxVolume     float64           `json:"max_volume"`
	Rules         []string          `json:"rules"`
	Seed          *int64            `json:"seed"`
	TopK          int               `json:"top_k"`
	RandomTrials  int               `json:"random_trials"`
	RefineEvals   int               `json:"refine_evals"`
	Workers       int               `json:"workers"`
	DefaultSigmaT float64           `json:"default_sigma_two_theta"`
	RelSigmaD     float64           `json:"rel_sigma_d"`
	Fixture       string            `json:"fixture"`
	PeakText      string            `json:"peak_text"`
}

func (s *Server) submitJob(w http.ResponseWriter, r *http.Request) {
	var req submitReq
	body, _ := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<20))
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "请求 JSON 无效: " + err.Error()})
		return
	}
	// fixture 快捷填充。
	if req.Fixture != "" && len(req.Peaks) == 0 {
		if err := fillFixture(&req); err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
	}
	if len(req.PeakText) > 0 {
		ps, err := parsePeakText(req.PeakText, req.Unit)
		if err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		req.Peaks = ps
	}
	cfg, err := req.toConfig()
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	id, created, err := s.mgr.Submit(r.Context(), cfg, "")
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	rec, _ := s.st.GetJob(r.Context(), id)
	writeJSON(w, 200, map[string]any{"job_id": id, "created": created, "status": rec.Status, "fingerprint": rec.Fingerprint})
}

func (r submitReq) toConfig() (solver.Config, error) {
	cfg := solver.Config{
		Peaks:         r.Peaks,
		Unit:          r.Unit,
		Wavelength:    r.Wavelength,
		MaxVolume:     r.MaxVolume,
		TopK:          r.TopK,
		RandomTrials:  r.RandomTrials,
		RefineEvals:   r.RefineEvals,
		Workers:       r.Workers,
		DefaultSigmaT: r.DefaultSigmaT,
		RelSigmaD:     r.RelSigmaD,
	}
	if r.Seed != nil {
		cfg.Seed = *r.Seed
	}
	for _, name := range r.Systems {
		sys := diffract.System(name)
		if !sys.Valid() {
			return cfg, fmt.Errorf("未知晶系统: %s", name)
		}
		cfg.Systems = append(cfg.Systems, sys)
	}
	for _, name := range r.Rules {
		rule := extinction.Rule(name)
		if !extinction.Valid(rule) {
			return cfg, fmt.Errorf("未知消光规则: %s", name)
		}
		cfg.Rules = append(cfg.Rules, rule)
	}
	for i := range cfg.Peaks {
		cfg.Peaks[i].Index = i
	}
	return cfg, nil
}

func fillFixture(req *submitReq) error {
	var spec *fixtures.Spec
	for i := range fixtures.Catalog() {
		if fixtures.Catalog()[i].Name == req.Fixture {
			sp := fixtures.Catalog()[i]
			spec = &sp
		}
	}
	if spec == nil {
		return errNotFound("fixture")
	}
	obs := fixtures.Generate(*spec)
	req.Peaks = make([]diffract.Peak, 0, len(obs))
	for _, o := range obs {
		req.Peaks = append(req.Peaks, diffract.Peak{
			Position: o.TwoTheta, Sigma: o.Sigma, Intensity: o.Intensity,
		})
	}
	req.Unit = diffract.UnitTwoTheta
	req.Wavelength = spec.Wavelength
	if len(req.Rules) == 0 {
		req.Rules = nil
		for _, rl := range spec.Rules {
			req.Rules = append(req.Rules, string(rl))
		}
	}
	return nil
}

func (s *Server) getJob(w http.ResponseWriter, r *http.Request) {
	rec, err := s.st.GetJob(r.Context(), r.PathValue("id"))
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "作业不存在"})
		return
	}
	writeJSON(w, 200, jobView(rec, s.mgr.Active(rec.ID)))
}

func jobView(rec store.JobRecord, active bool) map[string]any {
	v := map[string]any{
		"id": rec.ID, "fingerprint": rec.Fingerprint, "status": rec.Status,
		"active": active, "created_at": rec.CreatedAt, "updated_at": rec.UpdatedAt,
		"parent_candidate_id": rec.ParentCandidateID,
		"config":              json.RawMessage(rec.Config),
	}
	if len(rec.Manifest) > 0 {
		v["manifest"] = json.RawMessage(rec.Manifest)
	}
	if len(rec.Result) > 0 {
		v["result"] = json.RawMessage(rec.Result)
	}
	if len(rec.Issues) > 0 {
		v["issues"] = json.RawMessage(rec.Issues)
	}
	return v
}

func (s *Server) listJobs(w http.ResponseWriter, r *http.Request) {
	recs, err := s.st.ListJobs(r.Context(), 50)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	out := make([]map[string]any, 0, len(recs))
	for _, rec := range recs {
		out = append(out, map[string]any{
			"id": rec.ID, "status": rec.Status, "fingerprint": rec.Fingerprint,
			"created_at": rec.CreatedAt, "active": s.mgr.Active(rec.ID),
			"parent_candidate_id": rec.ParentCandidateID,
		})
	}
	writeJSON(w, 200, out)
}

func (s *Server) cancelJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.mgr.Cancel(id)
	writeJSON(w, 200, map[string]bool{"cancel_requested": true})
}

func (s *Server) manifest(w http.ResponseWriter, r *http.Request) {
	rec, err := s.st.GetJob(r.Context(), r.PathValue("id"))
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "作业不存在"})
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="manifest-`+rec.ID+`.json"`)
	if len(rec.Manifest) == 0 {
		_, _ = w.Write([]byte(`{"note":"作业尚未完成，无计算清单"}`))
		return
	}
	_, _ = w.Write(rec.Manifest)
}

type overrideReq struct {
	PeakIndex int  `json:"peak_index"`
	Excluded  bool `json:"excluded"`
	Rerun     bool `json:"rerun"`
}

func (s *Server) setOverride(w http.ResponseWriter, r *http.Request) {
	var req overrideReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	id := r.PathValue("id")
	if err := s.st.UpsertOverride(r.Context(), id, req.PeakIndex, req.Excluded); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	if !req.Rerun {
		writeJSON(w, 200, map[string]any{"saved": true})
		return
	}
	// 派生新作业：原始峰表与原作业不动，排除覆盖进入新输入。
	rec, err := s.st.GetJob(r.Context(), id)
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": err.Error()})
		return
	}
	var cfg solver.Config
	_ = json.Unmarshal(rec.Config, &cfg)
	ov, _ := s.st.Overrides(r.Context(), id)
	cfg.PeakOverrides = ov
	// 覆盖层不参与根指纹，因此必须挂在“派生自原作业”名下以获得独立指纹，
	// 而不是命中原作业的幂等记录。
	newID, _, err := s.mgr.Submit(r.Context(), cfg, "overrides:"+id)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"saved": true, "derived_job_id": newID})
}

type lockReq struct {
	Locked bool   `json:"locked"`
	Note   string `json:"note"`
}

func (s *Server) lockCandidate(w http.ResponseWriter, r *http.Request) {
	var req lockReq
	_ = json.NewDecoder(r.Body).Decode(&req)
	err := s.st.SetLocked(r.Context(), r.PathValue("jobID"), r.PathValue("candID"), req.Locked, req.Note)
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"locked": req.Locked})
}

type deriveReq struct {
	Overrides map[int]bool `json:"overrides"`
	Systems   []string     `json:"systems"`
}

func (s *Server) derive(w http.ResponseWriter, r *http.Request) {
	var req deriveReq
	_ = json.NewDecoder(r.Body).Decode(&req)
	id, _, err := s.mgr.SubmitDerived(r.Context(), r.PathValue("jobID"), r.PathValue("candID"), req.Overrides, req.Systems)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"job_id": id})
}

type compareReq struct {
	ID    string   `json:"id"`
	Label string   `json:"label"`
	IDs   []string `json:"candidate_ids"`
}

func (s *Server) saveCompare(w http.ResponseWriter, r *http.Request) {
	var req compareReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	if req.ID == "" {
		req.ID = "cmp-" + shortID(req.IDs)
	}
	if err := s.st.SaveCompareSet(r.Context(), req.ID, req.Label, req.IDs); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"id": req.ID})
}

func (s *Server) listCompare(w http.ResponseWriter, r *http.Request) {
	sets, err := s.st.ListCompareSets(r.Context())
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, sets)
}

func (s *Server) listFixtures(w http.ResponseWriter, r *http.Request) {
	type item struct {
		Name       string   `json:"name"`
		Wavelength float64  `json:"wavelength"`
		System     string   `json:"system"`
		Rules      []string `json:"rules"`
		NPeaks     int      `json:"n_peaks"`
	}
	out := []item{}
	for _, sp := range fixtures.Catalog() {
		rules := []string{}
		for _, rl := range sp.Rules {
			rules = append(rules, string(rl))
		}
		out = append(out, item{sp.Name, sp.Wavelength, string(sp.Cell.System), rules, len(fixtures.Generate(sp))})
	}
	writeJSON(w, 200, out)
}

func (s *Server) listExtinctions(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, extinction.Catalog)
}

func shortID(ids []string) string {
	h := 0
	for _, x := range ids {
		for i := 0; i < len(x); i++ {
			h = h*31 + int(x[i])
		}
	}
	const hexch = "0123456789abcdef"
	out := make([]byte, 10)
	for i := range out {
		out[i] = hexch[(h>>uint(i*4))&15]
		if h == 0 {
			break
		}
	}
	return string(out)
}

type sErr string

func (e sErr) Error() string        { return string(e) }
func errNotFound(what string) error { return sErr("未找到 " + what) }
