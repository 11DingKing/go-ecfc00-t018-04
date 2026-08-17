package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultValidates(t *testing.T) {
	cfg := Default()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("default config invalid: %v", err)
	}
	if cfg.Listen != ":50253" {
		t.Fatalf("expected :50253, got %s", cfg.Listen)
	}
	if cfg.SLADuration != 30*time.Minute {
		t.Fatalf("expected 30m SLA, got %v", cfg.SLADuration)
	}
	if cfg.CoreZoneMinAssignees != 2 {
		t.Fatalf("expected 2 core assignees, got %d", cfg.CoreZoneMinAssignees)
	}
}

func TestLoadFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	content := `{"listen":":50253","slaDuration":"45m","deviationThresholdMeters":150,"syncInterval":"3s","gridReuseWindow":"12h","coreZoneMinAssignees":2,"equipmentHoldWindow":"1m"}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.SLADuration != 45*time.Minute {
		t.Fatalf("expected 45m, got %v", cfg.SLADuration)
	}
	if cfg.DeviationThresholdM != 150 {
		t.Fatalf("expected 150m, got %v", cfg.DeviationThresholdM)
	}
	if cfg.GridReuseWindow != 12*time.Hour {
		t.Fatalf("expected 12h, got %v", cfg.GridReuseWindow)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestLoadMissingFileUsesDefaults(t *testing.T) {
	cfg, err := Load("/nonexistent/path/config.json")
	if err != nil {
		t.Fatalf("expected defaults on missing file, got %v", err)
	}
	if cfg.SLADuration != 30*time.Minute {
		t.Fatalf("expected default 30m, got %v", cfg.SLADuration)
	}
}

func TestEnvOverride(t *testing.T) {
	t.Setenv("PATROL_LISTEN", ":50300")
	t.Setenv("PATROL_SLA", "10m")
	t.Setenv("PATROL_DEVIATION_M", "300")
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Listen != ":50300" {
		t.Fatalf("expected :50300, got %s", cfg.Listen)
	}
	if cfg.SLADuration != 10*time.Minute {
		t.Fatalf("expected 10m, got %v", cfg.SLADuration)
	}
	if cfg.DeviationThresholdM != 300 {
		t.Fatalf("expected 300m, got %v", cfg.DeviationThresholdM)
	}
}
