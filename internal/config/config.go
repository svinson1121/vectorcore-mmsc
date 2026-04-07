package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Database DatabaseConfig `yaml:"database"`
	MM1      MM1Config      `yaml:"mm1"`
	MM3      MM3Config      `yaml:"mm3"`
	MM4      MM4Config      `yaml:"mm4"`
	MM7      MM7Config      `yaml:"mm7"`
	API      APIConfig      `yaml:"api"`
	Store    StoreConfig    `yaml:"store"`
	Adapt    AdaptConfig    `yaml:"adapt"`
	Limits   LimitsConfig   `yaml:"limits"`
	Billing  BillingConfig  `yaml:"billing"`
	Log      LogConfig      `yaml:"log"`
}

type DatabaseConfig struct {
	Driver                string        `yaml:"driver"`
	DSN                   string        `yaml:"dsn"`
	MaxOpenConns          int           `yaml:"max_open_conns"`
	MaxIdleConns          int           `yaml:"max_idle_conns"`
	RuntimeReloadInterval time.Duration `yaml:"runtime_reload_interval"`
}

type MM1Config struct {
	Listen           string `yaml:"listen"`
	RetrieveBaseURL  string `yaml:"retrieve_base_url"`
	MaxBodySizeBytes int64  `yaml:"max_body_size_bytes"`
}

type MM3Config struct {
	InboundListen       string `yaml:"inbound_listen"`
	MaxMessageSizeBytes int64  `yaml:"max_message_size_bytes"`
	TLSCertFile         string `yaml:"tls_cert_file"`
	TLSKeyFile          string `yaml:"tls_key_file"`
}

type MM4Config struct {
	InboundListen       string `yaml:"inbound_listen"`
	Hostname            string `yaml:"hostname"`
	MaxMessageSizeBytes int64  `yaml:"max_message_size_bytes"`
	TLSCertFile         string `yaml:"tls_cert_file"`
	TLSKeyFile          string `yaml:"tls_key_file"`
}

type MM7Config struct {
	Listen      string `yaml:"listen"`
	Path        string `yaml:"path"`
	EAIFPath    string `yaml:"eaif_path"`
	Version     string `yaml:"version"`
	EAIFVersion string `yaml:"eaif_version"`
	Namespace   string `yaml:"namespace"`
}

type APIConfig struct {
	Listen string `yaml:"listen"`
}

type StoreConfig struct {
	Backend    string            `yaml:"backend"`
	Filesystem FilesystemConfig  `yaml:"filesystem"`
	S3         S3Config          `yaml:"s3"`
	Tiered     TieredStoreConfig `yaml:"tiered"`
}

type FilesystemConfig struct {
	Root string `yaml:"root"`
}

type S3Config struct {
	Endpoint  string `yaml:"endpoint"`
	Bucket    string `yaml:"bucket"`
	AccessKey string `yaml:"access_key"`
	SecretKey string `yaml:"secret_key"`
	Region    string `yaml:"region"`
}

type TieredStoreConfig struct {
	OffloadAfter time.Duration `yaml:"offload_after"`
	LocalCache   bool          `yaml:"local_cache"`
}

type AdaptConfig struct {
	Enabled     bool   `yaml:"enabled"`
	LibvipsPath string `yaml:"libvips_path"`
	FFmpegPath  string `yaml:"ffmpeg_path"`
}

type LimitsConfig struct {
	MaxMessageSizeBytes    int64         `yaml:"max_message_size_bytes"`
	DefaultMessageExpiry   time.Duration `yaml:"default_message_expiry"`
	MaxMessageRetention    time.Duration `yaml:"max_message_retention"`
}

type BillingConfig struct {
	Enabled     bool          `yaml:"enabled"`
	ExportDir   string        `yaml:"export_dir"`
	Interval    time.Duration `yaml:"interval"`
	Tenant      string        `yaml:"tenant"`
	ReqType     string        `yaml:"req_type"`
	NodeID      string        `yaml:"node_id"`
}

type LogConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
	File   string `yaml:"file"`
}

