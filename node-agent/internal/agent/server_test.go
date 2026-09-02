package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"ezhiklb-node-agent/internal/domain"
)

func newTestServer(t *testing.T) (*Server, *fakeRunner) {
	t.Helper()
	runner := &fakeRunner{}
	reconciler := NewReconciler(context.Background(), runner, filepath.Join(t.TempDir(), "state.json"), nil)
	monitor := NewHealthMonitor(reconciler, nil)
	metrics := NewMetricsCollector()
	server := NewServer(context.Background(), runner, reconciler, monitor, metrics, "test-api-key-0123456789", "1.0.0", nil)
	return server, runner
}

func TestHealthEndpointIsUnauthenticated(t *testing.T) {
	server, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestStateEndpointRequiresValidAPIKey(t *testing.T) {
	server, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/state", nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing key: status = %d, want 401", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/state", nil)
	req.Header.Set("Authorization", "Bearer wrong-key")
	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong key: status = %d, want 401", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/state", nil)
	req.Header.Set("Authorization", "Bearer test-api-key-0123456789")
	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("correct key: status = %d, want 200", rec.Code)
	}
	var report map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&report); err != nil {
		t.Fatal(err)
	}
	if report["agent_version"] != "1.0.0" {
		t.Fatalf("agent_version = %v, want 1.0.0", report["agent_version"])
	}
	if report["apply_state"] != "waiting" {
		t.Fatalf("apply_state = %v, want waiting", report["apply_state"])
	}
}

func TestApplyEndpointReconcilesAndUpdatesState(t *testing.T) {
	server, runner := newTestServer(t)
	runner.calls = nil

	desired := domain.NodeDesiredState{
		IngressAddress: "198.51.100.10",
		Revision:       3,
		ProfileName:    "Test",
		Config: domain.ProfileConfig{
			SchemaVersion: domain.SchemaVersion,
			Inbounds: []domain.Inbound{{
				ID: "in1", Name: "Rule", Enabled: true, ListenAddress: "198.51.100.10", ListenPort: 8002, UDP: true,
			}},
			Outbounds: []domain.Outbound{{
				ID: "out1", Name: "Backend", Address: "192.0.2.10", Port: 9000, Enabled: true, HealthCheck: domain.DefaultHealthCheck(),
			}},
			Bindings: []domain.Binding{{
				ID: "b1", Enabled: true, InboundID: "in1", SelectionStrategy: domain.SelectionManual,
				Targets: []domain.BindingTarget{{OutboundID: "out1", WeightPercent: 100}},
			}},
		},
	}
	body, err := json.Marshal(desired)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/apply", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer test-api-key-0123456789")
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("apply status = %d, body = %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/state", nil)
	req.Header.Set("Authorization", "Bearer test-api-key-0123456789")
	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	var report map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&report); err != nil {
		t.Fatal(err)
	}
	if report["apply_state"] != "applied" {
		t.Fatalf("apply_state after apply = %v, want applied", report["apply_state"])
	}
	if report["applied_revision"].(float64) != 3 {
		t.Fatalf("applied_revision = %v, want 3", report["applied_revision"])
	}

	var sawIPVSAdd bool
	for _, call := range runner.calls {
		if call.name == "ipvsadm" && len(call.args) > 0 && call.args[0] == "-A" {
			sawIPVSAdd = true
		}
	}
	if !sawIPVSAdd {
		t.Fatalf("apply did not create any IPVS service: %#v", runner.calls)
	}
}

func TestApplyEndpointRejectsInvalidConfig(t *testing.T) {
	server, _ := newTestServer(t)
	desired := domain.NodeDesiredState{
		Revision: 1,
		Config:   domain.ProfileConfig{SchemaVersion: 999},
	}
	body, err := json.Marshal(desired)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/apply", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer test-api-key-0123456789")
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
}

func TestUpdateEndpointShortCircuitsOnCurrentOrOlderVersion(t *testing.T) {
	server, _ := newTestServer(t)
	body := strings.NewReader(`{"target_version":"0.9.0"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/update", body)
	req.Header.Set("Authorization", "Bearer test-api-key-0123456789")
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp["update_state"] != "completed" {
		t.Fatalf("update_state = %v, want completed", resp["update_state"])
	}
}

func TestDecommissionEndpointRemovesOnlyManagedState(t *testing.T) {
	server, runner := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/decommission", nil)
	req.Header.Set("Authorization", "Bearer test-api-key-0123456789")
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	for _, call := range runner.calls {
		joined := strings.Join(call.args, " ")
		if call.name == "ipvsadm" && joined == "-C" {
			t.Fatal("decommission attempted to clear the global IPVS table")
		}
	}
}
