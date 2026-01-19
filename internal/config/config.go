package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config holds all configuration for the push gateway server.
type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Firebase FirebaseConfig `yaml:"firebase"`
	OurCloud OurCloudConfig `yaml:"ourcloud"`
	Batch    BatchConfig    `yaml:"batch"`
	Status   StatusConfig   `yaml:"status"`
}

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	Port         int           `yaml:"port"`
	ReadTimeout  time.Duration `yaml:"read_timeout"`
	WriteTimeout time.Duration `yaml:"write_timeout"`
}

// FirebaseConfig holds Firebase Admin SDK settings.
type FirebaseConfig struct {
	CredentialsFile string `yaml:"credentials_file"`
	ProjectID       string `yaml:"project_id"`
}

// OurCloudConfig holds OurCloud DHT connection settings.
type OurCloudConfig struct {
	GRPCAddress string `yaml:"grpc_address"`
}

// BatchConfig holds notification batching settings.
type BatchConfig struct {
	Window      time.Duration `yaml:"window"`
	MaxSize     int           `yaml:"max_size"`
	StoragePath string        `yaml:"storage_path"`
}

// StatusConfig holds delivery status tracking settings.
type StatusConfig struct {
	Retention time.Duration `yaml:"retention"`
}

// Load reads configuration from a YAML file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	cfg.setDefaults()

	return cfg, nil
}

// setDefaults applies default values for unset fields.
func (c *Config) setDefaults() {
	if c.Server.Port == 0 {
		c.Server.Port = 8080
	}
	if c.Server.ReadTimeout == 0 {
		c.Server.ReadTimeout = 30 * time.Second
	}
	if c.Server.WriteTimeout == 0 {
		c.Server.WriteTimeout = 30 * time.Second
	}
	if c.OurCloud.GRPCAddress == "" {
		c.OurCloud.GRPCAddress = "localhost:50051"
	}
	if c.Batch.Window == 0 {
		c.Batch.Window = 60 * time.Second
	}
	if c.Batch.MaxSize == 0 {
		c.Batch.MaxSize = 100
	}
	if c.Batch.StoragePath == "" {
		c.Batch.StoragePath = "/var/lib/pushserver/batches"
	}
	if c.Status.Retention == 0 {
		c.Status.Retention = time.Hour
	}
}
