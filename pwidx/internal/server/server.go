// Package server 提供 HTTP 页面与 JSON API：提交/查询作业、查看候选、
// 锁定、派生、修改峰排除标记、比较候选与导出计算清单。
package server

import (
	"context"
	"encoding/json"
	"html/template"
	"net/http"
	"path"
	"strings"

	"pwidx/internal/diffract"
	"pwidx/internal/extinction"
	"pwidx/internal/fixtures"
	"pwidx/internal/store"
)

// Server 持有状态存储与作业管理器。
type Server struct {
	st  *store.Store
	mgr *store.Manager
	tpl *template.Template
}

// New 创建 HTTP 处理器集合。
func New(st *store.Store) (*Server, error) {
	tpl, err := template.New("").Funcs(template.FuncMap{
		"json": func(v any) (template.JS, error) {
			b, err := json.Marshal(v)
			return template.JS(b), err
		},
		"join": strings.Join,
	}).ParseFS(webFS, "web/*.html")
	if err != nil {
		return nil, err
	}
	return &Server{st: st, mgr: store.NewManager(st), tpl: tpl}, nil
}

// Routes 注册路由。
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.index)
	mux.HandleFunc("GET /jobs/{id}", s.jobPage)
	mux.HandleFunc("GET /candidates/{jobID}/{candID}", s.candidatePage)
	mux.HandleFunc("GET /compare", s.comparePage)
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS()))))

	mux.HandleFunc("GET /api/fixtures", s.listFixtures)
	mux.HandleFunc("GET /api/extinctions", s.listExtinctions)
	mux.HandleFunc("POST /api/jobs", s.submitJob)
	mux.HandleFunc("GET /api/jobs", s.listJobs)
	mux.HandleFunc("GET /api/jobs/{id}", s.getJob)
	mux.HandleFunc("POST /api/jobs/{id}/cancel", s.cancelJob)
	mux.HandleFunc("GET /api/jobs/{id}/manifest", s.manifest)
	mux.HandleFunc("POST /api/jobs/{id}/overrides", s.setOverride)
	mux.HandleFunc("POST /api/candidates/{jobID}/{candID}/lock", s.lockCandidate)
	mux.HandleFunc("POST /api/candidates/{jobID}/{candID}/derive", s.derive)
	mux.HandleFunc("POST /api/compare", s.saveCompare)
	mux.HandleFunc("GET /api/compare", s.listCompare)
	return logRecover(mux)
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	data := map[string]any{
		"Fixtures":  fixtures.Catalog(),
		"Systems":   diffract.AllSystems,
		"ExtRules":  extinction.Catalog,
		"Algorithm": diffract.AlgorithmVersion,
	}
	s.render(w, "index.html", data)
}

func (s *Server) jobPage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rec, err := s.st.GetJob(r.Context(), id)
	if err != nil {
		http.Error(w, "作业不存在", http.StatusNotFound)
		return
	}
	var cfg map[string]any
	_ = json.Unmarshal(rec.Config, &cfg)
	var result map[string]any
	if len(rec.Result) > 0 {
		_ = json.Unmarshal(rec.Result, &result)
	}
	s.render(w, "job.html", map[string]any{
		"Job":    rec,
		"Config": cfg,
		"Result": result,
		"Active": s.mgr.Active(id),
		"HasRes": len(rec.Result) > 0,
	})
}

func (s *Server) candidatePage(w http.ResponseWriter, r *http.Request) {
	jobID, candID := r.PathValue("jobID"), r.PathValue("candID")
	raw, err := s.st.CandidatePayload(r.Context(), jobID, candID)
	if err != nil {
		http.Error(w, "候选不存在", http.StatusNotFound)
		return
	}
	overrides, _ := s.st.Overrides(r.Context(), jobID)
	job, _ := s.st.GetJob(r.Context(), jobID)
	var result map[string]any
	_ = json.Unmarshal(job.Result, &result)
	var cand map[string]any
	_ = json.Unmarshal(raw, &cand)
	s.render(w, "candidate.html", map[string]any{
		"JobID":     jobID,
		"CandID":    candID,
		"Candidate": cand,
		"Result":    result,
		"Overrides": overrides,
		"Raw":       string(raw),
	})
}

func (s *Server) comparePage(w http.ResponseWriter, r *http.Request) {
	sets, _ := s.st.ListCompareSets(r.Context())
	s.render(w, "compare.html", map[string]any{"Sets": sets})
}

func (s *Server) render(w http.ResponseWriter, name string, data map[string]any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tpl.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// Manager 暴露给测试。
func (s *Server) Manager() *store.Manager { return s.mgr }

// Store 暴露给测试。
func (s *Server) Store() *store.Store { return s.st }

// StaticFile 供 health 等场景占位。
func StaticFile(name string) string { return path.Base(name) }

// 确保 context 被引用（恢复后重新排队由客户端重新 POST）。
var _ = context.Background
