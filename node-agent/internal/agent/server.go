package agent

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"ezhiklb-node-agent/internal/domain"
)

// Server is the node's local control API. This is the new half of the
// protocol inversion: the panel dials out to this instead of the node
// polling an inbound-open panel port. It owns the in-memory apply/update
// progress that used to live in cmd/ezhiklb-agent/main.go's closures, and
// the health-monitor goroutine lifecycle (start/cancel/restart on every
// successful apply) that used to be managed there too.
type Server struct {
	runner     Runner
	reconciler *Reconciler
	monitor    *HealthMonitor
	metrics    *MetricsCollector
	apiKey     string
	version    string
	logger     *slog.Logger
	baseCtx    context.Context

	diagMu sync.Mutex
	diag   domain.NodeDiagnostics
	diagAt time.Time

	state agentState

	// OnDecommissioned runs (in its own goroutine, after the HTTP response
	// for POST /v1/decommission has been written) once local teardown
	// succeeds. main.go wires this to the real "disable the systemd unit and
	// exit the process" behaviour; left nil (as it is in tests), decommission
	// is fully exercised without killing the test process.
	OnDecommissioned func()
}

type agentState struct {
	mu              sync.Mutex
	appliedRevision int64
	applyState      string
	applyError      string
	updateState     string
	updateError     string
	healthCancel    context.CancelFunc
}

func NewServer(ctx context.Context, runner Runner, reconciler *Reconciler, monitor *HealthMonitor, metrics *MetricsCollector, apiKey, version string, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		runner: runner, reconciler: reconciler, monitor: monitor, metrics: metrics,
		apiKey: apiKey, version: version, logger: logger, baseCtx: ctx,
		state: agentState{applyState: "waiting", updateState: "idle"},
	}
}

// SeedRestoredState reflects what Reconciler.Restore() already rebuilt on
// boot (independent of the panel) into the in-memory state GET /v1/state
// reports, so the very first poll after a restart already shows the truth.
func (s *Server) SeedRestoredState(revision int64, restoreErr error) {
	s.state.mu.Lock()
	defer s.state.mu.Unlock()
	if restoreErr != nil {
		s.state.applyState, s.state.applyError = "error", restoreErr.Error()
		return
	}
	s.state.appliedRevision = revision
	if revision > 0 {
		s.state.applyState = "applied"
	} else {
		s.state.applyState = "waiting"
	}
}

// StartHealthMonitor (re)starts the ICMP health-check goroutine, cancelling
// whatever was running before — mirrors the healthCancel/healthCtx dance
// cmd/ezhiklb-agent/main.go used to do inline. It pulls outbounds (and each
// one's own HealthCheck settings) live from the reconciler's last applied
// config, so it always reflects the most recent publish without needing to
// be told the config directly.
func (s *Server) StartHealthMonitor() {
	s.state.mu.Lock()
	if s.state.healthCancel != nil {
		s.state.healthCancel()
	}
	healthCtx, cancel := context.WithCancel(s.baseCtx)
	s.state.healthCancel = cancel
	s.state.mu.Unlock()
	s.monitor.Reset()
	go s.monitor.Run(healthCtx, s.reconciler.Outbounds)
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.Handle("GET /v1/state", s.auth(http.HandlerFunc(s.handleState)))
	mux.Handle("POST /v1/apply", s.auth(http.HandlerFunc(s.handleApply)))
	mux.Handle("POST /v1/update", s.auth(http.HandlerFunc(s.handleUpdate)))
	mux.Handle("POST /v1/health-probe", s.auth(http.HandlerFunc(s.handleHealthProbe)))
	mux.Handle("POST /v1/decommission", s.auth(http.HandlerFunc(s.handleDecommission)))
	return s.recoverPanics(s.logRequests(mux))
}

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !sameSecret(token, s.apiKey) {
			writeError(w, http.StatusUnauthorized, "unauthorized", "Invalid node API key")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	stats, err := CollectIPVSStats(ctx, s.runner)
	if err != nil {
		s.logger.Warn("collect IPVS stats", "error", err)
	}
	metrics, err := s.metrics.Collect()
	if err != nil {
		s.logger.Warn("collect system metrics", "error", err)
	}
	diagnostics := s.diagnostics(ctx)

	s.state.mu.Lock()
	report := map[string]any{
		"agent_version":    s.version,
		"applied_revision": s.state.appliedRevision,
		"apply_state":      s.state.applyState,
		"apply_error":      s.state.applyError,
		"health":           s.monitor.Results(),
		"stats":            stats,
		"outbound_stats":   s.reconciler.ProxyStats(),
		"metrics":          metrics,
		"diagnostics":      diagnostics,
		"update_state":     s.state.updateState,
		"update_error":     s.state.updateError,
	}
	s.state.mu.Unlock()
	writeJSON(w, http.StatusOK, report)
}

