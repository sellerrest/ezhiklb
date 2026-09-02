package agent

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var releaseVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z]+(?:\.[0-9A-Za-z]+)*)?$`)

// ReleaseRepo is "owner/repo" on GitHub that self-update downloads signed
// release archives from. This fork publishes its own releases (a different
// asset name and repo than the upstream project), so this is overridable via
// EZHIKLB_UPDATE_REPO — set the default once you know where you'll publish
// releases; until then self-update will simply fail its download step
// (verified, not silently — see InstallAgentUpdate's error path).
var ReleaseRepo = "sellerrest/ezhiklb"

// Update stage names reported to the panel via heartbeat while an update is
// in progress, so the UI can show real (coarse-grained) progress instead of
// a single opaque "updating" state.
const (
	UpdateStageDownloading = "downloading"
	UpdateStageVerifying   = "verifying"
	UpdateStageInstalling  = "installing"
)

// InstallAgentUpdate downloads an official release, verifies SHA-256 and
// atomically replaces only the agent binary. No command comes from the panel.
// onStage, if non-nil, is called as each stage starts so the caller can
// report progress upstream before the (possibly slow) step runs.
func InstallAgentUpdate(ctx context.Context, version string, onStage func(stage string)) error {
	if !releaseVersionPattern.MatchString(version) { return fmt.Errorf("invalid update version %q", version) }
	if onStage == nil { onStage = func(string) {} }
	asset := fmt.Sprintf("ezhiklb-node-agent_%s_linux_amd64.tar.gz", version)
	base := fmt.Sprintf("https://github.com/%s/releases/download/v%s/", ReleaseRepo, version)
	onStage(UpdateStageDownloading)
	archive, err := download(ctx, base+asset, 256<<20); if err != nil { return err }
	checksum, err := download(ctx, base+asset+".sha256", 4096); if err != nil { return err }
	onStage(UpdateStageVerifying)
	want := strings.Fields(string(checksum)); if len(want) == 0 { return fmt.Errorf("empty checksum file") }
	got := sha256.Sum256(archive); if !strings.EqualFold(hex.EncodeToString(got[:]), want[0]) { return fmt.Errorf("release checksum mismatch") }
	onStage(UpdateStageInstalling)
	binary, err := extractAgent(archive); if err != nil { return err }
	current, err := os.Executable(); if err != nil { return err }
	tmp, err := os.CreateTemp(filepath.Dir(current), ".ezhiklb-agent-update-*"); if err != nil { return err }
	tmpName := tmp.Name(); defer os.Remove(tmpName)
	if _, err = tmp.Write(binary); err == nil { err = tmp.Sync() }
	if closeErr := tmp.Close(); err == nil { err = closeErr }; if err != nil { return err }
	if err = os.Chmod(tmpName, 0755); err != nil { return err }
	return os.Rename(tmpName, current)
}

func download(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil); if err != nil { return nil, err }
	res, err := http.DefaultClient.Do(req); if err != nil { return nil, err }; defer res.Body.Close()
	if res.StatusCode != http.StatusOK { return nil, fmt.Errorf("download %s: %s", url, res.Status) }
	return io.ReadAll(io.LimitReader(res.Body, limit))
}

func extractAgent(data []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data)); if err != nil { return nil, err }; defer gz.Close()
	tr := tar.NewReader(gz)
	for { header, err := tr.Next(); if err == io.EOF { break }; if err != nil { return nil, err }
		if filepath.Base(header.Name) == "ezhiklb-agent" && header.Typeflag == tar.TypeReg { return io.ReadAll(io.LimitReader(tr, 128<<20)) }
	}
	return nil, fmt.Errorf("ezhiklb-agent is missing from release archive")
}
