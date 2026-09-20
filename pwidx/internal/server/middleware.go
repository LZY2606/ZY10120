package server

import (
	"log"
	"net/http"
	"runtime/debug"
)

func logRecover(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("panic %s %s: %v\n%s", r.Method, r.URL.Path, rec, debug.Stack())
				http.Error(w, "内部错误", http.StatusInternalServerError)
			}
		}()
		h.ServeHTTP(w, r)
	})
}