// diagnostics rate-limits the ipvsadm/iptables probe to once a minute, same
// as the original heartbeat loop, since GET /v1/state can now be polled
// more often than that by the panel.
func (s *Server) diagnostics(ctx context.Context) domain.NodeDiagnostics {
	s.diagMu.Lock()
	defer s.diagMu.Unlock()
	if s.diagAt.IsZero() || time.Since(s.diagAt) >= time.Minute {
		s.diag = CollectDiagnostics(ctx, s.runner, s.reconciler.Services())
		s.diagAt = time.Now()
	}
	return s.diag
}

func (s *Server) handleApply(w http.ResponseWriter, r *http.Request) {
	var desired domain.NodeDesiredState
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&desired); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	s.state.mu.Lock()
	s.state.applyState = "applying"
	s.state.mu.Unlock()

	if err := s.reconciler.Reconcile(r.Context(), desired); err != nil {
		s.state.mu.Lock()
		s.state.applyState, s.state.applyError = "error", err.Error()
		s.state.mu.Unlock()
		s.logger.Error("apply desired state", "revision", desired.Revision, "error", err)
		writeError(w, http.StatusUnprocessableEntity, "apply_failed", err.Error())
		return
	}

	s.state.mu.Lock()
	s.state.appliedRevision = desired.Revision
	s.state.applyState, s.state.applyError = "applied", ""
	s.state.mu.Unlock()
	s.StartHealthMonitor()
	s.logger.Info("applied desired revision", "revision", desired.Revision, "profile", desired.ProfileName)
	writeJSON(w, http.StatusOK, map[string]any{"applied_revision": desired.Revision, "apply_state": "applied"})
}

func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TargetVersion string `json:"target_version"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	target := strings.TrimSpace(body.TargetVersion)
	if target == "" || domain.CompareVersions(target, s.version) <= 0 {
		s.state.mu.Lock()
		s.state.updateState, s.state.updateError = "completed", ""
		s.state.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]string{"update_state": "completed"})
		return
	}
	s.state.mu.Lock()
	s.state.updateState, s.state.updateError = "requested", ""
	s.state.mu.Unlock()
	go s.runUpdate(target)
	writeJSON(w, http.StatusAccepted, map[string]string{"update_state": "requested"})
}

func (s *Server) runUpdate(target string) {
	ctx, cancel := context.WithTimeout(s.baseCtx, 3*time.Minute)
	defer cancel()
	err := InstallAgentUpdate(ctx, target, func(stage string) {
		s.state.mu.Lock()
		s.state.updateState = stage
		s.state.mu.Unlock()
	})
	if err != nil {
		s.state.mu.Lock()
		s.state.updateState, s.state.updateError = "error", err.Error()
		s.state.mu.Unlock()
		s.logger.Error("update agent", "version", target, "error", err)
		return
	}
	s.state.mu.Lock()
	s.state.updateState = "restarting"
	s.state.mu.Unlock()
	s.logger.Info("agent update installed", "version", target)
	// --no-block is required when a service asks systemd to restart itself:
	// waiting for the job would wait for this very process to terminate.
	if _, err := s.runner.Run(context.Background(), "systemctl", []string{"--no-block", "restart", "ezhiklb-agent.service"}, ""); err != nil {
		s.state.mu.Lock()
		s.state.updateState, s.state.updateError = "error", err.Error()
		s.state.mu.Unlock()
	}
}

func (s *Server) handleHealthProbe(w http.ResponseWriter, r *http.Request) {
	s.monitor.CheckNow(r.Context(), s.reconciler.Outbounds())
	writeJSON(w, http.StatusOK, map[string]any{"health": s.monitor.Results()})
}

func (s *Server) handleDecommission(w http.ResponseWriter, r *http.Request) {
	s.state.mu.Lock()
	s.state.applyState = "decommissioning"
	s.state.mu.Unlock()
	if err := s.reconciler.Decommission(r.Context()); err != nil {
		s.state.mu.Lock()
		s.state.applyState, s.state.applyError = "error", err.Error()
		s.state.mu.Unlock()
		writeError(w, http.StatusUnprocessableEntity, "decommission_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"decommissioned": true})
	if s.OnDecommissioned != nil {
		go s.OnDecommissioned()
	}
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		next.ServeHTTP(w, r)
		s.logger.Info("control api request", "method", r.Method, "path", r.URL.Path, "duration", time.Since(started))
	})
}

func (s *Server) recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				s.logger.Error("request panic", "error", recovered)
				writeError(w, http.StatusInternalServerError, "internal_error", "Internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func sameSecret(a, b string) bool {
	if len(a) != len(b) || len(a) == 0 {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
