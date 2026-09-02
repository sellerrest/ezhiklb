# Test checklist

## What's already verified by automated tests

- `panel/tests/` (34 tests, `pytest`): domain validation (port of
  `model_test.go`/`version_test.go`), store CRUD/versioning/audit-pruning
  (temp SQLite file), poller diff-apply/decommission branching against a
  fake node client, and full API integration tests via
  `httpx.ASGITransport` against the real FastAPI app.
- `node-agent/` (25 tests, `go test ./...`): the reconciler test suite
  ported from upstream (`TestApplyIPVSNeverClearsGlobalTable`,
  `TestResetConnectionStateIsScopedToManagedServices`,
  `TestRestoreRebuildsSavedDataPlane`, etc. — all against a fake command
  runner, no real `ipvsadm` needed), plus new tests for the local control
  API (`server_test.go`: auth, apply/update/decommission endpoints) and
  the enrollment identity (`enroll_test.go`: generated-once, stable
  across restarts, connection block contains every pasteable field).
- `web/`: `npm run typecheck` and `npm run build` (both clean), plus a
  manual Playwright pass driving a real login → Обзор → Ядра → Узлы →
  "Добавить узел" dialog → core editor flow against a live panel, with
  `console --errors` checked at each step.

Run all three before trusting a change:

```bash
cd panel && .venv/Scripts/python -m pytest -q       # or bin/python on Linux
cd node-agent && go test ./...
cd web && npm run typecheck && npm run build
```

## What still needs a real Debian/Ubuntu VPS with root

Everything below needs actual `ipvsadm`/`iptables`/kernel IPVS modules —
not exercisable from a dev machine without root and Linux. Run these on
disposable test VPS nodes, the same way upstream's own `TESTING.md`
required (see git history for that document's full historical checklist
if you need the original beta-cycle scenarios; this version is trimmed to
what's specific to this fork's changes).

### Install

1. Build a release bundle: `panel/` + `web/dist/` (via `npm run build`)
   for the panel; `node-agent/bin/ezhiklb-agent` (via
   `go build -o bin/ezhiklb-agent ./cmd/ezhiklb-agent`) + `docker/` for
   the node.
2. `sudo ./scripts/install-panel.sh` on one VPS. Choose SQLite first;
   repeat the exercise later against a real PostgreSQL instance
   (`EZHIKLB_DATABASE_URL=postgresql+asyncpg://...`) and confirm identical
   behaviour.
3. `sudo ./scripts/install-node.sh` on a second, disposable VPS. Confirm
   it prints a connection block (address, port, API key, full PEM
   certificate) and that the same block is saved at
   `/var/lib/ezhiklb-agent/enroll/connection.txt`.
4. Paste that block into the panel's **Узлы → Добавить узел** dialog.
   Confirm the dialog's "Проверить статус" reports connected within a few
   seconds, and that the node shows `online` in the Узлы table without a
   page refresh (the 5s poll should pick it up on its own).
5. Repeat step 3 with `--docker` instead, on a third VPS. Confirm
   `docker logs ezhiklb-node` shows the control API starting and the
   same connection block appears.

### Protocol inversion (point 2) — the core new behaviour

6. With a node online and a core assigned, run
   `sudo iptables -A INPUT -p tcp --dport <control_port> -j DROP` on the
   node to simulate the *panel* losing the ability to reach it (not the
   reverse). Publish an unrelated core change from the panel. Confirm the
   node's IPVS/iptables rules from before the block keep working for
   client traffic the whole time — the node never needed the panel to
   keep forwarding.
7. Remove the DROP rule. Confirm the panel's next poll (within
   `poll_interval_seconds`) successfully reaches the node, and that the
   pending core change is applied within one or two more polls, without
   the node card ever showing a manual "reconnect" action.
8. Reboot the node VPS entirely while the panel is *also* stopped.
   Confirm `systemctl status ezhiklb-agent` is active and
   `sudo ipvsadm -Ln` already shows the last-applied services before you
   start the panel back up — this is `Reconciler.Restore()` running on
   boot, independent of the panel.
9. Stop the panel, then run `sudo ./scripts/install-node.sh` on a fourth
   VPS (a brand-new node while the panel is down). Confirm the connection
   block still prints — the node's own enrollment never depended on the
   panel being reachable — and that pasting it into the panel a few
   minutes later (once the panel is back) works normally.

### TLS pinning

10. Edit a node's `cert_pem` in the panel's edit-node dialog to a
    different (but validly formed) self-signed certificate. Confirm the
    next poll fails (node shows offline / a certificate error in panel
    logs) — the panel refuses to talk to anything that doesn't present
    the exact pinned certificate.
11. Delete `/var/lib/ezhiklb-agent/enroll/` on a node and restart
    `ezhiklb-agent`. Confirm it generates a *new* API key and certificate
    (compare `enroll/connection.txt` before/after) and that the panel
    shows the node offline until you paste the new values into its
    edit-node dialog.

### Everything inherited from upstream — re-verify it still holds

12. UDP idle-resume: connect a real UDP client (e.g. a VPN) through a
    listener with Affinity ≥ 1h, lock/background the client for 5-20
    minutes, resume, and confirm it doesn't need to reconnect. Capture
    `sudo ipvsadm -Lnc` and `sudo conntrack -L -p udp -o extended` before
    and after.
13. Weighted distribution: two backends, weights 2/1, confirm ~66/33
    split over many independent flows.
14. ICMP health-check: make one backend unreachable, confirm its IPVS
    weight goes to zero after `failure_threshold`, recovers after
    `recovery_threshold`.
15. One-shot connection reset: enable it on a publish, confirm active
    sessions on assigned nodes are interrupted exactly once and it is not
    repeated on the next heartbeat/apply.
16. Self-update: point `agent.ReleaseRepo` at a real release, click
    "Обновить" on a node, confirm the progress bar advances through
    downloading/verifying/installing/restarting and the node comes back
    on the new version with existing IPVS routes untouched.
17. Decommission: delete an online node, confirm its EzhikLB-owned IPVS
    services and `EZHIKLB-FORWARD`/`EZHIKLB-SNAT` chains disappear, the
    row leaves the panel only after the node's
    `POST /v1/decommission` is acknowledged, and the systemd unit
    disables itself. Repeat while the node is offline/unreachable and
    confirm the force-delete button appears after the grace period
    instead.