func Load(path string) (*Config, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve config path: %w", err)
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	cfg := Default()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	cfg.resolvePaths(filepath.Dir(absPath))

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func Default() *Config {
	return &Config{
		Database: DatabaseConfig{
			Driver:                "postgres",
			MaxOpenConns:          20,
			MaxIdleConns:          5,
			RuntimeReloadInterval: 5 * time.Second,
		},
		MM1: MM1Config{
			Listen:           ":8002",
			MaxBodySizeBytes: 10 * 1024 * 1024,
		},
		MM3: MM3Config{
			InboundListen:       ":2026",
			MaxMessageSizeBytes: 10 * 1024 * 1024,
		},
		MM4: MM4Config{
			InboundListen:       ":2025",
			MaxMessageSizeBytes: 10 * 1024 * 1024,
		},
		MM7: MM7Config{
			Listen:      ":8007",
			Path:        "/mm7",
			EAIFPath:    "/eaif",
			Version:     "5.3.0",
			EAIFVersion: "3.0",
			Namespace:   "http://www.3gpp.org/ftp/Specs/archive/23_series/23.140/schema/REL-5-MM7-1-0",
		},
		API: APIConfig{
			Listen: ":8080",
		},
		Store: StoreConfig{
			Backend: "filesystem",
			Tiered: TieredStoreConfig{
				OffloadAfter: time.Hour,
				LocalCache:   true,
			},
		},
		Adapt: AdaptConfig{
			Enabled: false,
		},
		Limits: LimitsConfig{
			MaxMessageSizeBytes:  5 * 1024 * 1024, // 5 MB
			DefaultMessageExpiry: 7 * 24 * time.Hour,
			MaxMessageRetention:  30 * 24 * time.Hour,
		},
		Billing: BillingConfig{
			Enabled:  false,
			ExportDir: "./data/billing",
			Interval:  time.Hour,
			Tenant:   "cgrates.org",
			ReqType:  "*postpaid",
		},
		Log: LogConfig{
			Level:  "info",
			Format: "json",
			File:   "./log/vectorcore-mmsc.log",
		},
	}
}

func (c *Config) Validate() error {
	if c.Database.Driver == "" {
		return errors.New("database.driver is required")
	}
	if c.Database.DSN == "" {
		return errors.New("database.dsn is required")
	}
	if c.API.Listen == "" {
		return errors.New("api.listen is required")
	}
	if c.Store.Backend == "" {
		return errors.New("store.backend is required")
	}
	if c.Log.File == "" {
		return errors.New("log.file is required")
	}
	switch c.Store.Backend {
	case "filesystem":
		if c.Store.Filesystem.Root == "" {
			return errors.New("store.filesystem.root is required for filesystem backend")
		}
	case "s3":
		if c.Store.S3.Bucket == "" {
			return errors.New("store.s3.bucket is required for s3 backend")
		}
	case "tiered":
		if c.Store.Filesystem.Root == "" {
			return errors.New("store.filesystem.root is required for tiered backend")
		}
		if c.Store.S3.Bucket == "" {
			return errors.New("store.s3.bucket is required for tiered backend")
		}
	default:
		return fmt.Errorf("unsupported store backend %q", c.Store.Backend)
	}
	return nil
}

func (c *Config) resolvePaths(baseDir string) {
	if strings.EqualFold(c.Database.Driver, "sqlite") {
		c.Database.DSN = resolveSQLiteDSN(baseDir, c.Database.DSN)
	}
	c.Store.Filesystem.Root = resolvePath(baseDir, c.Store.Filesystem.Root)
	c.MM3.TLSCertFile = resolvePath(baseDir, c.MM3.TLSCertFile)
	c.MM3.TLSKeyFile = resolvePath(baseDir, c.MM3.TLSKeyFile)
	c.MM4.TLSCertFile = resolvePath(baseDir, c.MM4.TLSCertFile)
	c.MM4.TLSKeyFile = resolvePath(baseDir, c.MM4.TLSKeyFile)
	c.Adapt.LibvipsPath = resolvePath(baseDir, c.Adapt.LibvipsPath)
	c.Adapt.FFmpegPath = resolvePath(baseDir, c.Adapt.FFmpegPath)
	c.Log.File = resolvePath(baseDir, c.Log.File)
}

func resolvePath(baseDir, value string) string {
	if value == "" || filepath.IsAbs(value) {
		return value
	}
	return filepath.Clean(filepath.Join(baseDir, value))
}

func resolveSQLiteDSN(baseDir, dsn string) string {
	if dsn == "" || dsn == ":memory:" || dsn == "file::memory:" || strings.HasPrefix(dsn, "file::memory:?") {
		return dsn
	}
	if strings.HasPrefix(dsn, "file:") {
		name, query, hasQuery := strings.Cut(strings.TrimPrefix(dsn, "file:"), "?")
		if name == "" || filepath.IsAbs(name) {
			return dsn
		}
		resolved := "file:" + filepath.Clean(filepath.Join(baseDir, name))
		if hasQuery {
			return resolved + "?" + query
		}
		return resolved
	}
	if strings.Contains(dsn, "://") || filepath.IsAbs(dsn) {
		return dsn
	}
	return filepath.Clean(filepath.Join(baseDir, dsn))
}
