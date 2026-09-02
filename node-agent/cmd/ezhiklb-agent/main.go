// Command ezhiklb-agent is the EzhikLB node agent. Unlike the upstream Go
// project's agent, it does not poll a panel-owned inbound port: it runs its
// own small local control API (TLS + API-key authenticated) that the panel
// dials out to. See node-agent's package docs and docs/ARCHITECTURE.md for
// why: the panel needs no inbound-reachable port beyond its own admin UI,
// and the node keeps forwarding traffic indefinitely through a panel outage
// because Reconciler.Restore() rebuilds the last applied state on every
// boot, independent of whether the panel is reachable at all.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"ezhiklb-node-agent/internal/agent"
)

const version = "1.0.0"

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	statePath := env("EZHIKLB_AGENT_STATE", "/var/lib/ezhiklb-agent/state.json")
	enrollDir := env("EZHIKLB_AGENT_ENROLL_DIR", "/var/lib/ezhiklb-agent/enroll")
	host := env("EZHIKLB_AGENT_HOST", "0.0.0.0")
	port := parsePort(logger, "EZHIKLB_AGENT_PORT", "62050")

	if repo := os.Getenv("EZHIKLB_UPDATE_REPO"); repo != "" {
		agent.ReleaseRepo = repo
	}

	runner := agent.ExecRunner{}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	reconciler := agent.NewReconciler(ctx, runner, statePath, logger)
	monitor := agent.NewHealthMonitor(reconciler, logger)
	reconciler.AttachHealthMonitor(monitor)
	metrics := agent.NewMetricsCollector()

	// Rebuild the last committed data plane before anything else — this is
	// what keeps traffic flowing indefinitely through a panel outage or a
	// VPS reboot, entirely independent of whether the panel ever calls back.
	applied, restoreErr := reconciler.Restore(ctx)
	if restoreErr != nil {
		logger.Error("restore last applied state", "error", restoreErr)
	} else if applied > 0 {
		logger.Info("restored last applied state", "revision", applied)
	}

	enrollment, created, err := agent.LoadOrCreateEnrollment(enrollDir)
	if err != nil {
		logger.Error("load or create node enrollment", "error", err)
		os.Exit(1)
	}
	if created {
		printConnectionBlock(logger, enrollment, enrollDir, port)
	}

	server := agent.NewServer(ctx, runner, reconciler, monitor, metrics, enrollment.APIKey, version, logger)
	server.SeedRestoredState(applied, restoreErr)
	if restoreErr == nil && applied > 0 {
		server.StartHealthMonitor()
	}
	server.OnDecommissioned = func() {
		time.Sleep(300 * time.Millisecond) // let the HTTP response flush before disabling/exiting
		_, _ = runner.Run(context.Background(), "systemctl", []string{"disable", "ezhiklb-agent.service"}, "")
		os.Exit(0)
	}

	httpServer := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", host, port),
		Handler:           server.Handler(),
		TLSConfig:         &tls.Config{Certificates: []tls.Certificate{enrollment.TLSCert}},
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("EzhikLB node control API started", "address", httpServer.Addr, "version", version)
		if err := httpServer.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
		}
	}()

	select {
	case <-ctx.Done():
	case err := <-serverErrors:
		logger.Error("control API server failed", "error", err)
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	_ = httpServer.Shutdown(shutdownCtx)
}

// printConnectionBlock is shown once, the moment a node first generates its
// identity — this *is* enrollment point 4: one script, one printed block,
// pasted once into the panel's "Добавить узел" dialog. It is also persisted
// to disk so `cat .../enroll/connection.txt` can recover it later (e.g. to
// re-pair with a different panel) without regenerating the identity.
func printConnectionBlock(logger *slog.Logger, enrollment *agent.Enrollment, enrollDir string, port int) {
	address := env("EZHIKLB_AGENT_PUBLIC_ADDRESS", agent.DetectPublicIPv4())
	if address == "" {
		address = "<IP_ЭТОЙ_VPS>"
	}
	block := enrollment.ConnectionBlock(address, port)
	fmt.Println()
	fmt.Println("===== EzhikLB: подключение узла к панели =====")
	fmt.Println("Скопируйте блок ниже целиком в панели: Узлы → Добавить узел.")
	fmt.Println()
	fmt.Print(block)
	fmt.Println("===============================================")
	connectionFile := filepath.Join(enrollDir, "connection.txt")
	if err := os.WriteFile(connectionFile, []byte(block), 0600); err != nil {
		logger.Warn("save connection block", "error", err)
	} else {
		fmt.Println("Этот блок также сохранён в", connectionFile)
	}
}

func parsePort(logger *slog.Logger, key, fallback string) int {
	port, err := strconv.Atoi(env(key, fallback))
	if err != nil || port < 1 || port > 65535 {
		logger.Error("invalid port", "variable", key)
		os.Exit(1)
	}
	return port
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
