package daemon

import (
	"net/http"
	httppprof "net/http/pprof"
)

func (s *Server) registerPprofRoutes(mux *http.ServeMux) {
	if s == nil || mux == nil {
		return
	}
	mux.Handle("/debug/pprof/", s.withIntrospection(http.HandlerFunc(httppprof.Index), http.MethodGet))
	mux.Handle("/debug/pprof/cmdline", s.withIntrospection(http.HandlerFunc(httppprof.Cmdline), http.MethodGet))
	mux.Handle("/debug/pprof/profile", s.withIntrospection(http.HandlerFunc(httppprof.Profile), http.MethodGet))
	mux.Handle("/debug/pprof/symbol", s.withIntrospection(http.HandlerFunc(httppprof.Symbol), http.MethodGet, http.MethodPost))
	mux.Handle("/debug/pprof/trace", s.withIntrospection(http.HandlerFunc(httppprof.Trace), http.MethodGet))
	mux.Handle("/debug/pprof/allocs", s.withIntrospection(httppprof.Handler("allocs"), http.MethodGet))
	mux.Handle("/debug/pprof/block", s.withIntrospection(httppprof.Handler("block"), http.MethodGet))
	mux.Handle("/debug/pprof/goroutine", s.withIntrospection(httppprof.Handler("goroutine"), http.MethodGet))
	mux.Handle("/debug/pprof/heap", s.withIntrospection(httppprof.Handler("heap"), http.MethodGet))
	mux.Handle("/debug/pprof/mutex", s.withIntrospection(httppprof.Handler("mutex"), http.MethodGet))
	mux.Handle("/debug/pprof/threadcreate", s.withIntrospection(httppprof.Handler("threadcreate"), http.MethodGet))
}

func (s *Server) withIntrospection(next http.Handler, methods ...string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.authorize(w, r) {
			return
		}
		if !s.requireAccess(w, r, daemonModeIntrospection, "") {
			return
		}
		if len(methods) > 0 && !matchesMethod(r.Method, methods) {
			writeMethodNotAllowed(w, methods...)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func matchesMethod(method string, methods []string) bool {
	for _, candidate := range methods {
		if method == candidate {
			return true
		}
	}
	return false
}
