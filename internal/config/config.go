// Package config holds runtime configuration for the patrol dispatch
// service. Configuration is loaded from a JSON file and can be overridden by
// environment variables so the same binary runs in containers and locally.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config is the resolved runtime configuration.
type Config struct {
	Listen                 string        `json:"listen"`
	SLADuration            time.Duration `json:"-"`
	SLADurationRaw         string        `json:"slaDuration"`
	DeviationThresholdM    float64       `json:"deviationThresholdMeters"`
	SyncInterval           time.Duration `json:"-"`
	SyncIntervalRaw        string        `json:"syncInterval"`
	GridReuseWindow        time.Duration `json:"-"`
	GridReuseWindowRaw     string        `json:"gridReuseWindow"`
	CoreZoneMinAssignees   int           `json:"coreZoneMinAssignees"`
	EquipmentHoldWindow    time.Duration `json:"-"`
	EquipmentHoldWindowRaw string        `json:"equipmentHoldWindow"`
}

// Default returns a configuration with sensible production defaults that
// satisfy the business rules (30-minute SLA, 200m deviation, 24h reuse window).
func Default() Config {
	return Config{
		Listen:                 ":50253",
		SLADurationRaw:         "30m",
		SLADuration:            30 * time.Minute,
		DeviationThresholdM:    200,
		SyncIntervalRaw:        "5s",
		SyncInterval:           5 * time.Second,
		GridReuseWindowRaw:     "24h",
		GridReuseWindow:        24 * time.Hour,
		CoreZoneMinAssignees:   2,
		EquipmentHoldWindowRaw: "2m",
		EquipmentHoldWindow:    2 * time.Minute,
	}
}

// Load reads configuration from the given JSON path. If the path is empty or
// the file is missing the defaults are used. Environment variables override
// file values where present.
func Load(path string) (Config, error) {
	cfg := Default()
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			return Config{}, fmt.Errorf("read config %s: %w", path, err)
		}
		if err == nil && len(data) > 0 {
			if err := json.Unmarshal(data, &cfg); err != nil {
				return Config{}, fmt.Errorf("parse config %s: %w", path, err)
			}
		}
	}
	if cfg.SLADurationRaw != "" {
		d, err := time.ParseDuration(cfg.SLADurationRaw)
		if err != nil {
			return Config{}, fmt.Errorf("parse slaDuration: %w", err)
		}
		cfg.SLADuration = d
	}
	if cfg.SyncIntervalRaw != "" {
		d, err := time.ParseDuration(cfg.SyncIntervalRaw)
		if err != nil {
			return Config{}, fmt.Errorf("parse syncInterval: %w", err)
		}
		cfg.SyncInterval = d
	}
	if cfg.GridReuseWindowRaw != "" {
		d, err := time.ParseDuration(cfg.GridReuseWindowRaw)
		if err != nil {
			return Config{}, fmt.Errorf("parse gridReuseWindow: %w", err)
		}
		cfg.GridReuseWindow = d
	}
	if cfg.EquipmentHoldWindowRaw != "" {
		d, err := time.ParseDuration(cfg.EquipmentHoldWindowRaw)
		if err != nil {
			return Config{}, fmt.Errorf("parse equipmentHoldWindow: %w", err)
		}
		cfg.EquipmentHoldWindow = d
	}
	cfg.applyEnv()
	if cfg.CoreZoneMinAssignees < 1 {
		cfg.CoreZoneMinAssignees = 2
	}
	if cfg.DeviationThresholdM <= 0 {
		cfg.DeviationThresholdM = 200
	}
	if cfg.Listen == "" {
		cfg.Listen = ":50253"
	}
	return cfg, nil
}

func (c *Config) applyEnv() {
	if v := os.Getenv("PATROL_LISTEN"); v != "" {
		c.Listen = v
	}
	if v := os.Getenv("PATROL_SLA"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.SLADuration = d
		}
	}
	if v := os.Getenv("PATROL_DEVIATION_M"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			c.DeviationThresholdM = f
		}
	}
	if v := os.Getenv("PATROL_SYNC_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.SyncInterval = d
		}
	}
}

// Validate returns an error if the configuration violates business invariants.
func (c Config) Validate() error {
	if c.SLADuration <= 0 {
		return fmt.Errorf("slaDuration must be positive")
	}
	if c.SyncInterval <= 0 {
		return fmt.Errorf("syncInterval must be positive")
	}
	if c.GridReuseWindow <= 0 {
		return fmt.Errorf("gridReuseWindow must be positive")
	}
	if c.DeviationThresholdM <= 0 {
		return fmt.Errorf("deviationThresholdMeters must be positive")
	}
	if c.CoreZoneMinAssignees < 2 {
		return fmt.Errorf("coreZoneMinAssignees must be at least 2")
	}
	return nil
}
